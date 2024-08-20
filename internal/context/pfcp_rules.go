package context

import (
	"fmt"
	"time"

	"github.com/free5gc/pfcp/pfcpType"
)

const (
	RULE_INITIAL RuleState = 0
	RULE_CREATE  RuleState = 1
	RULE_UPDATE  RuleState = 2
	RULE_REMOVE  RuleState = 3
	RULE_QUERY   RuleState = 4
	RULE_SYNCED  RuleState = 5 // SMF notes that UPF successfully received rule via PFCP
)

type RuleState uint8

func (rs RuleState) String() string {
	switch rs {
	case RULE_INITIAL:
		return "RULE_INITIAL"
	case RULE_CREATE:
		return "RULE_CREATE"
	case RULE_UPDATE:
		return "RULE_UPDATE"
	case RULE_REMOVE:
		return "RULE_REMOVE"
	case RULE_QUERY:
		return "RULE_QUERY"
	case RULE_SYNCED:
		return "RULE_SYNCED"
	default:
		return fmt.Sprintf("UNKNOWN_RULE_STATE(%d)", rs)
	}
}

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

func (pdr *PDR) SetState(state RuleState) {
	pdr.State = state
}

func (pdr *PDR) GetState() RuleState {
	return pdr.State
}

func (pdr *PDR) CheckState(expectedState RuleState) bool {
	return pdr.State == expectedState
}

func (pdr *PDR) SetStateRecursive(state RuleState) {
	pdr.State = state
	if pdr.FAR != nil {
		pdr.FAR.State = state
		if pdr.FAR.BAR != nil {
			pdr.FAR.BAR.State = state
		}
	}
	// TODO: really set urr and qer states as well?
	for _, urr := range pdr.URR {
		urr.State = state
	}
	for _, qer := range pdr.QER {
		qer.State = state
	}
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
	QuotaValidityTime      time.Time
	MeasurementInformation pfcpType.MeasurementInformation
	VolumeThreshold        uint64
	VolumeQuota            uint64
	State                  RuleState
}

func (urr *URR) SetState(state RuleState) {
	urr.State = state
}

func (urr *URR) GetState() RuleState {
	return urr.State
}

func (urr *URR) CheckState(expectedState RuleState) bool {
	return urr.State == expectedState
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
		urr.ReportingTrigger.Perio = true
		urr.MeasurementPeriod = time
	}
}

func NewVolumeThreshold(threshold uint64) UrrOpt {
	return func(urr *URR) {
		urr.ReportingTrigger.Volth = true
		urr.VolumeThreshold = threshold
	}
}

func NewVolumeQuota(quota uint64) UrrOpt {
	return func(urr *URR) {
		urr.ReportingTrigger.Volqu = true
		urr.VolumeQuota = quota
	}
}

func SetStartOfSDFTrigger() UrrOpt {
	return func(urr *URR) {
		urr.ReportingTrigger.Start = true
	}
}

func MeasureInformation(isMeasurePkt, isMeasureBeforeQos bool) pfcpType.MeasurementInformation {
	var measureInformation pfcpType.MeasurementInformation
	measureInformation.Mnop = isMeasurePkt
	measureInformation.Mbqe = isMeasureBeforeQos
	return measureInformation
}

func (pdr *PDR) AppendURRs(urrs []*URR) {
	for _, urr := range urrs {
		if !isUrrExist(pdr.URR, urr) {
			pdr.URR = append(pdr.URR, urr)
		}
	}
}

func isUrrExist(urrs []*URR, urr *URR) bool { // check if urr is in URRs list
	for _, URR := range urrs {
		if urr.URRID == URR.URRID {
			return true
		}
	}
	return false
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

func (far *FAR) SetState(state RuleState) {
	far.State = state
}

func (far *FAR) GetState() RuleState {
	return far.State
}

func (far *FAR) CheckState(expectedState RuleState) bool {
	return far.State == expectedState
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

func (bar *BAR) SetState(state RuleState) {
	bar.State = state
}

func (bar *BAR) GetState() RuleState {
	return bar.State
}

func (bar *BAR) CheckState(expectedState RuleState) bool {
	return bar.State == expectedState
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

func (qer *QER) SetState(state RuleState) {
	qer.State = state
}

func (qer *QER) GetState() RuleState {
	return qer.State
}

func (qer *QER) CheckState(expectedState RuleState) bool {
	return qer.State == expectedState
}

func (pdr *PDR) String() string {
	prefix := "  "
	str := fmt.Sprintf("\nPDR %d {\n", pdr.PDRID)
	str += prefix + fmt.Sprintf("State: %s\n", pdr.State)
	str += prefix + fmt.Sprintf("Precedence: %d\n", pdr.Precedence)
	str += prefix + fmt.Sprintf("%s\n", pdr.PDI.String(prefix))
	str += prefix + fmt.Sprintf("OuterHeaderRemoval: %+v\n", pdr.OuterHeaderRemoval)
	str += prefix + fmt.Sprintf("%s\n", pdr.FAR.String(prefix))
	if pdr.URR != nil {
		for _, urr := range pdr.URR {
			str += prefix + fmt.Sprintf("%s\n", urr.String(prefix))
		}
	}
	if pdr.QER != nil {
		for _, qer := range pdr.QER {
			str += prefix + fmt.Sprintf("%s\n", qer.String(prefix))
		}
	}
	str += "}\n"
	return str
}

func (urr *URR) String(prefix string) string {
	if urr == nil {
		return prefix + "URR: nil"
	}
	str := fmt.Sprintf("URR %d {\n", urr.URRID)
	prefix += "  "
	str += prefix + fmt.Sprintf("State: %s\n", urr.State)
	str += prefix + fmt.Sprintf("MeasureMethod: %s\n", urr.MeasureMethod)
	str += prefix + fmt.Sprintf("VolumeThreshold: %d\n", urr.VolumeThreshold)
	prefix = prefix[:len(prefix)-2]
	str += prefix + "}\n"
	return str
}

func SDFFilterToString(filter *pfcpType.SDFFilter, prefix string) string {
	str := prefix + " SDFFilter {\n"
	prefix += "  "
	if filter == nil {
		return str + prefix + "}"
	}
	str += prefix + fmt.Sprintf("SdfFilterId: %d\n", filter.SdfFilterId)
	str += prefix + fmt.Sprintf("Bid: %t\n", filter.Bid)
	str += prefix + fmt.Sprintf("Fl: %t\n", filter.Fl)
	str += prefix + fmt.Sprintf("Spi: %t\n", filter.Spi)
	str += prefix + fmt.Sprintf("Ttc: %t\n", filter.Ttc)
	str += prefix + fmt.Sprintf("Fd: %t\n", filter.Fd)
	str += prefix + fmt.Sprintf("LengthOfFlowDescription: %d\n", filter.LengthOfFlowDescription)
	str += prefix + fmt.Sprintf("FlowDescription: %s\n", string(filter.FlowDescription))
	str += prefix + fmt.Sprintf("TosTrafficClass: %v\n", filter.TosTrafficClass)
	str += prefix + fmt.Sprintf("SecurityParameterIndex: %v\n", filter.SecurityParameterIndex)
	str += prefix + fmt.Sprintf("FlowLabel: %v\n", filter.FlowLabel)
	str += prefix + "}"
	return str
}

func (pdi *PDI) String(prefix string) string {
	if pdi == nil {
		return prefix + "PDI: nil"
	}
	str := "PDI {\n"
	prefix += "  "
	str += prefix + fmt.Sprintf("SourceInterface: %d\n", pdi.SourceInterface.InterfaceValue)
	if pdi.LocalFTeid != nil {
		str += prefix + fmt.Sprintf("%s\n", FTEIDToString(pdi.LocalFTeid, prefix))
	}
	str += prefix + fmt.Sprintf("NetworkInstance: %+v\n", pdi.NetworkInstance)
	str += prefix + fmt.Sprintf("UEIPAddress: %+v\n", pdi.UEIPAddress)
	str += prefix + fmt.Sprintf("SDFFilter: %s\n", SDFFilterToString(pdi.SDFFilter, prefix))
	str += prefix + fmt.Sprintf("ApplicationID: %s\n", pdi.ApplicationID)
	prefix = prefix[:len(prefix)-2]
	str += prefix + "}"
	return str
}

func FTEIDToString(fteid *pfcpType.FTEID, prefix string) string {
	str := "LocalFTEID {\n"
	prefix += "  "
	str += prefix + fmt.Sprintf("Teid: %d\n", fteid.Teid)
	if fteid.V4 {
		str += prefix + fmt.Sprintf("Ipv4Address: %s\n", fteid.Ipv4Address.String())
	}
	if fteid.V6 {
		str += prefix + fmt.Sprintf("Ipv6Address: %s\n", fteid.Ipv6Address.String())
	}
	str += prefix + fmt.Sprintf("ChooseId: %d\n", fteid.ChooseId)
	prefix = prefix[:len(prefix)-2]
	str += prefix + "}"
	return str
}

func (far *FAR) String(prefix string) string {
	if far == nil {
		return prefix + "FAR: nil"
	}
	str := prefix + fmt.Sprintf("FAR %d {\n", far.FARID)
	prefix += "  "
	str += prefix + fmt.Sprintf("State: %s\n", far.State)
	str += prefix + fmt.Sprintf("ApplyAction: %+v\n", far.ApplyAction)
	if far.ForwardingParameters != nil {
		str += prefix + fmt.Sprintf("%s\n", far.ForwardingParameters.String(prefix))
	} else {
		str += prefix + fmt.Sprintln("ForwardingParameters: nil")
	}
	if far.BAR != nil {
		str += prefix + fmt.Sprintf("%s\n", far.BAR.String(prefix))
	}
	prefix = prefix[:len(prefix)-2]
	str += prefix + "}"
	return str
}

func OhcToString(ohc *pfcpType.OuterHeaderCreation, prefix string) string {
	str := prefix + "OuterHeaderCreation {\n"
	prefix += "  "
	str += prefix + fmt.Sprintf("OuterHeaderCreationDescription: %d\n", ohc.OuterHeaderCreationDescription)
	str += prefix + fmt.Sprintf("Teid: %d\n", ohc.Teid)
	str += prefix + fmt.Sprintf("Ipv4Address: %+v\n", ohc.Ipv4Address)
	str += prefix + fmt.Sprintf("Ipv6Address: %+v\n", ohc.Ipv6Address)
	str += prefix + fmt.Sprintf("PortNumber: %d\n", ohc.PortNumber)
	prefix = prefix[:len(prefix)-2]
	str += prefix + "}\n"
	return str
}

func (fp *ForwardingParameters) String(prefix string) string {
	if fp == nil {
		return prefix + "ForwardingParameters: nil"
	}
	str := prefix + "ForwardingParameters {\n"
	prefix += "  "
	str += prefix + fmt.Sprintf("DestinationInterface: %+v\n", fp.DestinationInterface)
	str += prefix + fmt.Sprintf("NetworkInstance: %+v\n", fp.NetworkInstance)
	if fp.OuterHeaderCreation != nil {
		str += prefix + fmt.Sprintf("%s\n", OhcToString(fp.OuterHeaderCreation, prefix))
	}
	prefix = prefix[:len(prefix)-2]
	str += prefix + "}"
	return str
}

func (bar *BAR) String(prefix string) string {
	if bar == nil {
		return prefix + "BAR: nil"
	}
	str := fmt.Sprintf("BAR %d {\n", bar.BARID)
	prefix += "  "
	str += prefix + fmt.Sprintf("State: %s\n", bar.State)
	str += prefix + fmt.Sprintf("DownlinkDataNotificationDelay: %+v\n", bar.DownlinkDataNotificationDelay)
	str += prefix + fmt.Sprintf("SuggestedBufferingPacketsCount: %+v\n", bar.SuggestedBufferingPacketsCount)
	prefix = prefix[:len(prefix)-2]
	str += prefix + "}"
	return str
}

func (qer *QER) String(prefix string) string {
	if qer == nil {
		return prefix + "QER: nil"
	}
	str := fmt.Sprintf("QER %d {\n", qer.QERID)
	prefix += "  "
	str += prefix + fmt.Sprintf("State: %s\n", qer.State)
	str += prefix + fmt.Sprintf("QFI: %+v\n", qer.QFI)
	if qer.GateStatus != nil {
		str += prefix + fmt.Sprintf("GateStatus: DLGate %d, ULGate %d\n", qer.GateStatus.DLGate, qer.GateStatus.ULGate)
	}
	if qer.MBR != nil {
		str += prefix + fmt.Sprintf("MBR: DLMBR %d ULMBR %d\n", qer.MBR.DLMBR, qer.MBR.DLMBR)
	}
	if qer.GBR != nil {
		str += prefix + fmt.Sprintf("GBR: DLGBR %d ULGBR %d\n", qer.GBR.DLGBR, qer.GBR.DLGBR)
	}
	prefix = prefix[:len(prefix)-2]
	str += prefix + "}"
	return str
}
