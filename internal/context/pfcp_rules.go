package context

import (
	"time"

	"github.com/free5gc/pfcp/pfcpType"
)

const (
	RULE_INITIAL RuleState = 0
	RULE_CREATE  RuleState = 1
	RULE_UPDATE  RuleState = 2
	RULE_REMOVE  RuleState = 3
)

type RuleState uint8

// Packet Detection Rule. Table 7.5.2.2-1
type PDR struct {
	PDRID uint16

	Precedence         uint32
	PDI                PDI
	OuterHeaderRemoval *pfcpType.OuterHeaderRemoval

	FAR *FAR
	URR []*URR
	QER []*QER

	State RuleState
}

const (
	MeasureInfoMNOP     = 0x10 // Measure Num of Pkts (MNOP)
	MeasureInfoMBQE     = 0x1  // Measure Before Qos Enforce(MQBE)
	MesureMethodVol     = "vol"
	MesureMethodTime    = "time"
	MeasurePeriodReport = 0x0100 // 0x10: PERIO
)

// Usage Report Rule
type URR struct {
	URRID                  uint32
	MeasureMethod          string // vol or time
	ReportingTrigger       pfcpType.ReportingTriggers
	MeasurementPeriod      time.Duration
	MeasurementInformation pfcpType.MeasurementInformation
	VolumeThreshold        uint64
	State                  RuleState
}

type UrrOpt func(urr *URR)

func NewMeasureInformation(isMeasurePkt, isMeasureBeforeQos bool) UrrOpt {
	return func(urr *URR) {
		urr.MeasurementInformation.Mnop = isMeasurePkt
		urr.MeasurementInformation.Mbqe = isMeasureBeforeQos
	}
}

func NewMeasurementPeriod(time time.Duration) UrrOpt {
	return func(urr *URR) {
		urr.MeasurementPeriod = time
	}
}

func NewVolumeThreshold(threshold uint64) UrrOpt {
	return func(urr *URR) {
		urr.VolumeThreshold = threshold
	}
}

func MeasureInformation(isMeasurePkt, isMeasureBeforeQos bool) pfcpType.MeasurementInformation {
	var measureInformation pfcpType.MeasurementInformation
	measureInformation.Mnop = isMeasurePkt
	measureInformation.Mbqe = isMeasureBeforeQos
	return measureInformation
}

// Packet Detection. 7.5.2.2-2
type PDI struct {
	SourceInterface pfcpType.SourceInterface
	LocalFTeid      *pfcpType.FTEID
	NetworkInstance *pfcpType.NetworkInstance
	UEIPAddress     *pfcpType.UEIPAddress
	SDFFilter       *pfcpType.SDFFilter
	ApplicationID   string
}

// Forwarding Action Rule. 7.5.2.3-1
type FAR struct {
	FARID uint32

	ApplyAction          pfcpType.ApplyAction
	ForwardingParameters *ForwardingParameters

	BAR   *BAR
	State RuleState
}

// Forwarding Parameters. 7.5.2.3-2
type ForwardingParameters struct {
	DestinationInterface pfcpType.DestinationInterface
	NetworkInstance      *pfcpType.NetworkInstance
	OuterHeaderCreation  *pfcpType.OuterHeaderCreation
	ForwardingPolicyID   string
	SendEndMarker        bool
}

// Buffering Action Rule 7.5.2.6-1
type BAR struct {
	BARID uint8

	DownlinkDataNotificationDelay  pfcpType.DownlinkDataNotificationDelay
	SuggestedBufferingPacketsCount pfcpType.SuggestedBufferingPacketsCount

	State RuleState
}

// QoS Enhancement Rule
type QER struct {
	QERID uint32

	QFI pfcpType.QFI

	GateStatus *pfcpType.GateStatus
	MBR        *pfcpType.MBR
	GBR        *pfcpType.GBR

	State RuleState
}
