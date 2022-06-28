package context

import (
	"encoding/binary"
	"errors"

	"github.com/free5gc/aper"
	"github.com/free5gc/ngap/ngapType"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
)

func HandlePDUSessionResourceSetupResponseTransfer(b []byte, ctx *SMContext) (err error) {
	resourceSetupResponseTransfer := ngapType.PDUSessionResourceSetupResponseTransfer{}

	err = aper.UnmarshalWithParams(b, &resourceSetupResponseTransfer, "valueExt")

	if err != nil {
		return err
	}

	QosFlowPerTNLInformation := resourceSetupResponseTransfer.DLQosFlowPerTNLInformation

	if QosFlowPerTNLInformation.UPTransportLayerInformation.Present !=
		ngapType.UPTransportLayerInformationPresentGTPTunnel {
		return errors.New("resourceSetupResponseTransfer.QosFlowPerTNLInformation.UPTransportLayerInformation.Present")
	}

	GTPTunnel := QosFlowPerTNLInformation.UPTransportLayerInformation.GTPTunnel

	ctx.Tunnel.UpdateANInformation(
		GTPTunnel.TransportLayerAddress.Value.Bytes,
		binary.BigEndian.Uint32(GTPTunnel.GTPTEID.Value))

	ctx.UpCnxState = models.UpCnxState_ACTIVATED
	return nil
}

func HandlePDUSessionResourceSetupUnsuccessfulTransfer(b []byte, ctx *SMContext) (err error) {
	resourceSetupUnsuccessfulTransfer := ngapType.PDUSessionResourceSetupUnsuccessfulTransfer{}

	err = aper.UnmarshalWithParams(b, &resourceSetupUnsuccessfulTransfer, "valueExt")

	if err != nil {
		return err
	}

	switch resourceSetupUnsuccessfulTransfer.Cause.Present {
	case ngapType.CausePresentRadioNetwork:
		logger.PduSessLog.Warnf("PDU Session Resource Setup Unsuccessful by RadioNetwork[%d]",
			resourceSetupUnsuccessfulTransfer.Cause.RadioNetwork.Value)
	case ngapType.CausePresentTransport:
		logger.PduSessLog.Warnf("PDU Session Resource Setup Unsuccessful by Transport[%d]",
			resourceSetupUnsuccessfulTransfer.Cause.Transport.Value)
	case ngapType.CausePresentNas:
		logger.PduSessLog.Warnf("PDU Session Resource Setup Unsuccessful by NAS[%d]",
			resourceSetupUnsuccessfulTransfer.Cause.Nas.Value)
	case ngapType.CausePresentProtocol:
		logger.PduSessLog.Warnf("PDU Session Resource Setup Unsuccessful by Protocol[%d]",
			resourceSetupUnsuccessfulTransfer.Cause.Protocol.Value)
	case ngapType.CausePresentMisc:
		logger.PduSessLog.Warnf("PDU Session Resource Setup Unsuccessful by Protocol[%d]",
			resourceSetupUnsuccessfulTransfer.Cause.Misc.Value)
	case ngapType.CausePresentChoiceExtensions:
		logger.PduSessLog.Warnf("PDU Session Resource Setup Unsuccessful by Protocol[%v]",
			resourceSetupUnsuccessfulTransfer.Cause.ChoiceExtensions)
	}

	ctx.UpCnxState = models.UpCnxState_ACTIVATING

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

	GTPTunnel := pathSwitchRequestTransfer.DLNGUUPTNLInformation.GTPTunnel

	ctx.Tunnel.UpdateANInformation(
		GTPTunnel.TransportLayerAddress.Value.Bytes,
		binary.BigEndian.Uint32(GTPTunnel.GTPTEID.Value))

	ctx.UpSecurityFromPathSwitchRequestSameAsLocalStored = true

	// Verify whether UP security in PathSwitchRequest same as SMF locally stored or not TS 33.501 6.6.1
	if ctx.UpSecurity != nil && pathSwitchRequestTransfer.UserPlaneSecurityInformation != nil {
		rcvSecurityIndication := pathSwitchRequestTransfer.UserPlaneSecurityInformation.SecurityIndication
		rcvUpSecurity := new(models.UpSecurity)
		switch rcvSecurityIndication.IntegrityProtectionIndication.Value {
		case ngapType.IntegrityProtectionIndicationPresentRequired:
			rcvUpSecurity.UpIntegr = models.UpIntegrity_REQUIRED
		case ngapType.IntegrityProtectionIndicationPresentPreferred:
			rcvUpSecurity.UpIntegr = models.UpIntegrity_PREFERRED
		case ngapType.IntegrityProtectionIndicationPresentNotNeeded:
			rcvUpSecurity.UpIntegr = models.UpIntegrity_NOT_NEEDED
		}
		switch rcvSecurityIndication.ConfidentialityProtectionIndication.Value {
		case ngapType.ConfidentialityProtectionIndicationPresentRequired:
			rcvUpSecurity.UpConfid = models.UpConfidentiality_REQUIRED
		case ngapType.ConfidentialityProtectionIndicationPresentPreferred:
			rcvUpSecurity.UpConfid = models.UpConfidentiality_PREFERRED
		case ngapType.ConfidentialityProtectionIndicationPresentNotNeeded:
			rcvUpSecurity.UpConfid = models.UpConfidentiality_NOT_NEEDED
		}

		if rcvUpSecurity.UpIntegr != ctx.UpSecurity.UpIntegr ||
			rcvUpSecurity.UpConfid != ctx.UpSecurity.UpConfid {
			ctx.UpSecurityFromPathSwitchRequestSameAsLocalStored = false

			// SMF shall support logging capabilities for this mismatch event TS 33.501 6.6.1
			logger.PduSessLog.Warnf("Received UP security policy mismatch from SMF locally stored")
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

	DLNGUUPGTPTunnel := handoverRequestAcknowledgeTransfer.DLNGUUPTNLInformation.GTPTunnel

	ctx.Tunnel.UpdateANInformation(
		DLNGUUPGTPTunnel.TransportLayerAddress.Value.Bytes,
		binary.BigEndian.Uint32(DLNGUUPGTPTunnel.GTPTEID.Value))

	return nil
}
