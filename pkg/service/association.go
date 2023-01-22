package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/internal/sbi/producer"
)

func toBeAssociatedWithUPF(ctx context.Context, upf *smf_context.UPF) {
	var upfStr string
	if upf.NodeID.NodeIdType == pfcpType.NodeIdTypeFqdn {
		upfStr = fmt.Sprintf("[%s](%s)", upf.NodeID.FQDN, upf.NodeID.ResolveNodeIdToIp().String())
	} else {
		upfStr = fmt.Sprintf("[%s]", upf.NodeID.ResolveNodeIdToIp().String())
	}

	for {
		ensureSetupPfcpAssociation(ctx, upf, upfStr)
		if isDone(ctx) {
			break
		}

		if smf_context.SMF_Self().PfcpHeartbeatInterval == 0 {
			return
		}

		keepHeartbeatTo(ctx, upf, upfStr)
		// return when UPF heartbeat lost is detected or association is canceled
		if isDone(ctx) {
			break
		}

		releaseAllResourcesOfUPF(upf, upfStr)
		if isDone(ctx) {
			break
		}
	}
}

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func ensureSetupPfcpAssociation(ctx context.Context, upf *smf_context.UPF, upfStr string) {
	alertTime := time.Now()
	alertInterval := smf_context.SMF_Self().AssociationSetupFailedAlertInterval
	retryInterval := smf_context.SMF_Self().AssociationSetupFailedRetryInterval
	for {
		timer := time.After(retryInterval)
		err := setupPfcpAssociation(upf, upfStr)
		if err == nil {
			return
		}
		logger.AppLog.Warnf("Failed to setup an association with UPF%s, error:%+v", upfStr, err)
		now := time.Now()
		logger.AppLog.Debugf("now %+v, alertTime %+v", now, alertTime)
		if now.After(alertTime.Add(alertInterval)) {
			logger.AppLog.Errorf("ALERT for UPF%s", upfStr)
			alertTime = now
		}
		logger.AppLog.Debugf("Wait %+v (or less) until next retry attempt", retryInterval)
		select {
		case <-ctx.Done():
			logger.AppLog.Infof("Canceled association request to UPF%s", upfStr)
			return
		case <-timer:
			continue
		}
	}
}

func setupPfcpAssociation(upf *smf_context.UPF, upfStr string) error {
	logger.AppLog.Infof("Sending PFCP Association Request to UPF%s", upfStr)

	resMsg, err := message.SendPfcpAssociationSetupRequest(upf.NodeID)
	if err != nil {
		return err
	}

	rsp := resMsg.PfcpMessage.Body.(pfcp.PFCPAssociationSetupResponse)

	if rsp.Cause == nil || rsp.Cause.CauseValue != pfcpType.CauseRequestAccepted {
		return fmt.Errorf("received PFCP Association Setup Not Accepted Response from UPF%s", upfStr)
	}

	nodeID := rsp.NodeID
	if nodeID == nil {
		return fmt.Errorf("pfcp association needs NodeID")
	}

	logger.AppLog.Infof("Received PFCP Association Setup Accepted Response from UPF%s", upfStr)

	upf.UPFStatus = smf_context.AssociatedSetUpSuccess

	if rsp.UserPlaneIPResourceInformation != nil {
		upf.UPIPInfo = *rsp.UserPlaneIPResourceInformation

		logger.AppLog.Infof("UPF(%s)[%s] setup association",
			upf.NodeID.ResolveNodeIdToIp().String(), upf.UPIPInfo.NetworkInstance)
	}

	return nil
}

func keepHeartbeatTo(ctx context.Context, upf *smf_context.UPF, upfStr string) {
	for {
		err := doPfcpHeartbeat(upf, upfStr)
		if err != nil {
			logger.AppLog.Errorf("PFCP Heartbeat error: %v", err)
			return
		}

		timer := time.After(smf_context.SMF_Self().PfcpHeartbeatInterval)
		select {
		case <-ctx.Done():
			logger.AppLog.Infof("Canceled Heartbeat with UPF%s", upfStr)
			return
		case <-timer:
			continue
		}
	}
}

func doPfcpHeartbeat(upf *smf_context.UPF, upfStr string) error {
	if upf.UPFStatus != smf_context.AssociatedSetUpSuccess {
		return fmt.Errorf("invalid status of UPF%s: %d", upfStr, upf.UPFStatus)
	}

	logger.AppLog.Debugf("Sending PFCP Heartbeat Request to UPF%s", upfStr)

	resMsg, err := message.SendPfcpHeartbeatRequest(upf)
	if err != nil {
		upf.UPFStatus = smf_context.NotAssociated
		upf.RecoveryTimeStamp = time.Time{}
		return fmt.Errorf("SendPfcpHeartbeatRequest error: %w", err)
	}

	rsp := resMsg.PfcpMessage.Body.(pfcp.HeartbeatResponse)
	if rsp.RecoveryTimeStamp == nil {
		logger.AppLog.Warnf("Received PFCP Heartbeat Response without timestamp from UPF%s", upfStr)
		return nil
	}

	logger.AppLog.Debugf("Received PFCP Heartbeat Response from UPF%s", upfStr)
	if upf.RecoveryTimeStamp.IsZero() {
		// first receive
		upf.RecoveryTimeStamp = rsp.RecoveryTimeStamp.RecoveryTimeStamp
	} else if upf.RecoveryTimeStamp.Before(rsp.RecoveryTimeStamp.RecoveryTimeStamp) {
		// received a newer recovery timestamp
		upf.UPFStatus = smf_context.NotAssociated
		upf.RecoveryTimeStamp = time.Time{}
		return fmt.Errorf("received PFCP Heartbeat Response RecoveryTimeStamp has been updated")
	}
	return nil
}

func releaseAllResourcesOfUPF(upf *smf_context.UPF, upfStr string) {
	logger.AppLog.Infof("Release all resources of UPF%s", upfStr)

	upf.ProcEachSMContext(func(smContext *smf_context.SMContext) {
		smContext.SMLock.Lock()
		defer smContext.SMLock.Unlock()
		switch smContext.SMContextState {
		case smf_context.Active, smf_context.ModificationPending, smf_context.PFCPModification:

			needToSendNotify, removeContext := requestAMFToReleasePDUResources(smContext)
			if needToSendNotify {
				sendReleaseNotification(smContext)
			}
			if removeContext {
				// Notification has already been sent, if it is needed
				producer.RemoveSMContextFromAllNF(smContext, false)
			}
		}
	})
}

func requestAMFToReleasePDUResources(smContext *smf_context.SMContext) (sendNotify bool, releaseContext bool) {
	n1n2Request := models.N1N2MessageTransferRequest{}
	// TS 23.502 4.3.4.2 3b. Send Namf_Communication_N1N2MessageTransfer Request, SMF->AMF
	n1n2Request.JsonData = &models.N1N2MessageTransferReqData{
		PduSessionId: smContext.PDUSessionID,
		SkipInd:      true,
	}
	cause := nasMessage.Cause5GSMNetworkFailure
	if buf, err := smf_context.BuildGSMPDUSessionReleaseCommand(smContext, cause, false); err != nil {
		logger.AppLog.Errorf("Build GSM PDUSessionReleaseCommand failed: %+v", err)
	} else {
		n1n2Request.BinaryDataN1Message = buf
		n1n2Request.JsonData.N1MessageContainer = &models.N1MessageContainer{
			N1MessageClass:   "SM",
			N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
		}
	}
	if smContext.UpCnxState != models.UpCnxState_DEACTIVATED {
		if buf, err := smf_context.BuildPDUSessionResourceReleaseCommandTransfer(smContext); err != nil {
			logger.AppLog.Errorf("Build PDUSessionResourceReleaseCommandTransfer failed: %+v", err)
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
					SNssai: smContext.Snssai,
				},
			}
		}
	}

	rspData, res, err := smContext.CommunicationClient.
		N1N2MessageCollectionDocumentApi.
		N1N2MessageTransfer(context.Background(), smContext.Supi, n1n2Request)
	if err != nil {
		logger.AppLog.Warnf("Send N1N2Transfer failed: %+v", err)
	}
	defer func() {
		if resCloseErr := res.Body.Close(); resCloseErr != nil {
			logger.PduSessLog.Errorf("N1N2MessageTransfer response body cannot close: %+v", resCloseErr)
		}
	}()
	switch res.StatusCode {
	case http.StatusOK:
		if rspData.Cause == models.N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
			// the PDU Session Release Command was not transferred to the UE since it is in CM-IDLE state.
			//   ref. step3b of "4.3.4.2 UE or network requested PDU Session Release for Non-Roaming and
			//        Roaming with Local Breakout" in TS23.502
			// it is needed to remove both AMF's and SMF's SM Contexts immediately
			smContext.SMContextState = smf_context.InActive
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
			return true, true
		} else if rspData.Cause == models.N1N2MessageTransferCause_N1_N2_TRANSFER_INITIATED {
			// wait for N2 PDU Session Release Response
			smContext.SMContextState = smf_context.InActivePending
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		} else {
			// other causes are unexpected.
			// keep SM Context to avoid inconsistency with AMF
			smContext.SMContextState = smf_context.InActive
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		}
	case http.StatusNotFound:
		// it is not needed to notify AMF, but needed to remove SM Context in SMF immediately
		smContext.SMContextState = smf_context.InActive
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		return false, true
	default:
		// keep SM Context to avoid inconsistency with AMF
		smContext.SMContextState = smf_context.InActive
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
	}
	return false, false
}

func sendReleaseNotification(smContext *smf_context.SMContext) {
	problemDetails, err := consumer.SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
	if problemDetails != nil || err != nil {
		if problemDetails != nil {
			logger.AppLog.Warnf("Send SMContext Status Notification Problem[%+v]", problemDetails)
		}
		if err != nil {
			logger.AppLog.Warnf("Send SMContext Status Notification Error[%v]", err)
		}
	} else {
		logger.AppLog.Traceln("Send SMContext Status Notification successfully")
	}
}
