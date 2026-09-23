package context

import (
	"encoding/binary"
	"errors"
	"fmt"

	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func strNgapCause(cause *ngapie.Cause) string {
	ret := ""
	switch c := cause.Choice.(type) {
	case *ngapie.CauseRadioNetwork:
		ret = fmt.Sprintf("Cause by RadioNetwork[%d]", c.Value)
	case *ngapie.CauseTransport:
		ret = fmt.Sprintf("Cause by Transport[%d]", c.Value)
	case *ngapie.CauseNas:
		ret = fmt.Sprintf("Cause by NAS[%d]", c.Value)
	case *ngapie.CauseProtocol:
		ret = fmt.Sprintf("Cause by Protocol[%d]", c.Value)
	case *ngapie.CauseMisc:
		ret = fmt.Sprintf("Cause by Protocol[%d]", c.Value)
	case *ngapie.ProtocolIESingleContainerCauseExtIEs:
		ret = fmt.Sprintf("Cause by Protocol[%v]", c)
	default:
		ret = "Cause [unspecific]"
	}

	return ret
}

// gtpTunnelFromUPTransport returns the GTPTunnel choice of an
// UPTransportLayerInformation, or nil if another choice is present.
func gtpTunnelFromUPTransport(upInfo *ngapie.UPTransportLayerInformation) *ngapie.GTPTunnel {
	if upInfo == nil {
		return nil
	}
	if tunnel, ok := upInfo.Choice.(*ngapie.GTPTunnel); ok {
		return tunnel
	}
	return nil
}

func HandlePDUSessionResourceSetupResponseTransfer(b []byte, ctx *SMContext) error {
	resourceSetupResponseTransfer := ngapie.PDUSessionResourceSetupResponseTransfer{}

	err := ngapie.UnmarshalBinary(b, &resourceSetupResponseTransfer)
	if err != nil {
		return err
	}

	QosFlowPerTNLInformation := resourceSetupResponseTransfer.DLQosFlowPerTNLInformation
	var DCQosFlowPerTNLInformationItem ngapie.QosFlowPerTNLInformationItem
	DCQosFlowPerTNLInformation := resourceSetupResponseTransfer.AdditionalDLQosFlowPerTNLInformation
	if DCQosFlowPerTNLInformation != nil && len(DCQosFlowPerTNLInformation.List) > 0 {
		ctx.NrdcIndicator = true
		DCQosFlowPerTNLInformationItem = DCQosFlowPerTNLInformation.List[0]
	}

	GTPTunnel := gtpTunnelFromUPTransport(QosFlowPerTNLInformation.UPTransportLayerInformation)
	if GTPTunnel == nil {
		return errors.New("resourceSetupResponseTransfer.QosFlowPerTNLInformation.UPTransportLayerInformation.Choice")
	}
	DCGTPTunnel := &ngapie.GTPTunnel{}
	if ctx.NrdcIndicator {
		DCGTPTunnel = gtpTunnelFromUPTransport(
			DCQosFlowPerTNLInformationItem.QosFlowPerTNLInformation.UPTransportLayerInformation)
		if DCGTPTunnel == nil {
			return errors.New(
				"resourceSetupResponseTransfer.AdditionalQosFlowPerTNLInformation." +
					"QosFlowPerTNLInformation.UPTransportLayerInformation.Choice")
		}
	}

	ctx.Tunnel.UpdateANInformation(
		GTPTunnel.TransportLayerAddress.Value.Bytes,
		binary.BigEndian.Uint32(GTPTunnel.GTPTEID.Value))
	if ctx.NrdcIndicator {
		ctx.DCTunnel.UpdateANInformation(
			DCGTPTunnel.TransportLayerAddress.Value.Bytes,
			binary.BigEndian.Uint32(DCGTPTunnel.GTPTEID.Value))
	}

	ctx.UpCnxState = models.Smf_PDUSess_UpCnxState_ACTIVATED
	for _, qos := range ctx.AdditionalQosFlows {
		qos.State = QoSFlowSet
	}
	return nil
}

func HandlePDUSessionResourceModifyResponseTransfer(b []byte, ctx *SMContext) error {
	resourceModifyResponseTransfer := ngapie.PDUSessionResourceModifyResponseTransfer{}

	err := ngapie.UnmarshalBinary(b, &resourceModifyResponseTransfer)
	if err != nil {
		return err
	}

	if DLInfo := resourceModifyResponseTransfer.DLNGUUPTNLInformation; DLInfo != nil {
		if GTPTunnel := gtpTunnelFromUPTransport(DLInfo); GTPTunnel != nil {
			ctx.Tunnel.UpdateANInformation(
				GTPTunnel.TransportLayerAddress.Value.Bytes,
				binary.BigEndian.Uint32(GTPTunnel.GTPTEID.Value))
		}
	}

	if qosInfoList := resourceModifyResponseTransfer.QosFlowAddOrModifyResponseList; qosInfoList != nil {
		for _, item := range qosInfoList.List {
			qfi := uint8(item.QosFlowIdentifier.Value)
			if qosFlow, ok := ctx.AdditionalQosFlows[qfi]; ok {
				qosFlow.State = QoSFlowSet
			} else {
				logger.PduSessLog.Warnf("PDU Session Resource Modify QFI[%d] not found in AdditionalQosFlows", qfi)
			}
		}
	}

	if qosFailedInfoList := resourceModifyResponseTransfer.QosFlowFailedToAddOrModifyList; qosFailedInfoList != nil {
		for _, item := range qosFailedInfoList.List {
			qfi := uint8(item.QosFlowIdentifier.Value)
			logger.PduSessLog.Warnf("PDU Session Resource Modify QFI[%d] %s",
				qfi, strNgapCause(item.Cause))

			if qosFlow, ok := ctx.AdditionalQosFlows[qfi]; ok {
				qosFlow.State = QoSFlowUnset
			} else {
				logger.PduSessLog.Warnf("PDU Session Resource Modify QFI[%d] not found in AdditionalQosFlows", qfi)
			}
		}
	}

	return nil
}

func HandlePDUSessionResourceModifyIndicationTransfer(b []byte, ctx *SMContext) error {
	resourceModifyIndicationTransfer := ngapie.PDUSessionResourceModifyIndicationTransfer{}

	if err := ngapie.UnmarshalBinary(b, &resourceModifyIndicationTransfer); err != nil {
		return err
	}

	var DCQosFlowPerTNLInformationItem ngapie.QosFlowPerTNLInformationItem
	DCQosFlowPerTNLInformation := resourceModifyIndicationTransfer.AdditionalDLQosFlowPerTNLInformation
	if DCQosFlowPerTNLInformation != nil && len(DCQosFlowPerTNLInformation.List) > 0 {
		ctx.NrdcIndicator = true
		DCQosFlowPerTNLInformationItem = DCQosFlowPerTNLInformation.List[0]
	}

	if ctx.NrdcIndicator {
		DCGTPTunnel := gtpTunnelFromUPTransport(
			DCQosFlowPerTNLInformationItem.QosFlowPerTNLInformation.UPTransportLayerInformation)
		if DCGTPTunnel == nil {
			return errors.New(
				"resourceModifyIndicationTransfer.AdditionalQosFlowPerTNLInformation." +
					"QosFlowPerTNLInformation.UPTransportLayerInformation.Choice")
		}
		ctx.DCTunnel.UpdateANInformation(
			DCGTPTunnel.TransportLayerAddress.Value.Bytes,
			binary.BigEndian.Uint32(DCGTPTunnel.GTPTEID.Value))
	}

	return nil
}

func HandlePDUSessionResourceSetupUnsuccessfulTransfer(b []byte, ctx *SMContext) error {
	resourceSetupUnsuccessfulTransfer := ngapie.PDUSessionResourceSetupUnsuccessfulTransfer{}

	err := ngapie.UnmarshalBinary(b, &resourceSetupUnsuccessfulTransfer)
	if err != nil {
		return err
	}

	logger.PduSessLog.Warnf("PDU Session Resource Setup Unsuccessful: %s",
		strNgapCause(resourceSetupUnsuccessfulTransfer.Cause))

	ctx.UpCnxState = models.Smf_PDUSess_UpCnxState_ACTIVATING

	return nil
}

func HandlePathSwitchRequestTransfer(b []byte, ctx *SMContext) error {
	pathSwitchRequestTransfer := ngapie.PathSwitchRequestTransfer{}

	if err := ngapie.UnmarshalBinary(b, &pathSwitchRequestTransfer); err != nil {
		return err
	}

	GTPTunnel := gtpTunnelFromUPTransport(pathSwitchRequestTransfer.DLNGUUPTNLInformation)
	if GTPTunnel == nil {
		return errors.New("pathSwitchRequestTransfer.DLNGUUPTNLInformation.Choice")
	}

	ctx.Tunnel.UpdateANInformation(
		GTPTunnel.TransportLayerAddress.Value.Bytes,
		binary.BigEndian.Uint32(GTPTunnel.GTPTEID.Value))

	ctx.UpSecurityFromPathSwitchRequestSameAsLocalStored = true

	// Verify whether UP security in PathSwitchRequest same as SMF locally stored or not TS 33.501 6.6.1
	if ctx.UpSecurity != nil && pathSwitchRequestTransfer.UserPlaneSecurityInformation != nil {
		rcvSecurityIndication := pathSwitchRequestTransfer.UserPlaneSecurityInformation.SecurityIndication
		rcvUpSecurity := new(models.UpSecurity)
		switch rcvSecurityIndication.IntegrityProtectionIndication.Value {
		case ngapie.IntegrityProtectionIndicationPresentRequired:
			rcvUpSecurity.UpIntegr = models.UpIntegrity_REQUIRED
		case ngapie.IntegrityProtectionIndicationPresentPreferred:
			rcvUpSecurity.UpIntegr = models.UpIntegrity_PREFERRED
		case ngapie.IntegrityProtectionIndicationPresentNotNeeded:
			rcvUpSecurity.UpIntegr = models.UpIntegrity_NOT_NEEDED
		}
		switch rcvSecurityIndication.ConfidentialityProtectionIndication.Value {
		case ngapie.ConfidentialityProtectionIndicationPresentRequired:
			rcvUpSecurity.UpConfid = models.UpConfidentiality_REQUIRED
		case ngapie.ConfidentialityProtectionIndicationPresentPreferred:
			rcvUpSecurity.UpConfid = models.UpConfidentiality_PREFERRED
		case ngapie.ConfidentialityProtectionIndicationPresentNotNeeded:
			rcvUpSecurity.UpConfid = models.UpConfidentiality_NOT_NEEDED
		}

		if rcvUpSecurity.UpIntegr != ctx.UpSecurity.UpIntegr ||
			rcvUpSecurity.UpConfid != ctx.UpSecurity.UpConfid {
			ctx.UpSecurityFromPathSwitchRequestSameAsLocalStored = false

			// SMF shall support logging capabilities for this mismatch event TS 33.501 6.6.1
			logger.PduSessLog.Warnf("Received UP security policy mismatch from SMF locally stored")
		}
	}

	// If NRDC is activated
	// update the DC tunnel AN information from Additional DL QoS Flow per TNL Information at IE Extensions
	if ctx.NrdcIndicator {
		ieExtensions := pathSwitchRequestTransfer.IEExtensions
		if ieExtensions == nil {
			logger.PduSessLog.Warnf("PathSwitchRequestTransfer IEExtensions is nil when NRDC is activated")
		} else {
			for _, extIE := range ieExtensions.List {
				if extIE.Id.Value == ngapie.ProtocolIEIDAdditionalDLQosFlowPerTNLInformation {
					qosFlowInfo := extIE.AdditionalDLQosFlowPerTNLInformation.List[0]
					DCGTPTunnel := gtpTunnelFromUPTransport(
						qosFlowInfo.QosFlowPerTNLInformation.UPTransportLayerInformation)
					if DCGTPTunnel == nil {
						logger.PduSessLog.Warnf("AdditionalDLQosFlowPerTNLInformation without GTPTunnel choice")
						break
					}
					ctx.DCTunnel.UpdateANInformation(
						DCGTPTunnel.TransportLayerAddress.Value.Bytes,
						binary.BigEndian.Uint32(DCGTPTunnel.GTPTEID.Value))
					break
				}
			}
		}
	}

	return nil
}

func HandlePathSwitchRequestSetupFailedTransfer(b []byte, ctx *SMContext) error {
	pathSwitchRequestSetupFailedTransfer := ngapie.PathSwitchRequestSetupFailedTransfer{}

	err := ngapie.UnmarshalBinary(b, &pathSwitchRequestSetupFailedTransfer)
	if err != nil {
		return err
	}

	// TODO: finish handler
	return nil
}

func HandleHandoverRequiredTransfer(b []byte, ctx *SMContext) error {
	handoverRequiredTransfer := ngapie.HandoverRequiredTransfer{}

	err := ngapie.UnmarshalBinary(b, &handoverRequiredTransfer)

	directForwardingPath := handoverRequiredTransfer.DirectForwardingPathAvailability
	if directForwardingPath != nil {
		logger.PduSessLog.Infoln("Direct Forwarding Path Available")
		ctx.DLForwardingType = DirectForwarding
	} else {
		logger.PduSessLog.Infoln("Direct Forwarding Path Unavailable")
		ctx.DLForwardingType = IndirectForwarding
	}

	if err != nil {
		return err
	}
	return nil
}

func HandleHandoverRequestAcknowledgeTransfer(b []byte, ctx *SMContext) error {
	handoverRequestAcknowledgeTransfer := ngapie.HandoverRequestAcknowledgeTransfer{}

	err := ngapie.UnmarshalBinary(b, &handoverRequestAcknowledgeTransfer)
	if err != nil {
		return err
	}

	DLNGUUPGTPTunnel := gtpTunnelFromUPTransport(handoverRequestAcknowledgeTransfer.DLNGUUPTNLInformation)
	if DLNGUUPGTPTunnel == nil {
		return errors.New("handoverRequestAcknowledgeTransfer.DLNGUUPTNLInformation.Choice")
	}

	ctx.Tunnel.UpdateANInformation(
		DLNGUUPGTPTunnel.TransportLayerAddress.Value.Bytes,
		binary.BigEndian.Uint32(DLNGUUPGTPTunnel.GTPTEID.Value))

	DLForwardingInfo := handoverRequestAcknowledgeTransfer.DLForwardingUPTNLInformation

	if DLForwardingInfo == nil {
		ctx.DLForwardingType = NoForwarding
		logger.PduSessLog.Warnf("Handle HandoverRequestAcknowledgeTransfer warned: %+v", "DL Forwarding Info not provision")
		return nil
	}

	switch ctx.DLForwardingType {
	case IndirectForwarding:
		DLForwardingGTPTunnel := gtpTunnelFromUPTransport(DLForwardingInfo)
		if DLForwardingGTPTunnel == nil {
			return errors.New("handoverRequestAcknowledgeTransfer.DLForwardingUPTNLInformation.Choice")
		}
		if err = configureIndirectForwardingTunnel(ctx, DLForwardingGTPTunnel); err != nil {
			return err
		}
	case DirectForwarding:
		ctx.DLDirectForwardingTunnel = DLForwardingInfo
	}

	return nil
}

// configureIndirectForwardingTunnel reserves the AN UPF for the complete local
// rule/tunnel configuration. Association Release may change the state to
// Releasing after the reservation is granted, but it must wait for finishWork
// before cleaning sessions. The unexported addPDR is safe here because the
// reservation already established this operation's right to finish.
func configureIndirectForwardingTunnel(ctx *SMContext, forwardingGTP *ngapie.GTPTunnel) error {
	if ctx.Tunnel == nil {
		return errors.New("handover has no user-plane tunnel")
	}
	defaultPath := ctx.Tunnel.DataPathPool.GetDefaultPath()
	if defaultPath == nil || defaultPath.FirstDPNode == nil || defaultPath.FirstDPNode.UPF == nil {
		return errors.New("handover has no default-path AN UPF")
	}
	originTunnel := defaultPath.FirstDPNode.UpLinkTunnel
	if originTunnel == nil || originTunnel.PDR == nil || originTunnel.PDR.PDI.LocalFTeid == nil {
		return errors.New("handover default path has no local F-TEID")
	}
	if forwardingGTP == nil || forwardingGTP.GTPTEID == nil || forwardingGTP.TransportLayerAddress == nil {
		return errors.New("handover forwarding tunnel is incomplete")
	}

	ANUPF := defaultPath.FirstDPNode.UPF
	_, finishWork, err := ANUPF.BeginSessionWork()
	if err != nil {
		return err
	}
	defer finishWork()

	committed := false
	teid, err := GenerateTEID()
	if err != nil {
		return err
	}
	defer func() {
		if !committed {
			ReleaseTEID(teid)
		}
	}()

	indirectForwardingPDR, err := ANUPF.addPDR()
	if err != nil {
		return err
	}
	defer func() {
		if !committed {
			ANUPF.discardPDRAndFAR(indirectForwardingPDR)
		}
	}()

	originFTEID := originTunnel.PDR.PDI.LocalFTeid
	indirectForwardingPDR.PDI.LocalFTeid = &pfcptype.FTEID{
		V4:          originFTEID.V4,
		Teid:        teid,
		Ipv4Address: originFTEID.Ipv4Address,
	}
	indirectForwardingPDR.OuterHeaderRemoval = &pfcptype.OuterHeaderRemoval{
		OuterHeaderRemovalDescription: pfcptype.OuterHeaderRemovalGtpUUdpIpv4,
	}
	indirectForwardingPDR.FAR.ApplyAction = pfcptype.ApplyAction{Forw: true}
	indirectForwardingPDR.FAR.ForwardingParameters = &ForwardingParameters{
		DestinationInterface: pfcptype.DestinationInterface{
			InterfaceValue: pfcptype.DestinationInterfaceAccess,
		},
		OuterHeaderCreation: &pfcptype.OuterHeaderCreation{
			OuterHeaderCreationDescription: pfcptype.OuterHeaderCreationGtpUUdpIpv4,
			Teid:                           binary.BigEndian.Uint32(forwardingGTP.GTPTEID.Value),
			Ipv4Address:                    forwardingGTP.TransportLayerAddress.Value.Bytes,
		},
	}

	indirectPath := NewDataPath()
	indirectPath.FirstDPNode = NewDataPathNode()
	indirectPath.FirstDPNode.UPF = ANUPF
	indirectPath.FirstDPNode.UpLinkTunnel.TEID = teid
	indirectPath.FirstDPNode.UpLinkTunnel.PDR = indirectForwardingPDR
	ctx.IndirectForwardingTunnel = indirectPath
	committed = true
	return nil
}

// RemoveIndirectForwardingTunnel releases local resources created for an
// indirect handover. It is valid during Releasing/Down because rollback must
// not depend on the failed association remaining available.
func (ctx *SMContext) RemoveIndirectForwardingTunnel() {
	path := ctx.IndirectForwardingTunnel
	if path == nil || path.FirstDPNode == nil {
		ctx.IndirectForwardingTunnel = nil
		return
	}
	node := path.FirstDPNode
	if node.UPF != nil && node.UpLinkTunnel != nil {
		node.UPF.discardPDRAndFAR(node.UpLinkTunnel.PDR)
		if node.UpLinkTunnel.TEID != 0 {
			ReleaseTEID(node.UpLinkTunnel.TEID)
		}
	}
	ctx.IndirectForwardingTunnel = nil
}
