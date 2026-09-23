package message

import (
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	goPfcpMessage "github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

const maxPFCPBitrate = uint64(1<<40 - 1)

var pfcpTimeEpoch = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

// BuildSessionEstablishmentRequest maps SMF rule state directly to concrete
// go-pfcp IEs. Sequence 0 is intentional: TxTransaction assigns the final
// collision-free 24-bit sequence immediately before marshaling the request.
func BuildSessionEstablishmentRequest(
	upNodeID pfcptype.NodeID,
	upN4Addr string,
	upfUUID string,
	smContext *context.SMContext,
	pdrList []*context.PDR,
	farList []*context.FAR,
	barList []*context.BAR,
	qerList []*context.QER,
	urrList []*context.URR,
) (*goPfcpMessage.SessionEstablishmentRequest, error) {
	if smContext == nil {
		return nil, fmt.Errorf("build PFCP Session Establishment Request: nil SM context")
	}

	nodeIP := upN4Addr
	if nodeIP == "" {
		nodeIP = upNodeID.ResolveNodeIdToIp().String()
	}
	sessionContext := smContext.PFCPContext[nodeIP]
	if sessionContext == nil {
		return nil, fmt.Errorf("build PFCP Session Establishment Request: missing PFCP context for UPF %s", nodeIP)
	}
	if sessionContext.LocalSEID == 0 {
		return nil, fmt.Errorf("build PFCP Session Establishment Request: local SEID for UPF %s is zero", nodeIP)
	}

	nodeIDIE, err := newNodeIDIE(context.GetSelf().CPNodeID)
	if err != nil {
		return nil, fmt.Errorf("build PFCP Session Establishment Node ID: %w", err)
	}
	cpAddress := context.GetSelf().ExternalIP()
	if cpAddress == nil {
		return nil, fmt.Errorf("SMF PFCP external address is not configured")
	}
	var cpIPv4, cpIPv6 net.IP
	if cpIPv4 = cpAddress.To4(); cpIPv4 == nil {
		cpIPv6 = cpAddress.To16()
		if cpIPv6 == nil {
			return nil, fmt.Errorf("SMF PFCP external address %q is invalid", cpAddress)
		}
	}
	cpFSEID := ie.NewFSEID(sessionContext.LocalSEID, cpIPv4, cpIPv6)
	if cpFSEID == nil {
		return nil, fmt.Errorf("build PFCP Session Establishment CP F-SEID")
	}

	createPDRs := make([]*ie.IE, 0, len(pdrList))
	createFARs := make([]*ie.IE, 0, len(farList))
	createBARs := make([]*ie.IE, 0, len(barList))
	createQERs := make([]*ie.IE, 0, len(qerList))
	createURRs := make([]*ie.IE, 0, len(urrList))

	// TODO(go-pfcp migration): Building a request still changes rule state to
	// RULE_CREATE before the UPF accepts it. Replace this with a prepared change
	// set and commit it only after a validated accepted response.
	for index, pdr := range pdrList {
		if pdr == nil {
			return nil, fmt.Errorf("build Create PDR at index %d: nil PDR", index)
		}
		if pdr.State == context.RULE_INITIAL {
			createPDR, buildErr := newCreatePDRIE(pdr)
			if buildErr != nil {
				return nil, fmt.Errorf("build Create PDR %d: %w", pdr.PDRID, buildErr)
			}
			createPDRs = append(createPDRs, createPDR)
		}
		pdr.State = context.RULE_CREATE
	}

	for index, far := range farList {
		if far == nil {
			return nil, fmt.Errorf("build Create FAR at index %d: nil FAR", index)
		}
		if far.State == context.RULE_INITIAL {
			createFAR, buildErr := newCreateFARIE(far)
			if buildErr != nil {
				return nil, fmt.Errorf("build Create FAR %d: %w", far.FARID, buildErr)
			}
			createFARs = append(createFARs, createFAR)
		}
		far.State = context.RULE_CREATE
	}

	for index, bar := range barList {
		if bar == nil {
			return nil, fmt.Errorf("build Create BAR at index %d: nil BAR", index)
		}
		if bar.State == context.RULE_INITIAL {
			createBARs = append(createBARs, newCreateBARIE(bar))
		}
		bar.State = context.RULE_CREATE
	}
	if len(createBARs) > 1 {
		return nil, fmt.Errorf("build PFCP Session Establishment Request: %d Create BAR IEs, want at most 1", len(createBARs))
	}

	qerByID := make(map[uint32]*context.QER, len(qerList))
	for index, qer := range qerList {
		if qer == nil {
			return nil, fmt.Errorf("build Create QER at index %d: nil QER", index)
		}
		qerByID[qer.QERID] = qer
	}
	qerIDs := sortedRuleIDs(qerByID)
	for _, qerID := range qerIDs {
		qer := qerByID[qerID]
		if qer.State == context.RULE_INITIAL {
			createQER, buildErr := newCreateQERIE(qer)
			if buildErr != nil {
				return nil, fmt.Errorf("build Create QER %d: %w", qer.QERID, buildErr)
			}
			createQERs = append(createQERs, createQER)
		}
		qer.State = context.RULE_CREATE
	}

	urrByID := make(map[uint32]*context.URR, len(urrList))
	for index, urr := range urrList {
		if urr == nil {
			return nil, fmt.Errorf("build Create URR at index %d: nil URR", index)
		}
		urrByID[urr.URRID] = urr
	}
	urrIDs := sortedRuleIDs(urrByID)
	smContext.Log.Infof("[BuildEstReq] UPF=%s urrList=%d unique_urrs=%d", upfUUID, len(urrList), len(urrByID))
	for _, urrID := range urrIDs {
		urr := urrByID[urrID]
		if smContext.GetUrrState(upfUUID, urr.URRID) == context.RULE_INITIAL {
			createURR, buildErr := newCreateURRIE(urr)
			if buildErr != nil {
				return nil, fmt.Errorf("build Create URR %d: %w", urr.URRID, buildErr)
			}
			createURRs = append(createURRs, createURR)
			smContext.SetUrrState(upfUUID, urr.URRID, context.RULE_CREATE)
		}
	}

	topLevelIEs := make([]*ie.IE, 0,
		2+len(createPDRs)+len(createFARs)+len(createURRs)+len(createQERs)+len(createBARs)+1)
	topLevelIEs = append(topLevelIEs, nodeIDIE, cpFSEID)
	topLevelIEs = append(topLevelIEs, createPDRs...)
	topLevelIEs = append(topLevelIEs, createFARs...)
	topLevelIEs = append(topLevelIEs, createURRs...)
	topLevelIEs = append(topLevelIEs, createQERs...)
	topLevelIEs = append(topLevelIEs, createBARs...)
	topLevelIEs = append(topLevelIEs, ie.NewPDNType(ie.PDNTypeIPv4))

	return goPfcpMessage.NewSessionEstablishmentRequest(1, 0, 0, 0, 0, topLevelIEs...), nil
}

func newCreatePDRIE(pdr *context.PDR) (*ie.IE, error) {
	if pdr.FAR == nil {
		return nil, fmt.Errorf("missing FAR reference")
	}
	pdi, err := newPDIIE(&pdr.PDI)
	if err != nil {
		return nil, err
	}
	children := []*ie.IE{
		ie.NewPDRID(pdr.PDRID),
		ie.NewPrecedence(pdr.Precedence),
		pdi,
	}
	if removal := pdr.OuterHeaderRemoval; removal != nil {
		children = append(children, ie.NewOuterHeaderRemoval(removal.OuterHeaderRemovalDescription, 0))
	}
	children = append(children, ie.NewFARID(pdr.FAR.FARID))
	for _, urr := range pdr.URR {
		if urr != nil {
			children = append(children, ie.NewURRID(urr.URRID))
		}
	}
	for _, qer := range pdr.QER {
		if qer != nil {
			children = append(children, ie.NewQERID(qer.QERID))
		}
	}
	return ie.NewCreatePDR(children...), nil
}

func newPDIIE(pdi *context.PDI) (*ie.IE, error) {
	if pdi.SourceInterface.InterfaceValue > 0x0f {
		return nil, fmt.Errorf("source interface %d exceeds 4 bits", pdi.SourceInterface.InterfaceValue)
	}
	children := []*ie.IE{ie.NewSourceInterface(pdi.SourceInterface.InterfaceValue)}

	if fteid := pdi.LocalFTeid; fteid != nil {
		flags := boolMask(fteid.Chid, 0x08) |
			boolMask(fteid.Ch, 0x04) |
			boolMask(fteid.V6, 0x02) |
			boolMask(fteid.V4, 0x01)
		fteidIE := ie.NewFTEID(flags, fteid.Teid, fteid.Ipv4Address, fteid.Ipv6Address, fteid.ChooseId)
		if fteidIE == nil {
			return nil, fmt.Errorf("invalid Local F-TEID")
		}
		children = append(children, fteidIE)
	}
	if networkInstance := pdi.NetworkInstance; networkInstance != nil {
		children = append(children, newNetworkInstanceIE(networkInstance))
	}
	if ueIP := pdi.UEIPAddress; ueIP != nil {
		ueIPIE, err := newUEIPAddressIE(ueIP)
		if err != nil {
			return nil, err
		}
		children = append(children, ueIPIE)
	}
	if sdf := pdi.SDFFilter; sdf != nil {
		fd, ttc, spi, flowLabel := "", "", "", ""
		var filterID uint32
		if sdf.Fd {
			fd = string(sdf.FlowDescription)
		}
		if sdf.Ttc {
			ttc = string(sdf.TosTrafficClass)
		}
		if sdf.Spi {
			spi = string(sdf.SecurityParameterIndex)
		}
		if sdf.Fl {
			flowLabel = string(sdf.FlowLabel)
		}
		if sdf.Bid {
			filterID = sdf.SdfFilterId
		}
		sdfIE := ie.NewSDFFilter(fd, ttc, spi, flowLabel, filterID)
		if sdfIE == nil {
			return nil, fmt.Errorf("invalid SDF Filter")
		}
		children = append(children, sdfIE)
	}
	if pdi.ApplicationID != "" {
		children = append(children, ie.NewApplicationID(pdi.ApplicationID))
	}
	return ie.NewPDI(children...), nil
}

func newCreateFARIE(far *context.FAR) (*ie.IE, error) {
	children := []*ie.IE{ie.NewFARID(far.FARID)}
	if far.ForwardingParameters == nil {
		children = append(children, ie.NewApplyAction(0x01)) // DROP
	} else {
		children = append(children, ie.NewApplyAction(0x02)) // FORW
		forwardingParameters, err := newForwardingParametersIE(far.ForwardingParameters)
		if err != nil {
			return nil, err
		}
		children = append(children, forwardingParameters)
	}
	if far.BAR != nil {
		children = append(children, ie.NewBARID(far.BAR.BARID))
	}
	return ie.NewCreateFAR(children...), nil
}

func newForwardingParametersIE(parameters *context.ForwardingParameters) (*ie.IE, error) {
	if parameters.DestinationInterface.InterfaceValue > 0x0f {
		return nil, fmt.Errorf("destination interface %d exceeds 4 bits", parameters.DestinationInterface.InterfaceValue)
	}
	children := []*ie.IE{ie.NewDestinationInterface(parameters.DestinationInterface.InterfaceValue)}
	if networkInstance := parameters.NetworkInstance; networkInstance != nil {
		children = append(children, newNetworkInstanceIE(networkInstance))
	}
	if header := parameters.OuterHeaderCreation; header != nil {
		descriptionOctet := uint8(header.OuterHeaderCreationDescription >> 8)
		if descriptionOctet&0x0f == 0 {
			return nil, fmt.Errorf("outer header creation description has no GTP-U/UDP flag")
		}
		if descriptionOctet&0x05 != 0 && header.Ipv4Address.To4() == nil {
			return nil, fmt.Errorf("outer header creation requires an IPv4 address")
		}
		if descriptionOctet&0x0a != 0 &&
			(header.Ipv6Address.To16() == nil || header.Ipv6Address.To4() != nil) {
			return nil, fmt.Errorf("outer header creation requires an IPv6 address")
		}

		headerIE := ie.NewOuterHeaderCreation(
			header.OuterHeaderCreationDescription,
			header.Teid,
			ipString(header.Ipv4Address, true),
			ipString(header.Ipv6Address, false),
			header.PortNumber,
			0,
			0,
		)
		if headerIE == nil {
			return nil, fmt.Errorf("invalid Outer Header Creation")
		}
		children = append(children, headerIE)
	}
	if parameters.ForwardingPolicyID != "" {
		children = append(children, ie.NewForwardingPolicy(parameters.ForwardingPolicyID))
	}
	return ie.NewForwardingParameters(children...), nil
}

func newCreateBARIE(bar *context.BAR) *ie.IE {
	// Downlink Data Notification Delay is not applicable to N4 Create BAR.
	// Omitting it also avoids using zero as an ambiguous default: on interfaces
	// where the IE applies, zero explicitly clears a previously configured delay.
	return ie.NewCreateBAR(ie.NewBARID(bar.BARID))
}

func newCreateQERIE(qer *context.QER) (*ie.IE, error) {
	if qer.QFI.QFI > 63 {
		return nil, fmt.Errorf("QFI %d exceeds 6 bits", qer.QFI.QFI)
	}
	children := []*ie.IE{ie.NewQERID(qer.QERID)}
	if gate := qer.GateStatus; gate != nil {
		if gate.ULGate > 1 || gate.DLGate > 1 {
			return nil, fmt.Errorf("invalid Gate Status UL=%d DL=%d", gate.ULGate, gate.DLGate)
		}
		children = append(children, ie.NewGateStatus(uint8(gate.ULGate), uint8(gate.DLGate)))
	}
	if mbr := qer.MBR; mbr != nil {
		if mbr.ULMBR > maxPFCPBitrate || mbr.DLMBR > maxPFCPBitrate {
			return nil, fmt.Errorf("MBR exceeds 40 bits: UL=%d DL=%d", mbr.ULMBR, mbr.DLMBR)
		}
		children = append(children, ie.NewMBR(mbr.ULMBR, mbr.DLMBR))
	}
	if gbr := qer.GBR; gbr != nil {
		if gbr.ULGBR > maxPFCPBitrate || gbr.DLGBR > maxPFCPBitrate {
			return nil, fmt.Errorf("GBR exceeds 40 bits: UL=%d DL=%d", gbr.ULGBR, gbr.DLGBR)
		}
		children = append(children, ie.NewGBR(gbr.ULGBR, gbr.DLGBR))
	}
	children = append(children, ie.NewQFI(qer.QFI.QFI))
	return ie.NewCreateQER(children...), nil
}

func newCreateURRIE(urr *context.URR) (*ie.IE, error) {
	measurementEvent, measurementVolume, measurementDuration := 0, 0, 0
	switch urr.MeasureMethod {
	case context.MesureMethodVol:
		measurementVolume = 1
	case context.MesureMethodTime:
		measurementDuration = 1
	default:
		return nil, fmt.Errorf("unsupported Measurement Method %q", urr.MeasureMethod)
	}
	children := []*ie.IE{
		ie.NewURRID(urr.URRID),
		ie.NewMeasurementMethod(measurementEvent, measurementVolume, measurementDuration),
		newReportingTriggersIE(urr.ReportingTrigger),
	}
	if urr.MeasurementPeriod != 0 {
		children = append(children, ie.NewMeasurementPeriod(urr.MeasurementPeriod))
	}
	if urr.VolumeThreshold != 0 {
		children = append(children, ie.NewVolumeThreshold(
			0x06, 0, urr.VolumeThreshold, urr.VolumeThreshold,
		))
	}
	if urr.VolumeQuota != 0 {
		children = append(children, ie.NewVolumeQuota(
			0x07, urr.VolumeQuota, urr.VolumeQuota, urr.VolumeQuota,
		))
	}
	children = append(children, newMeasurementInformationIE(urr.MeasurementInformation))
	if !urr.QuotaValidityTime.IsZero() {
		children = append(children, ie.NewQuotaValidityTime(urr.QuotaValidityTime.Sub(pfcpTimeEpoch)))
	}
	return ie.NewCreateURR(children...), nil
}

func newNodeIDIE(nodeID pfcptype.NodeID) (*ie.IE, error) {
	switch nodeID.NodeIdType {
	case pfcptype.NodeIdTypeIpv4Address:
		if nodeID.IP.To4() == nil {
			return nil, fmt.Errorf("invalid IPv4 Node ID %q", nodeID.IP)
		}
		return ie.NewNodeID(nodeID.IP.String(), "", ""), nil
	case pfcptype.NodeIdTypeIpv6Address:
		if nodeID.IP.To16() == nil || nodeID.IP.To4() != nil {
			return nil, fmt.Errorf("invalid IPv6 Node ID %q", nodeID.IP)
		}
		return ie.NewNodeID("", nodeID.IP.String(), ""), nil
	case pfcptype.NodeIdTypeFqdn:
		if nodeID.FQDN == "" {
			return nil, fmt.Errorf("empty FQDN Node ID")
		}
		return ie.NewNodeID("", "", nodeID.FQDN), nil
	default:
		return nil, fmt.Errorf("unsupported Node ID type %d", nodeID.NodeIdType)
	}
}

func newNetworkInstanceIE(instance *pfcptype.NetworkInstance) *ie.IE {
	if instance.FQDNEncoding {
		return ie.NewNetworkInstanceFQDN(instance.NetworkInstance)
	}
	return ie.NewNetworkInstance(instance.NetworkInstance)
}

func newUEIPAddressIE(address *pfcptype.UEIPAddress) (*ie.IE, error) {
	flags := boolMask(address.Ipv6d, 0x08) |
		boolMask(address.Sd, 0x04) |
		boolMask(address.V4, 0x02) |
		boolMask(address.V6, 0x01)
	v4, v6 := "", ""
	if address.V4 {
		if address.Ipv4Address.To4() == nil {
			return nil, fmt.Errorf("UE IP has V4 flag without IPv4 address")
		}
		v4 = address.Ipv4Address.String()
	}
	if address.V6 {
		if address.Ipv6Address.To16() == nil || address.Ipv6Address.To4() != nil {
			return nil, fmt.Errorf("UE IP has V6 flag without IPv6 address")
		}
		v6 = address.Ipv6Address.String()
	}
	return ie.NewUEIPAddress(flags, v4, v6, address.Ipv6PrefixDelegationBits, 0), nil
}

func newReportingTriggersIE(trigger pfcptype.ReportingTrigger) *ie.IE {
	return trigger.IE()
}

func newMeasurementInformationIE(info pfcptype.MeasurementInformation) *ie.IE {
	return ie.NewMeasurementInformation(info.Flags)
}

func sortedRuleIDs[T any](rules map[uint32]T) []uint32 {
	ids := make([]uint32, 0, len(rules))
	for id := range rules {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func boolMask(enabled bool, mask uint8) uint8 {
	if enabled {
		return mask
	}
	return 0
}

func ipString(ip net.IP, ipv4 bool) string {
	if ip == nil {
		return ""
	}
	if ipv4 {
		if value := ip.To4(); value != nil {
			return value.String()
		}
		return ""
	}
	if value := ip.To16(); value != nil && value.To4() == nil {
		return value.String()
	}
	return ""
}
