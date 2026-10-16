package message

import (
	"fmt"
	"net"

	"github.com/wmnsk/go-pfcp/ie"
	goPfcpMessage "github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

// BuildSessionModificationRequest maps pending SMF rule changes directly to
// go-pfcp grouped IEs. Sequence 0 is assigned by TxTransaction before marshal.
func BuildSessionModificationRequest(
	upN4Addr string,
	upfUUID string,
	smContext *context.SMContext,
	pdrList []*context.PDR,
	farList []*context.FAR,
	barList []*context.BAR,
	qerList []*context.QER,
	urrList []*context.URR,
) (*goPfcpMessage.SessionModificationRequest, error) {
	if smContext == nil {
		return nil, fmt.Errorf("build PFCP Session Modification Request: nil SM context")
	}
	sessionContext := smContext.PFCPContext[upN4Addr]
	if sessionContext == nil {
		return nil, fmt.Errorf("build PFCP Session Modification Request: missing PFCP context for UPF %s", upN4Addr)
	}
	if sessionContext.LocalSEID == 0 || sessionContext.RemoteSEID == 0 {
		return nil, fmt.Errorf(
			"build PFCP Session Modification Request: invalid SEIDs for UPF %s (local=%d remote=%d)",
			upN4Addr, sessionContext.LocalSEID, sessionContext.RemoteSEID,
		)
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

	ies := []*ie.IE{ie.NewFSEID(sessionContext.LocalSEID, cpIPv4, cpIPv6)}

	// TODO(go-pfcp migration): This builder still commits RULE_CREATE before a
	// validated accepted response. Replace it with a prepared change set and
	// explicit unknown-outcome handling for transaction timeout.
	for index, pdr := range pdrList {
		if pdr == nil {
			return nil, fmt.Errorf("build Session Modification PDR at index %d: nil PDR", index)
		}
		var ruleIE *ie.IE
		var err error
		switch pdr.State {
		case context.RULE_INITIAL:
			ruleIE, err = newCreatePDRIE(pdr)
		case context.RULE_UPDATE:
			ruleIE, err = newUpdatePDRIE(pdr)
		case context.RULE_REMOVE:
			ruleIE = ie.NewRemovePDR(ie.NewPDRID(pdr.PDRID))
		}
		if err != nil {
			return nil, fmt.Errorf("build Session Modification PDR %d: %w", pdr.PDRID, err)
		}
		if ruleIE != nil {
			ies = append(ies, ruleIE)
		}
		pdr.State = context.RULE_CREATE
	}

	for index, far := range farList {
		if far == nil {
			return nil, fmt.Errorf("build Session Modification FAR at index %d: nil FAR", index)
		}
		var ruleIE *ie.IE
		var err error
		switch far.State {
		case context.RULE_INITIAL:
			ruleIE, err = newCreateFARIE(far)
		case context.RULE_UPDATE:
			ruleIE, err = newUpdateFARIE(far)
		case context.RULE_REMOVE:
			ruleIE = ie.NewRemoveFAR(ie.NewFARID(far.FARID))
		}
		if err != nil {
			return nil, fmt.Errorf("build Session Modification FAR %d: %w", far.FARID, err)
		}
		if ruleIE != nil {
			ies = append(ies, ruleIE)
		}
		far.State = context.RULE_CREATE
	}

	createBARs := 0
	for index, bar := range barList {
		if bar == nil {
			return nil, fmt.Errorf("build Session Modification BAR at index %d: nil BAR", index)
		}
		if bar.State == context.RULE_INITIAL {
			createBARs++
			ies = append(ies, newCreateBARIE(bar))
		}
	}
	if createBARs > 1 {
		return nil, fmt.Errorf("build PFCP Session Modification Request: %d Create BAR IEs, want at most 1", createBARs)
	}

	qerByID := make(map[uint32]*context.QER, len(qerList))
	for index, qer := range qerList {
		if qer == nil {
			return nil, fmt.Errorf("build Session Modification QER at index %d: nil QER", index)
		}
		qerByID[qer.QERID] = qer
	}
	for _, qerID := range sortedRuleIDs(qerByID) {
		qer := qerByID[qerID]
		if qer.State == context.RULE_INITIAL {
			createQER, err := newCreateQERIE(qer)
			if err != nil {
				return nil, fmt.Errorf("build Session Modification QER %d: %w", qer.QERID, err)
			}
			ies = append(ies, createQER)
		}
		qer.State = context.RULE_CREATE
	}

	urrByID := make(map[uint32]*context.URR, len(urrList))
	for index, urr := range urrList {
		if urr == nil {
			return nil, fmt.Errorf("build Session Modification URR at index %d: nil URR", index)
		}
		urrByID[urr.URRID] = urr
	}
	smContext.Log.Infof("[BuildModReq] UPF=%s urrList=%d unique_urrs=%d", upfUUID, len(urrList), len(urrByID))
	for _, urrID := range sortedRuleIDs(urrByID) {
		urr := urrByID[urrID]
		switch smContext.GetUrrState(upfUUID, urr.URRID) {
		case context.RULE_INITIAL:
			createURR, err := newCreateURRIE(urr)
			if err != nil {
				return nil, fmt.Errorf("build Session Modification Create URR %d: %w", urr.URRID, err)
			}
			ies = append(ies, createURR)
			smContext.SetUrrState(upfUUID, urr.URRID, context.RULE_CREATE)
		case context.RULE_UPDATE:
			updateURR, err := newUpdateURRIE(urr)
			if err != nil {
				return nil, fmt.Errorf("build Session Modification Update URR %d: %w", urr.URRID, err)
			}
			ies = append(ies, updateURR)
			smContext.SetUrrState(upfUUID, urr.URRID, context.RULE_CREATE)
		case context.RULE_REMOVE:
			ies = append(ies, ie.NewRemoveURR(ie.NewURRID(urr.URRID)))
		case context.RULE_QUERY:
			ies = append(ies, ie.NewQueryURR(ie.NewURRID(urr.URRID)))
			smContext.SetUrrState(upfUUID, urr.URRID, context.RULE_CREATE)
		}
	}

	return goPfcpMessage.NewSessionModificationRequest(
		1, 0, sessionContext.RemoteSEID, 0, 12, ies...,
	), nil
}

func newUpdatePDRIE(pdr *context.PDR) (*ie.IE, error) {
	if pdr.FAR == nil {
		return nil, fmt.Errorf("missing FAR reference")
	}
	pdi, err := newPDIIE(&pdr.PDI)
	if err != nil {
		return nil, err
	}
	children := []*ie.IE{ie.NewPDRID(pdr.PDRID), ie.NewPrecedence(pdr.Precedence), pdi}
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
	return ie.NewUpdatePDR(children...), nil
}

func newUpdateFARIE(far *context.FAR) (*ie.IE, error) {
	children := []*ie.IE{ie.NewFARID(far.FARID), newApplyActionIE(far.ApplyAction)}
	if far.BAR != nil {
		children = append(children, ie.NewBARID(far.BAR.BARID))
	}
	if far.ForwardingParameters != nil {
		parameters, err := newUpdateForwardingParametersIE(far.ForwardingParameters)
		if err != nil {
			return nil, err
		}
		children = append(children, parameters)
	}
	return ie.NewUpdateFAR(children...), nil
}

func newApplyActionIE(action pfcptype.ApplyAction) *ie.IE {
	first := boolMask(action.Drop, 0x01) |
		boolMask(action.Forw, 0x02) |
		boolMask(action.Buff, 0x04) |
		boolMask(action.Nocp, 0x08) |
		boolMask(action.Dupl, 0x10) |
		boolMask(action.Ipma, 0x20) |
		boolMask(action.Ipmd, 0x40) |
		boolMask(action.Dfrt, 0x80)
	second := boolMask(action.Edrt, 0x01) |
		boolMask(action.Bdpn, 0x02) |
		boolMask(action.Ddpn, 0x04) |
		boolMask(action.Fssm, 0x08) |
		boolMask(action.Mbsu, 0x10)
	if second != 0 {
		return ie.NewApplyAction(first, second)
	}
	return ie.NewApplyAction(first)
}

func newUpdateForwardingParametersIE(parameters *context.ForwardingParameters) (*ie.IE, error) {
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
	if parameters.SendEndMarker {
		children = append(children, ie.NewPFCPSMReqFlags(0x02))
	}
	if parameters.ForwardingPolicyID != "" {
		children = append(children, ie.NewForwardingPolicy(parameters.ForwardingPolicyID))
	}
	return ie.NewUpdateForwardingParameters(children...), nil
}

func newUpdateURRIE(urr *context.URR) (*ie.IE, error) {
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
	return ie.NewUpdateURR(children...), nil
}
