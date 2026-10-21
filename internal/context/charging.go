package context

import (
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

// BeginChargingRelease elects one caller to send the CHF termination request.
// Only NotStarted can enter InProgress. Failed attempts remain blocked until
// CHF supports duplicate detection and settlement can be retried safely.
func (smContext *SMContext) BeginChargingRelease() bool {
	smContext.chargingReleaseMu.Lock()
	defer smContext.chargingReleaseMu.Unlock()
	if smContext.chargingReleaseState != ChargingReleaseNotStarted {
		return false
	}

	smContext.chargingReleaseState = ChargingReleaseInProgress
	return true
}

// FinishChargingRelease records the outcome; LastErr describes a failure only.
func (smContext *SMContext) FinishChargingRelease(err error) {
	smContext.chargingReleaseMu.Lock()
	defer smContext.chargingReleaseMu.Unlock()
	smContext.chargingReleaseLastErr = err
	if err != nil {
		smContext.chargingReleaseState = ChargingReleaseFailed
	} else {
		smContext.chargingReleaseState = ChargingReleaseSucceeded
	}
}

// ChargingReleaseResult returns state and failure details in one locked snapshot.
// A nil error alone does not imply success: NotStarted and InProgress have no error.
func (smContext *SMContext) ChargingReleaseResult() (ChargingReleaseState, error) {
	smContext.chargingReleaseMu.Lock()
	defer smContext.chargingReleaseMu.Unlock()
	return smContext.chargingReleaseState, smContext.chargingReleaseLastErr
}
