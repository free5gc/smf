package context

import (
	"errors"

	"bytes"
	"fmt"

	"encoding/binary"
	"free5gc/lib/aper"
	"free5gc/lib/ngap/ngapType"
	"free5gc/lib/pfcp/pfcpType"
)

func HandlePDUSessionResourceSetupResponseTransfer(b []byte, ctx *SMContext) (err error) {
	resourceSetupResponseTransfer := ngapType.PDUSessionResourceSetupResponseTransfer{}

	err = aper.UnmarshalWithParams(b, &resourceSetupResponseTransfer, "valueExt")

	if err != nil {
		return err
	}

	QosFlowPerTNLInformation := resourceSetupResponseTransfer.QosFlowPerTNLInformation

	if QosFlowPerTNLInformation.UPTransportLayerInformation.Present !=
		ngapType.UPTransportLayerInformationPresentGTPTunnel {
		return errors.New("resourceSetupResponseTransfer.QosFlowPerTNLInformation.UPTransportLayerInformation.Present")
	}

	gtpTunnel := QosFlowPerTNLInformation.UPTransportLayerInformation.GTPTunnel

	teid := binary.BigEndian.Uint32(gtpTunnel.GTPTEID.Value)

	ctx.Tunnel.ANInformation.IPAddress = gtpTunnel.TransportLayerAddress.Value.Bytes
	ctx.Tunnel.ANInformation.TEID = teid

	for _, dataPath := range ctx.Tunnel.DataPathPool {

		if dataPath.Activated {
			ANUPF := dataPath.FirstDPNode
			DLPDR := ANUPF.DownLinkTunnel.PDR

			DLPDR.FAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
			dlOuterHeaderCreation := DLPDR.FAR.ForwardingParameters.OuterHeaderCreation
			dlOuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
			dlOuterHeaderCreation.Teid = teid
			dlOuterHeaderCreation.Ipv4Address = ctx.Tunnel.ANInformation.IPAddress.To4()
		}

	}

	return nil
}

func HandlePathSwitchRequestTransfer(b []byte, ctx *SMContext) error {
	pathSwitchRequestTransfer := ngapType.PathSwitchRequestTransfer{}

	if err := aper.UnmarshalWithParams(b, &pathSwitchRequestTransfer, "valueExt"); err != nil {
		return err
	}

	if pathSwitchRequestTransfer.DLNGUUPTNLInformation.Present != ngapType.UPTransportLayerInformationPresentGTPTunnel {
		return errors.New("pathSwitchRequestTransfer.DLNGUUPTNLInformation.Present")
	}

	gtpTunnel := pathSwitchRequestTransfer.DLNGUUPTNLInformation.GTPTunnel

	TEIDReader := bytes.NewBuffer(gtpTunnel.GTPTEID.Value)

	teid, err := binary.ReadUvarint(TEIDReader)
	if err != nil {
		return fmt.Errorf("Parse TEID error %s", err.Error())
	}

	for _, dataPath := range ctx.Tunnel.DataPathPool {

		if dataPath.Activated {
			ANUPF := dataPath.FirstDPNode
			DLPDR := ANUPF.DownLinkTunnel.PDR

			DLPDR.FAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
			dlOuterHeaderCreation := DLPDR.FAR.ForwardingParameters.OuterHeaderCreation
			dlOuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
			dlOuterHeaderCreation.Teid = uint32(teid)
			dlOuterHeaderCreation.Ipv4Address = gtpTunnel.TransportLayerAddress.Value.Bytes
			DLPDR.FAR.State = RULE_UPDATE
		}

	}

	return nil
}

func HandlePathSwitchRequestSetupFailedTransfer(b []byte, ctx *SMContext) (err error) {
	pathSwitchRequestSetupFailedTransfer := ngapType.PathSwitchRequestSetupFailedTransfer{}

	err = aper.UnmarshalWithParams(b, &pathSwitchRequestSetupFailedTransfer, "valueExt")

	if err != nil {
		return err
	}

	// TODO: finish handler
	return nil
}

func HandleHandoverRequiredTransfer(b []byte, ctx *SMContext) (err error) {
	handoverRequiredTransfer := ngapType.HandoverRequiredTransfer{}

	err = aper.UnmarshalWithParams(b, &handoverRequiredTransfer, "valueExt")

	if err != nil {
		return err
	}

	// TODO: Handle Handover Required Transfer
	return nil
}

func HandleHandoverRequestAcknowledgeTransfer(b []byte, ctx *SMContext) (err error) {
	handoverRequestAcknowledgeTransfer := ngapType.HandoverRequestAcknowledgeTransfer{}

	err = aper.UnmarshalWithParams(b, &handoverRequestAcknowledgeTransfer, "valueExt")

	if err != nil {
		return err
	}
	DLNGUUPTNLInformation := handoverRequestAcknowledgeTransfer.DLNGUUPTNLInformation
	GTPTunnel := DLNGUUPTNLInformation.GTPTunnel
	TEIDReader := bytes.NewBuffer(GTPTunnel.GTPTEID.Value)

	teid, err := binary.ReadUvarint(TEIDReader)
	if err != nil {
		return fmt.Errorf("Parse TEID error %s", err.Error())
	}

	for _, dataPath := range ctx.Tunnel.DataPathPool {

		if dataPath.Activated {
			ANUPF := dataPath.FirstDPNode
			DLPDR := ANUPF.DownLinkTunnel.PDR

			DLPDR.FAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
			dlOuterHeaderCreation := DLPDR.FAR.ForwardingParameters.OuterHeaderCreation
			dlOuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
			dlOuterHeaderCreation.Teid = uint32(teid)
			dlOuterHeaderCreation.Ipv4Address = GTPTunnel.TransportLayerAddress.Value.Bytes
			DLPDR.FAR.State = RULE_UPDATE
		}
	}

	return nil
}
