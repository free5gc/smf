package context

import (
	"encoding/json"
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
)

// pendingChargingRequest owns an immutable JSON snapshot kept until CHF reports
// success. These snapshots are not persisted across SMF restarts.
type pendingChargingRequest struct {
	chargingDataRef string
	body            []byte
}

func (smContext *SMContext) NextChargingSequenceNumber() int32 {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	smContext.chargingInvocationSequenceNumber++
	return smContext.chargingInvocationSequenceNumber
}

func (smContext *SMContext) BeginChargingUpdate() error {
	smContext.chargingMu.Lock()
	defer smContext.chargingMu.Unlock()
	if smContext.chargingReleaseState != ChargingReleaseNotStarted || smContext.chargingReleaseRequest != nil {
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
