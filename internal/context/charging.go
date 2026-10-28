package context

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/free5gc/openapi/models"
)

type ChargingLevel uint8

// For a rating group that is pdu session charging level, all volume in a pdu session will be charged
// For a rating group that is flow charging level (or Rating group level (32.255)),
// only volume in a flow will be charged
const (
	PduSessionCharging ChargingLevel = iota
	FlowCharging
)

type RequestType uint8

// For each charging event, it will have a corresponding charging request type, see 32.255 Table 5.2.1.4.1
const (
	CHARGING_INIT RequestType = iota
	CHARGING_UPDATE
	CHARGING_RELEASE
)

// ChargingReleaseState describes the lifecycle of the final CHF request.
type ChargingReleaseState uint8

const (
	ChargingReleaseNotStarted ChargingReleaseState = iota
	ChargingReleaseInProgress
	ChargingReleaseSucceeded
	ChargingReleaseFailed
)

// ChargingUpdateState serializes recurring updates. Success returns to Idle;
// a failed operation must be resolved before another update or final release.
type ChargingUpdateState uint8

const (
	ChargingUpdateIdle ChargingUpdateState = iota
	ChargingUpdateInProgress
	ChargingUpdateFailed
	// CHF acknowledged; UPF delivery is not yet complete.
	ChargingUpdateQuotaPending
)

// pendingChargingRequest owns an immutable JSON snapshot retained until the
// invocation and its local continuation complete. It does not survive restart.
type pendingChargingRequest struct {
	chargingDataRef string
	target          string
	body            []byte
}

func (smContext *SMContext) NextChargingSequenceNumber() int32 {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	smContext.chargingInvocationSequenceNumber++
	return smContext.chargingInvocationSequenceNumber
}

// RequestChargingRelease blocks new updates while the existing invocation drains.
// It does not change that invocation or consume the final-release send guard.
func (smContext *SMContext) RequestChargingRelease() {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	smContext.chargingReleaseRequested = true
}

func (smContext *SMContext) BeginChargingUpdate() error {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	if smContext.chargingReleaseRequested ||
		smContext.chargingReleaseState != ChargingReleaseNotStarted || smContext.chargingReleaseRequest != nil {
		return fmt.Errorf("charging session is terminating")
	}
	if smContext.chargingUpdateState == ChargingUpdateFailed {
		return fmt.Errorf("previous charging update remains unsettled: %w", smContext.chargingUpdateLastErr)
	}
	if smContext.chargingUpdateState != ChargingUpdateIdle || smContext.chargingUpdateRequest != nil {
		return fmt.Errorf("charging update is already pending")
	}
	smContext.chargingUpdateState = ChargingUpdateInProgress
	return nil
}

func (smContext *SMContext) FinishChargingUpdate(err error) {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	smContext.chargingUpdateLastErr = err
	if err != nil {
		smContext.chargingUpdateState = ChargingUpdateFailed
	} else {
		smContext.chargingUpdateState = ChargingUpdateIdle
		smContext.chargingUpdateRequest = nil
	}
}

func (smContext *SMContext) ChargingUpdateResult() (ChargingUpdateState, error) {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	return smContext.chargingUpdateState, smContext.chargingUpdateLastErr
}

// BeginChargingRelease elects one caller to send the CHF termination request.
// Only NotStarted can enter InProgress here. Background retries use
// BeginChargingRetry to resend a frozen failed invocation.
func (smContext *SMContext) BeginChargingRelease() bool {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	if smContext.chargingReleaseState != ChargingReleaseNotStarted {
		return false
	}

	if smContext.chargingUpdateState != ChargingUpdateIdle || smContext.chargingUpdateRequest != nil {
		return false
	}

	smContext.chargingReleaseState = ChargingReleaseInProgress
	return true
}

// FinishChargingRelease records the outcome; LastErr describes a failure only.
func (smContext *SMContext) FinishChargingRelease(err error) {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	smContext.chargingReleaseLastErr = err
	if err != nil {
		smContext.chargingReleaseState = ChargingReleaseFailed
	} else {
		smContext.chargingReleaseState = ChargingReleaseSucceeded
		smContext.chargingReleaseRequest = nil
	}
}

// ChargingReleaseResult returns state and failure details in one locked snapshot.
// A nil error alone does not imply success: NotStarted and InProgress have no error.
func (smContext *SMContext) ChargingReleaseResult() (ChargingReleaseState, error) {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	return smContext.chargingReleaseState, smContext.chargingReleaseLastErr
}

func (smContext *SMContext) StorePendingChargingRelease(
	chargingDataRef string, request *models.Chf_ConvCharging_ChargingDataRequest,
) error {
	return smContext.StorePendingChargingRequest(CHARGING_RELEASE, chargingDataRef, request)
}

func (smContext *SMContext) PendingChargingRelease() (
	string, *models.Chf_ConvCharging_ChargingDataRequest, error,
) {
	return smContext.PendingChargingRequest(CHARGING_RELEASE)
}

// StorePendingChargingRequest freezes the resource reference and full request,
// including its invocation sequence, timestamps and nested session metadata.
func (smContext *SMContext) StorePendingChargingRequest(
	requestType RequestType, chargingDataRef string, request *models.Chf_ConvCharging_ChargingDataRequest,
) error {
	if chargingDataRef == "" || request == nil {
		return fmt.Errorf("charging request requires a resource reference and body")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode pending charging request: %w", err)
	}
	pending := &pendingChargingRequest{chargingDataRef: chargingDataRef, body: body}

	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	switch requestType {
	case CHARGING_UPDATE:
		if smContext.chargingUpdateRequest != nil {
			return fmt.Errorf("charging update request is already pending")
		}
		smContext.chargingUpdateRequest = pending
	case CHARGING_RELEASE:
		if smContext.chargingReleaseRequest != nil {
			return fmt.Errorf("charging release request is already pending")
		}
		smContext.chargingReleaseRequest = pending
	default:
		return fmt.Errorf("unsupported pending charging request type: %d", requestType)
	}
	return nil
}

// PendingChargingRequest returns a detached copy that cannot mutate the snapshot.
func (smContext *SMContext) PendingChargingRequest(requestType RequestType) (
	string, *models.Chf_ConvCharging_ChargingDataRequest, error,
) {
	smContext.chargingMu.Lock()
	var pending *pendingChargingRequest
	switch requestType {
	case CHARGING_UPDATE:
		pending = smContext.chargingUpdateRequest
	case CHARGING_RELEASE:
		pending = smContext.chargingReleaseRequest
	default:
		smContext.chargingMu.Unlock()
		return "", nil, fmt.Errorf("unsupported pending charging request type: %d", requestType)
	}
	smContext.chargingMu.Unlock()
	if pending == nil {
		return "", nil, nil
	}

	var request models.Chf_ConvCharging_ChargingDataRequest
	if err := json.Unmarshal(pending.body, &request); err != nil {
		return "", nil, fmt.Errorf("decode pending charging request: %w", err)
	}
	return pending.chargingDataRef, &request, nil
}

type ChargingInfo struct {
	ChargingMethod         models.Chf_ConvCharging_QuotaManagementIndicator
	VolumeLimitExpiryTimer *Timer
	EventLimitExpiryTimer  *Timer
	ChargingLevel          ChargingLevel
	RatingGroup            int32
	UpfId                  string
}

// IsChargingRelatedUrr determines whether a URR is expected to have ChargingInfo.
func IsChargingRelatedUrr(urr *URR) bool {
	if urr == nil {
		return false
	}
	return !urr.MeasurementInformation.HasMNOP() && !urr.MeasurementInformation.HasMBQE()
}

// BeginChargingRetry requires the session lock and an existing frozen request.
// Ordinary callers still cannot start a new invocation over a failed one.
func (smContext *SMContext) BeginChargingRetry(requestType RequestType) bool {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	switch requestType {
	case CHARGING_UPDATE:
		if smContext.chargingUpdateRequest == nil ||
			(smContext.chargingUpdateState != ChargingUpdateFailed &&
				smContext.chargingUpdateState != ChargingUpdateQuotaPending) {
			return false
		}
		smContext.chargingUpdateState = ChargingUpdateInProgress
	case CHARGING_RELEASE:
		if smContext.chargingReleaseRequest == nil || smContext.chargingReleaseState != ChargingReleaseFailed ||
			smContext.chargingUpdateState != ChargingUpdateIdle {
			return false
		}
		smContext.chargingReleaseState = ChargingReleaseInProgress
	default:
		return false
	}
	return true
}

func (smContext *SMContext) ChargingQuotaPending(err error) {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	smContext.chargingUpdateState = ChargingUpdateQuotaPending
	smContext.chargingUpdateLastErr = err
}

var ErrChargingResponseSequence = errors.New("CHF response invocation sequence does not match request")

// ChargingTarget freezes the destination alongside the request, before I/O.
// Once frozen, later CHF discovery changes cannot redirect an unsettled request.
func (smContext *SMContext) ChargingTarget(requestType RequestType, candidate string) (string, error) {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	var pending *pendingChargingRequest
	switch requestType {
	case CHARGING_UPDATE:
		pending = smContext.chargingUpdateRequest
	case CHARGING_RELEASE:
		pending = smContext.chargingReleaseRequest
	default:
		return candidate, nil
	}
	if pending == nil {
		return "", fmt.Errorf("charging request has not been prepared")
	}
	if pending.target == "" {
		pending.target = candidate
	}
	if pending.target == "" {
		return "", fmt.Errorf("charging request has no CHF destination")
	}
	return pending.target, nil
}
