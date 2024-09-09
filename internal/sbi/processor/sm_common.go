package processor

import (
	"net/http"

	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

func (p *Processor) NotifyNFsAndReleaseSMContext(smContext *smf_context.SMContext) {
	logger.CtxLog.Infof("Request AMF to release session resources for PDU Session[ UEIP %s | ID %d ]",
		smContext.PDUAddress.String(), smContext.PduSessionId)
	needToSendNotify, removeContext := p.requestAMFToReleasePDUResources(smContext)
	if needToSendNotify {
		logger.CtxLog.Infof("Send release notification for PDU Session[ UEIP %s | ID %d ]",
			smContext.PDUAddress.String(), smContext.PduSessionId)
		p.sendReleaseNotification(smContext)
	}
	if removeContext {
		logger.CtxLog.Infof("Remove SM context for for PDU Session[ UEIP %s | ID %d ] from all NFs",
			smContext.PDUAddress.String(), smContext.PduSessionId)
		// Notification has already been sent, if it is needed
		p.removeSMContextFromAllNF(smContext, false)
	}
}

func (p *Processor) requestAMFToReleasePDUResources(
	smContext *smf_context.SMContext,
) (sendNotify bool, releaseContext bool) {
	n1n2Request := models.N1N2MessageTransferRequest{}
	// TS 23.502 4.3.4.2 3b. Send Namf_Communication_N1N2MessageTransfer Request, SMF->AMF
	n1n2Request.JsonData = &models.N1N2MessageTransferReqData{
		PduSessionId: smContext.PDUSessionID,
		SkipInd:      true,
	}
	cause := nasMessage.Cause5GSMNetworkFailure
	if buf, err := smf_context.BuildGSMPDUSessionReleaseCommand(smContext, cause, false); err != nil {
		logger.MainLog.Errorf("Build GSM PDUSessionReleaseCommand failed: %+v", err)
	} else {
		n1n2Request.BinaryDataN1Message = buf
		n1n2Request.JsonData.N1MessageContainer = &models.N1MessageContainer{
			N1MessageClass:   "SM",
			N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
		}
	}
	if smContext.UpCnxState != models.UpCnxState_DEACTIVATED {
		if buf, err := smf_context.BuildPDUSessionResourceReleaseCommandTransfer(smContext); err != nil {
			logger.MainLog.Errorf("Build PDUSessionResourceReleaseCommandTransfer failed: %+v", err)
		} else {
			n1n2Request.BinaryDataN2Information = buf
			n1n2Request.JsonData.N2InfoContainer = &models.N2InfoContainer{
				N2InformationClass: models.N2InformationClass_SM,
				SmInfo: &models.N2SmInformation{
					PduSessionId: smContext.PDUSessionID,
					N2InfoContent: &models.N2InfoContent{
						NgapIeType: models.NgapIeType_PDU_RES_REL_CMD,
						NgapData: &models.RefToBinaryData{
							ContentId: "N2SmInformation",
						},
					},
					SNssai: smContext.SNssai,
				},
			}
		}
	}

	ctx, _, errToken := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NAMF_COMM, models.NfType_AMF)
	if errToken != nil {
		return false, false
	}

	rspData, statusCode, err := p.Consumer().
		N1N2MessageTransfer(ctx, smContext.Supi, n1n2Request, smContext.CommunicationClientApiPrefix)
	if err != nil {
		logger.ConsumerLog.Warnf("N1N2MessageTransfer for RequestAMFToReleasePDUResources failed: %+v", err)
	}

	switch *statusCode {
	case http.StatusOK:
		if rspData.Cause == models.N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
			// the PDU Session Release Command was not transferred to the UE since it is in CM-IDLE state.
			//   ref. step3b of "4.3.4.2 UE or network requested PDU Session Release for Non-Roaming and
			//        Roaming with Local Breakout" in TS23.502
			// it is needed to remove both AMF's and SMF's SM Contexts immediately
			smContext.SetState(smf_context.InActive)
			return true, true
		} else if rspData.Cause == models.N1N2MessageTransferCause_N1_N2_TRANSFER_INITIATED {
			// wait for N2 PDU Session Release Response
			smContext.SetState(smf_context.InActivePending)
		} else {
			// other causes are unexpected.
			// keep SM Context to avoid inconsistency with AMF
			smContext.SetState(smf_context.InActive)
		}
	case http.StatusNotFound:
		// it is not needed to notify AMF, but needed to remove SM Context in SMF immediately
		smContext.SetState(smf_context.InActive)
		return false, true
	default:
		// keep SM Context to avoid inconsistency with AMF
		smContext.SetState(smf_context.InActive)
	}
	return false, false
}

func (p *Processor) removeSMContextFromAllNF(smContext *smf_context.SMContext, sendNotification bool) {
	smContext.SetState(smf_context.InActive)
	// remove SM Policy Association
	if smContext.SMPolicyID != "" {
		if err := p.Consumer().SendSMPolicyAssociationTermination(smContext); err != nil {
			smContext.Log.Errorf("SM Policy Termination failed: %s", err)
		} else {
			smContext.SMPolicyID = ""
		}
	}

	// Because the amfUE who called this SMF API is being locked until the API Handler returns,
	// sending SMContext Status Notification should run asynchronously
	// so that this function returns immediately.
	if sendNotification {
		go p.sendSMContextStatusNotificationAndRemoveSMContext(smContext, sendNotification)
	}
}

func (p *Processor) sendSMContextStatusNotificationAndRemoveSMContext(
	smContext *smf_context.SMContext,
	sendNotification bool,
) {
	if sendNotification && len(smContext.SmStatusNotifyUri) != 0 {
		p.sendReleaseNotification(smContext)
	}

	smf_context.GetSelf().RemoveSMContext(smContext)
}

func (p *Processor) sendReleaseNotification(smContext *smf_context.SMContext) {
	// Use go routine to send Notification to prevent blocking the handling process
	problemDetails, err := p.Consumer().SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
	if problemDetails != nil || err != nil {
		if problemDetails != nil {
			smContext.Log.Warnf("Send SMContext Status Notification Problem[%+v]", problemDetails)
		}

		if err != nil {
			smContext.Log.Warnf("Send SMContext Status Notification Error[%v]", err)
		}
	} else {
		smContext.Log.Traceln("Send SMContext Status Notification successful")
	}
}
