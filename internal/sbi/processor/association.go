package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/message"
)

func (p *Processor) ToBeAssociatedWithUPF(smfCtx context.Context, upf *smf_context.UPF) {
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
		// UPF now is already in state NotAssociated and Association context is cancelled

		logger.CtxLog.Warnf("UPF[%s] missed a heartbeat :(", upf.GetNodeIDString())

		// delete resources on AMF and SMF
		p.ReleaseAllResourcesOfUPF(upf)

		logger.CtxLog.Infof("UPF[%s] is waiting to resume association after releasing all sessions", upf.GetNodeIDString())

		// just sleep some time before trying to re-associate UPF
		// (in a real system, e.g. a failure analysis would be performed)
		// this is important to let all pending PFCP messages time out
		time.Sleep(2 * time.Second)

		// re-associate and restore sessions, blocking operation that returns when successful
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

		// retry association
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
	logger.PfcpLog.Infof("Send PFCP Association Setup Request to UPF[%s]", upf.GetNodeIDString())

	resMsg, err := message.SendPfcpAssociationSetupRequest(upf.PFCPAddr())
	if err != nil {
		return err
	}

	rsp := resMsg.PfcpMessage.Body.(pfcp.PFCPAssociationSetupResponse)

	if rsp.Cause == nil || rsp.Cause.CauseValue != pfcpType.CauseRequestAccepted {
		return fmt.Errorf("received PFCP Association Setup Not Accepted Response from UPF[%s]", upf.GetNodeIDString())
	}

	if rsp.NodeID == nil {
		return fmt.Errorf("PFCP Association needs NodeID")
	}

	logger.PfcpLog.Infof("Received PFCP Association Setup Accepted Response from UPF[%s]", upf.GetNodeIDString())

	// success
	// create context to signal processes that UPF is ready for session management messages
	// (still needs to recover old sessions though)
	// if the session was not established in the first place (RemoteSEID == 0), then special logic applies
	upf.Association, upf.AssociationCancelFunc = context.WithCancel(context.Background())
	upf.UPFStatus = smf_context.AssociatedSetUpSuccess

	if rsp.UserPlaneIPResourceInformation != nil {
		upf.UPIPInfo = *rsp.UserPlaneIPResourceInformation
		logger.MainLog.Infof("UPF(%s)[%s] setup association", upf.GetNodeIDString(), upf.UPIPInfo.NetworkInstance.NetworkInstance)
	}

	// start session restoration procedure
	// let other processes know that UPF is recovering sessions
	upf.RestoresSessions, upf.RestoresSessionsCancelFunc = context.WithCancel(context.Background())

	// reset remote SEID (the one of the rebooted UPF node)
	// this tells all subsequent session management processes that modify or delete a session
	// that it was not established at the UPF after its re-association
	for _, pfcpSessionContext := range upf.PFCPSessionContexts {
		pfcpSessionContext.Restoring.Lock()
		pfcpSessionContext.RemoteSEID = 0
		pfcpSessionContext.Restoring.Unlock()
	}

	// check if UPF has existing session contexts and restore them
	// these are the PDRs that were applied before the UPF crashed plus the ones created/ changed during its downtime
	// here, we again check if the RemoteSEID == 0 before sending the establishment request
	restored := 0
	for _, pfcpSessionContext := range upf.PFCPSessionContexts {
		pfcpSessionContext.Restoring.Lock()
		// u

		logger.PfcpLog.Infof("UPF[%s]: restoring session %s", upf.GetNodeIDString(), pfcpSessionContext)

		if pfcpSessionContext.RemoteSEID > 0 {
			logger.PfcpLog.Infof("Some other process already established session rules for UPF[%s]", upf.GetNodeIDString())
			continue
		}
		RestorePDUSessionAtUPF(pfcpSessionContext)
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
			logger.MainLog.Errorln("SMF context was cancelled, stop heartbeat")
			return
		case <-ticker.C:
			go doPfcpHeartbeat(upf, errChan, quit)
		}
	}
}

func doPfcpHeartbeat(upf *smf_context.UPF, errChan chan error, quit chan bool) {
	logger.PfcpLog.Tracef("Sending PFCP Heartbeat Request to UPF[%s]", upf.GetNodeIDString())

	select {
	case <-quit:
		logger.MainLog.Warnf("Previous missed heartbeat already disassociated UPF[%s]", upf.GetNodeIDString())
		return
	default:
	}

	logger.PfcpLog.Tracef("Sending PFCP Heartbeat Request to UPF[%s]", upf.GetNodeIDString())

	resMsg, err := message.SendPfcpHeartbeatRequest(upf)
	if err != nil {
		select {
		case <-quit:
			logger.MainLog.Warnf("Previous heartbeat already disassociated UPF[%s]", upf.GetNodeIDString())
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
		// received a newer recovery timestamp
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

func (p *Processor) ReleaseAllResourcesOfUPF(upf *smf_context.UPF) {
	logger.MainLog.Infof("Release all resources of UPF [%s]", upf.GetNodeIDString())

	removalPending := make(map[uint64]*smf_context.SMContext)

	// first thing to do: remove PFCPSessionContext of affected UPF to avoid session recovery when UPF reboots!
	for _, pfcpSessionContext := range upf.PFCPSessionContexts {
		pfcpSessionContext.Restoring.Lock() // avoid accidental parallel restoration of this context
		localSEID := pfcpSessionContext.LocalSEID
		delete(upf.PFCPSessionContexts, localSEID)
		pfcpSessionContext.Restoring.Unlock()

		smContext := smf_context.GetSelf().GetSMContextBySEID(localSEID)
		removalPending[localSEID] = smContext
	}

	// release SM context from SMF context and notify other NFs
	for len(removalPending) > 0 {
		for localSEID, smContext := range removalPending {
			logger.CtxLog.Infof("Release session: check context for PDU Session[ UEIP %s | ID %d ]",
				smContext.PDUAddress.String(), smContext.PduSessionId)
			switch smContext.State() {
			case smf_context.Active, smf_context.ModificationPending, smf_context.PFCPModification:
				p.NotifyNFsAndReleaseSMContext(smContext)

				// modify map and restart loop
				delete(removalPending, localSEID)
				break // break to restart iteration as map has been modified
			default:
				logger.MainLog.Errorf("SMContext for UPF[%s], UE IP [%s] and session ID %d is in state %s, do not release resources yet",
					upf.GetNodeIDString(), smContext.PDUAddress.String(), smContext.PDUSessionID, smContext.State())
			}
		}
	}
}
