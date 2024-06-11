package association

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
	"github.com/free5gc/smf/internal/sbi/producer"
)

func ToBeAssociatedWithUPF(smfCtx context.Context, upf *smf_context.UPF) {
	// set up association and restore sessions, returns when successful
	// do this once before loop
	ensureSetupPfcpAssociation(smfCtx, upf)

	for {
		if smf_context.GetSelf().PfcpHeartbeatInterval == 0 {
			logger.MainLog.Warnln("PfcpHeartbeatInterval is zero, no heartbeats are expected from UPF")
			return
		}

		// wait a short time after association before starting heartbeat
		time.Sleep(1 * time.Second)

		keepHeartbeatTo(smfCtx, upf)
		// inifinite loop that returns when UPF heartbeat loss is detected or association is canceled
		// UPF now is already in state NotAssociated
		// and Association and SessionManagement Contexts are cancelled

		logger.CtxLog.Warnf("UPF[%s] missed a heartbeat :(", upf.GetNodeIDString())

		// delete resources on AMF and SMF
		ReleaseAllResourcesOfUPF(upf)

		logger.CtxLog.Infof("UPF[%s] is waiting to resume association after releasing all sessions", upf.GetNodeIDString())

		// just sleep some time before trying to re-associate both UPFs
		// (in a real system, e.g. a failure analysis would be performed)
		// this is important to let all pending PFCP messages time out
		time.Sleep(2 * time.Second)

		// re-associate and restore sessions, returns when successful
		logger.CtxLog.Infof("Re-associate UPF[%s]", upf.GetNodeIDString())
		ensureSetupPfcpAssociation(smfCtx, upf)
	}
}

func ensureSetupPfcpAssociation(ctx context.Context, upf *smf_context.UPF) {
	alertTime := time.Now()
	alertInterval := smf_context.GetSelf().AssocFailAlertInterval
	retryInterval := smf_context.GetSelf().AssocFailRetryInterval
	for {
		timer := time.After(retryInterval)
		err := setupPfcpAssociation(upf)
		if err == nil {
			// success
			return
		}
		logger.PfcpLog.Warnf("Failed to setup an association with UPF[%s], error:%+v", upf.GetNodeIDString(), err)
		now := time.Now()
		logger.PfcpLog.Debugf("now %+v, alertTime %+v", now, alertTime)
		if now.After(alertTime.Add(alertInterval)) {
			logger.PfcpLog.Errorf("ALERT for UPF[%s]", upf.GetNodeIDString())
			alertTime = now
		}
		logger.PfcpLog.Debugf("Wait %+v (or less) until next retry attempt", retryInterval)
		select {
		case <-ctx.Done():
			logger.PfcpLog.Infof("Canceled smf context, stop association request to UPF[%s]", upf.GetNodeIDString())
			return
		case <-timer:
			continue
		default:
		}
	}
}

func setupPfcpAssociation(upf *smf_context.UPF) error {
	logger.PfcpLog.Infof("Send an Association Setup Request to UPF[%s]", upf.GetNodeIDString())

	resMsg, err := message.SendPfcpAssociationSetupRequest(upf.PFCPAddr())
	if err != nil {
		return err
	}

	rsp := resMsg.PfcpMessage.Body.(pfcp.PFCPAssociationSetupResponse)

	if rsp.Cause == nil || rsp.Cause.CauseValue != pfcpType.CauseRequestAccepted {
		return fmt.Errorf("received PFCP Association Setup Not Accepted Response from UPF[%s]", upf.GetNodeIDString())
	}

	if rsp.NodeID == nil {
		return fmt.Errorf("pfcp association needs NodeID")
	}

	logger.PfcpLog.Infof("Received PFCP Association Setup Accepted Response from UPF[%s]", upf.GetNodeIDString())

	upf.UPFStatus = smf_context.AssociatedSetUpSuccess

	if rsp.UserPlaneIPResourceInformation != nil {
		upf.UPIPInfo = *rsp.UserPlaneIPResourceInformation
		logger.MainLog.Infof("UPF(%s)[%s] setup association", upf.GetNodeIDString(), upf.UPIPInfo.NetworkInstance.NetworkInstance)
	}

	// new session restoration procedure
	// reset remote SEID (the one of the rebooted UPF node)
	// this tells all subsequent session management processes that modify or delete a session
	// that it was not established at the UPF after its re-association
	for _, pfcpSessionContext := range upf.PFCPSessionContexts {
		pfcpSessionContext.Restoring.Lock()
		pfcpSessionContext.RemoteSEID = 0
		pfcpSessionContext.Restoring.Unlock()
	}

	// let other processes know that UPF is recovering sessions
	upf.RestoresSessions, upf.RestoresSessionsCancelFunc = context.WithCancel(context.Background())

	// create context to signal processes that UPF is ready for session management messages
	// (still needs to recover old sessions though)
	// if the session was not established in the first place (RemoteSEID == 0), then special logic applies
	upf.UPFStatus = smf_context.AssociatedSetUpSuccess
	upf.Association, upf.AssociationCancelFunc = context.WithCancel(context.Background())

	// check if UPF has existing session contexts and restore them
	// these are the PDRs that were applied before the UPF crashed plus the ones created/ changed during its downtime
	// here, we again check if the RemoteSEID == 0 before sending the establishment request
	restored := 0
	for _, pfcpSessionContext := range upf.PFCPSessionContexts {
		pfcpSessionContext.Restoring.Lock()
		// unlock happens in restorePfcpSession

		logger.PfcpLog.Infof("UPF[%s]: restoring session %s", upf.GetNodeIDString(), pfcpSessionContext)

		if pfcpSessionContext.RemoteSEID > 0 {
			logger.PfcpLog.Infof("Some other process already established session rules for UPF[%s]", upf.GetNodeIDString())
			continue
		}
		producer.RestorePDUSessionAtUPF(pfcpSessionContext)
		restored++
	}

	// signal other processes that session recovery is completed
	upf.RestoresSessionsCancelFunc()

	if restored > 0 {
		logger.PfcpLog.Infof("Successfully restored sessions on UPF[%s]", upf.GetNodeIDString())
	}

	return nil
}

func keepHeartbeatTo(ctx context.Context, upf *smf_context.UPF) {
	// use a ticker to send heartbeat at regular interval
	ticker := time.NewTicker(smf_context.GetSelf().PfcpHeartbeatInterval)
	defer ticker.Stop()

	errChan := make(chan error)
	defer close(errChan)

	quit := make(chan bool)

	for {
		select {
		case err := <-errChan:
			// disassociate and cancel session management as soon as heartbeat failed
			upf.UPFStatus = smf_context.NotAssociated
			upf.AssociationCancelFunc()
			upf.RecoveryTimeStamp = time.Time{}

			close(quit)
			logger.MainLog.Errorf("PFCP Heartbeat error: %v", err)
			return
		case <-upf.Association.Done():
			close(quit)
			logger.MainLog.Errorf("UPF[%s] no longer associated, stop heartbeat", upf.GetNodeIDString())
			return
		case <-ctx.Done():
			close(quit)
			logger.MainLog.Errorf("Canceled smf context, stop heartbeat to UPF[%s]", upf.GetNodeIDString())
			return
		case <-ticker.C:
			go doPfcpHeartbeat(upf, errChan, quit)
		}

	}
}

func doPfcpHeartbeat(upf *smf_context.UPF, errChan chan error, quit chan bool) {
	select {
	case <-quit:
		logger.MainLog.Warnf("Previous heartbeat already crashed UPF[%s]", upf.GetNodeIDString())
		return
	default:
	}

	logger.PfcpLog.Tracef("Sending PFCP Heartbeat Request to UPF[%s]", upf.GetNodeIDString())

	resMsg, err := message.SendPfcpHeartbeatRequest(upf)
	if err != nil {
		select {
		case <-quit:
			logger.MainLog.Warnf("Previous heartbeat already crashed UPF[%s]", upf.GetNodeIDString())
			return
		default:
			errChan <- fmt.Errorf("%w", err)
			return
		}
	}

	rsp := resMsg.PfcpMessage.Body.(pfcp.HeartbeatResponse)
	if rsp.RecoveryTimeStamp == nil {
		logger.PfcpLog.Warnf("Received PFCP Heartbeat Response without timestamp from UPF[%s]", upf.GetNodeIDString())
		return
	}

	logger.PfcpLog.Tracef("Received PFCP Heartbeat Response from UPF[%s]", upf.GetNodeIDString())
	if upf.RecoveryTimeStamp.IsZero() {
		// first receive
		upf.RecoveryTimeStamp = rsp.RecoveryTimeStamp.RecoveryTimeStamp
	} else if upf.RecoveryTimeStamp.Before(rsp.RecoveryTimeStamp.RecoveryTimeStamp) {
		select {
		case <-quit:
			logger.MainLog.Warnf("Previous heartbeat already crashed UPF[%s]", upf.GetNodeIDString())
			return
		default:
		}
		errChan <- fmt.Errorf("received PFCP Heartbeat Response RecoveryTimeStamp has been updated")
		return
	}
}

func ReleaseAllResourcesOfUPF(upf *smf_context.UPF) {
	logger.MainLog.Infof("Release all resources of UPF [%s]", upf.GetNodeIDString())

	// first thing to do: remove PFCPSessionContext of affected UPF to avoid session recovery when UPF reboots!
	for _, pfcpSessionContext := range upf.PFCPSessionContexts {
		pfcpSessionContext.Restoring.Lock() // avoid accidental parallel restoration
		localSEID := pfcpSessionContext.LocalSEID
		smf_context.GetSelf().SeidSMContextMap.Delete(localSEID)
		delete(upf.PFCPSessionContexts, localSEID)
		pfcpSessionContext.Restoring.Unlock()
	}

	// find the SMContexts that belong to the UPF and release resources
	allResourcesReleased := true
	for {
		smf_context.GetSelf().ProcEachSMContext(func(smContext *smf_context.SMContext) bool {
			logger.CtxLog.Infof("Release session: check context for PDU Session[ UEIP %s | ID %d ]",
				smContext.PDUAddress.String(), smContext.PduSessionId)
			if smContext.SelectedUPF != nil && smContext.SelectedUPF == upf {
				switch smContext.State() {
				case smf_context.Active, smf_context.ModificationPending, smf_context.PFCPModification:
					logger.CtxLog.Infof("Request AMF to release session resources for PDU Session[ UEIP %s | ID %d ]",
						smContext.PDUAddress.String(), smContext.PduSessionId)
					needToSendNotify, removeContext := requestAMFToReleasePDUResources(smContext)
					if needToSendNotify {
						logger.CtxLog.Infof("Send release notification for PDU Session[ UEIP %s | ID %d ]",
							smContext.PDUAddress.String(), smContext.PduSessionId)
						producer.SendReleaseNotification(smContext.SmStatusNotifyUri)
					}
					if removeContext {
						logger.CtxLog.Infof("Remove context  for for PDU Session[ UEIP %s | ID %d ] from all NFs",
							smContext.PDUAddress.String(), smContext.PduSessionId)
						// Notification has already been sent, if it is needed
						producer.RemoveSMContextFromAllNF(smContext, false)
					}
				default:
					logger.MainLog.Errorf("SMContext for UPF[%s], UE IP [%s] and session ID %d is in state %s, do not release resources yet",
						upf.GetNodeIDString(), smContext.PDUAddress.String(), smContext.PDUSessionID, smContext.State())

					//time.Sleep(2 * time.Second)
					allResourcesReleased = false // continue with loop
				}
			} else {
				logger.CtxLog.Warnf("Found session context without UPF for PDU Session[ UEIP %s | ID %d ]!",
					smContext.PDUAddress.String(), smContext.PduSessionId)
			}
			return true
		})
		if allResourcesReleased {
			break
		}
	}
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

	ctx, _, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NAMF_COMM, models.NfType_AMF)
	if err != nil {
		return false, false
	}

	rspData, res, err := smContext.CommunicationClient.
		N1N2MessageCollectionDocumentApi.
		N1N2MessageTransfer(ctx, smContext.Supi, n1n2Request)
	if err != nil {
		logger.MainLog.Warnf("Send N1N2Transfer failed: %+v", err)
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
