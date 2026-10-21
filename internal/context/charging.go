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

// pendingChargingRelease owns an immutable JSON snapshot. It stays in SMContext
// until CHF reports success; it is not a restart-persistent settlement queue.
type pendingChargingRelease struct {
	chargingDataRef string
	body            []byte
}

func (smContext *SMContext) NextChargingSequenceNumber() int32 {
	smContext.chargingReleaseMu.Lock()
	defer smContext.chargingReleaseMu.Unlock()
	smContext.chargingInvocationSequenceNumber++
	return smContext.chargingInvocationSequenceNumber
}

// StorePendingChargingRelease freezes the resource reference and full request,
// including its invocation sequence, timestamps and nested session metadata.
func (smContext *SMContext) StorePendingChargingRelease(
	chargingDataRef string, request *models.Chf_ConvCharging_ChargingDataRequest,
) error {
	if chargingDataRef == "" || request == nil {
		return fmt.Errorf("charging release requires a resource reference and request")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode pending charging release: %w", err)
	}
	smContext.chargingReleaseMu.Lock()
	defer smContext.chargingReleaseMu.Unlock()
	if smContext.chargingReleaseRequest != nil {
		return fmt.Errorf("charging release request is already pending")
	}
	smContext.chargingReleaseRequest = &pendingChargingRelease{
		chargingDataRef: chargingDataRef,
		body:            body,
	}
	return nil
}

// PendingChargingRelease returns a detached copy so callers cannot mutate the
// stored request through slices or pointers returned by this method.
func (smContext *SMContext) PendingChargingRelease() (
	string, *models.Chf_ConvCharging_ChargingDataRequest, error,
) {
	smContext.chargingReleaseMu.Lock()
	pending := smContext.chargingReleaseRequest
	smContext.chargingReleaseMu.Unlock()
	if pending == nil {
		return "", nil, nil
	}

	var request models.Chf_ConvCharging_ChargingDataRequest
	if err := json.Unmarshal(pending.body, &request); err != nil {
		return "", nil, fmt.Errorf("decode pending charging release: %w", err)
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
