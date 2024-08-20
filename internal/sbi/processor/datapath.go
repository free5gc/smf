package processor

import (
	"fmt"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
	"github.com/google/uuid"
)

// It returns after all PFCP response have been returned or timed out,
// and before sending the N1N2MessageTransfer request if it is needed.
func (p *Processor) ActivatePDUSessionAtUPFs(
	smContext *smf_context.SMContext,
) smf_context.PFCPSessionResponseStatus {
	// smContext has a map to all PFCPSessionContexts that need to be established,
	// one for each involved UPF
	smContext.Log.Traceln("In ActivatePDUSessionAtUPFs")

	resChan := make(chan smf_context.SendPfcpResult)
	defer close(resChan)

	waitForReply := 0

	// loop through all PFCP session contexts associated with this SM context
	for _, pfcpSessionContext := range smContext.PFCPSessionContexts {
		upf := pfcpSessionContext.UPF
		nodeID := upf.GetNodeIDString()
		select {
		case <-upf.Association.Done():
			logger.PduSessLog.Warnf("UPF[%s] not associated, skip session establishment for %s",
				nodeID, pfcpSessionContext.PDUSessionParams())
			continue
		default:
			logger.PduSessLog.Infof("Init session establishment on UPF[%s] for %s",
				nodeID, pfcpSessionContext.PDUSessionParams())
			waitForReply += 1
			if pfcpSessionContext.RemoteSEID == 0 {
				go establishPfcpSession(pfcpSessionContext, resChan)
			} else {
				go modifyExistingPfcpSession(smContext, pfcpSessionContext, resChan, "")
			}
		}
	}

	if waitForReply == 0 {
		logger.PduSessLog.Warnln("No UPFs are associated to establish the session")
		return smf_context.SessionEstablishFailed
	}

	return waitAllPfcpRsp(waitForReply, smf_context.SessionEstablishSuccess, smf_context.SessionEstablishFailed, resChan)
}

func (p *Processor) UpdatePDUSessionAtANUPF(
	smContext *smf_context.SMContext,
) smf_context.PFCPSessionResponseStatus {

	defaultPath := smContext.Tunnel.DataPathPool.GetDefaultPath()
	anUPF := defaultPath.FirstDPNode.UPF
	nodeID := anUPF.GetNodeIDString()
	pfcpSessionContext := smContext.PFCPSessionContexts[anUPF.ID]

	resChan := make(chan smf_context.SendPfcpResult)
	defer close(resChan)

	select {
	case <-anUPF.Association.Done():
		logger.PduSessLog.Warnf("UPF[%s] not associated, skip session modification for %s",
			nodeID, pfcpSessionContext.PDUSessionParams())
		return smf_context.SessionUpdateFailed
	default:
		logger.PduSessLog.Infof("Init session modification on AN UPF[%s] for %s",
			nodeID, pfcpSessionContext.PDUSessionParams())
		select {
		case <-anUPF.RestoresSessions.Done():
			go modifyExistingPfcpSession(smContext, pfcpSessionContext, resChan, "")
		default:
			logger.PduSessLog.Warnf("UPF[%s] is currently restoring sessions", nodeID)
			pfcpSessionContext.Restoring.Lock()
			// unlock when restore is finished

			if pfcpSessionContext.RemoteSEID == 0 {
				// session has not been restored yet, establish pfcp session context as new PDRs
				logger.PduSessLog.Warnf("Restore instead of modify session on UPF[%s]", nodeID)
				go restorePfcpSession(pfcpSessionContext, resChan)
			} else {
				pfcpSessionContext.Restoring.Unlock()
				go modifyExistingPfcpSession(smContext, pfcpSessionContext, resChan, "")
			}
		}
	}

	//TODO: can we integrate the functionality in this code partly in session establishment?
	if smf_context.GetSelf().ULCLSupport && smContext.BPManager != nil {
		if smContext.BPManager.BPStatus == smf_context.UnInitialized {
			logger.PfcpLog.Infoln("Add PSAAndULCL")
			// TODO: handle error cases
			p.AddPDUSessionAnchorAndULCL(smContext)
			smContext.BPManager.BPStatus = smf_context.AddingPSA
		}
	}

	return waitAllPfcpRsp(1, smf_context.SessionUpdateSuccess, smf_context.SessionUpdateFailed, resChan)
}

func (p *Processor) ReleaseSessionAtUPFs(
	smContext *smf_context.SMContext,
) smf_context.PFCPSessionResponseStatus {

	resChan := make(chan smf_context.SendPfcpResult)
	defer close(resChan)

	waitForReply := 0

	// note: do NOT remove datapath and tunnel in SM context before UPF(s) returned SessionReleaseSuccess!
	// cleanup happens in pdu_session.go after receiving all PFCP responses

	for _, pfcpSessionContext := range smContext.PFCPSessionContexts {
		upf := pfcpSessionContext.UPF
		nodeID := upf.GetNodeIDString()
		select {
		case <-upf.Association.Done():
			logger.PduSessLog.Warnf("UPF[%s] not associated, skip session release for %s",
				nodeID, pfcpSessionContext.PDUSessionParams())
			continue
		default:
			logger.PduSessLog.Infof("Init session release on UPF[%s] for %s",
				nodeID, pfcpSessionContext.PDUSessionParams())
			if pfcpSessionContext.RemoteSEID == 0 {
				logger.PduSessLog.Infof("%s not yet established on UPF[%s], do not release it",
					pfcpSessionContext.PDUSessionParams(), nodeID)
			} else {
				waitForReply += 1
				go releasePfcpSession(pfcpSessionContext, resChan)
			}
		}
	}

	if waitForReply == 0 {
		logger.PduSessLog.Warnln("No UPFs are associated to release the session")
		return smf_context.SessionReleaseFailed
	}

	return waitAllPfcpRsp(waitForReply, smf_context.SessionReleaseSuccess, smf_context.SessionReleaseFailed, resChan)
}

func RestorePDUSessionAtUPF(
	pfcpSessionContext *smf_context.PFCPSessionContext,
) smf_context.PFCPSessionResponseStatus {

	// pfcpSessionContext.Restoring is locked by caller
	// unlock happens in restorePfcpSession

	upf := pfcpSessionContext.UPF
	nodeID := upf.GetNodeIDString()

	if pfcpSessionContext.RemoteSEID > 0 {
		logger.CtxLog.Infof("Some other process already established %s on UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		return smf_context.SessionEstablishSuccess
	}

	select {
	case <-upf.Association.Done():
		logger.PduSessLog.Warnf("UPF[%s] not associated, skip session restoration for %s",
			nodeID, pfcpSessionContext.PDUSessionParams())
		return smf_context.SessionEstablishFailed
	default:
	}

	logger.PduSessLog.Infof("Init session restoration on UPF[%s] for %s",
		nodeID, pfcpSessionContext.PDUSessionParams())

	resChan := make(chan smf_context.SendPfcpResult)
	defer close(resChan)

	if pfcpSessionContext.RemoteSEID > 0 {
		logger.CtxLog.Infof("Some other process already established %s on UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		return smf_context.SessionEstablishSuccess
	}

	go restorePfcpSession(pfcpSessionContext, resChan)

	return waitAllPfcpRsp(1, smf_context.SessionEstablishSuccess, smf_context.SessionEstablishFailed, resChan)
}

func (p *Processor) QueryReport(
	smContext *smf_context.SMContext,
	upf *smf_context.UPF,
	urrs []*smf_context.URR,
	reportResaon models.TriggerType,
) smf_context.PFCPSessionResponseStatus {
	for _, urr := range urrs {
		urr.SetState(smf_context.RULE_QUERY)
	}

	resChan := make(chan smf_context.SendPfcpResult)
	defer close(resChan)

	pfcpSessionContext := smContext.PFCPSessionContexts[upf.ID]

	go modifyExistingPfcpSession(smContext, pfcpSessionContext, resChan, reportResaon)

	pfcpResponseState := waitAllPfcpRsp(1, smf_context.SessionReleaseSuccess, smf_context.SessionReleaseFailed, resChan)

	return pfcpResponseState
}

func establishPfcpSession(
	pfcpSessionContext *smf_context.PFCPSessionContext,
	resCh chan<- smf_context.SendPfcpResult,
) {
	upf := pfcpSessionContext.UPF
	nodeID := upf.GetNodeIDString()

	select {
	case <-upf.Association.Done():
		logger.PduSessLog.Warnf("UPF[%s] no longer associated, do not establish %s",
			nodeID, pfcpSessionContext.PDUSessionParams())
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Source: nodeID,
		}
		return
	default:
	}

	logger.PduSessLog.Infof("Sending SessionEstablishmentRequest for %s to UPF[%s]",
		pfcpSessionContext.PDUSessionParams(), nodeID)
	logger.PduSessLog.Tracef("Transfer the following PFCPSessionContext: %s", pfcpSessionContext)

	rcvMsg, err := pfcp_message.SendPfcpSessionEstablishmentRequest(pfcpSessionContext)
	if err != nil {
		logger.PduSessLog.Errorf("SessionEstablishmentRequest for %s to UPF[%s] error: %+v",
			pfcpSessionContext.PDUSessionParams(), nodeID, err)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Err:    err,
			Source: nodeID,
		}
		return
	}

	rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionEstablishmentResponse)
	if rsp.UPFSEID != nil {
		pfcpSessionContext.RemoteSEID = rsp.UPFSEID.Seid
		logger.PduSessLog.Infof("Received remote SEID %d for %s from UPF[%s]",
			rsp.UPFSEID.Seid, pfcpSessionContext.PDUSessionParams(), nodeID)
	}

	if rsp.Cause != nil && rsp.Cause.CauseValue == pfcpType.CauseRequestAccepted {
		logger.PduSessLog.Infof("Received SessionEstablishmentAccept for %s from UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)

		// set all session rules in UPF's PFCPSessionContext to state RULE_SYNCED
		pfcpSessionContext.MarkAsSyncedToUPFRecursive()

		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionEstablishSuccess,
			RcvMsg: rcvMsg,
			Source: nodeID,
		}
	} else {
		logger.PduSessLog.Errorf("Received SessionEstablishmentReject for %s from UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Err:    fmt.Errorf("cause[%d] if not request accepted", rsp.Cause.CauseValue),
			RcvMsg: rcvMsg,
			Source: nodeID,
		}
	}
}

func modifyExistingPfcpSession(
	smContext *smf_context.SMContext,
	pfcpSessionContext *smf_context.PFCPSessionContext,
	resCh chan<- smf_context.SendPfcpResult,
	reportResaon models.TriggerType,
) {
	upf := pfcpSessionContext.UPF
	nodeID := upf.GetNodeIDString()

	select {
	case <-upf.Association.Done():
		logger.PduSessLog.Warnf("UPF[%s] no longer associated, do not modify %s",
			nodeID, pfcpSessionContext.PDUSessionParams())
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionUpdateFailed,
			Source: nodeID,
		}
		return
	default:
	}

	logger.PduSessLog.Infof("Sending SessionModificationRequest for %s to UPF[%s]",
		pfcpSessionContext.PDUSessionParams(), nodeID)
	logger.PduSessLog.Tracef("Transfer the following PFCPSessionContext: %s", pfcpSessionContext)

	rcvMsg, err := pfcp_message.SendPfcpSessionModificationRequest(pfcpSessionContext)
	if err != nil {
		logger.PduSessLog.Errorf("SessionModificationRequest for %s from UPF[%s] error: %+v",
			pfcpSessionContext.PDUSessionParams(), nodeID, err)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionUpdateFailed,
			Err:    err,
			Source: nodeID,
		}
		return
	}

	rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionModificationResponse)
	if rsp.Cause != nil && rsp.Cause.CauseValue == pfcpType.CauseRequestAccepted {
		logger.PduSessLog.Infof("Received SessionModificationAccept for %s from UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionUpdateSuccess,
			RcvMsg: rcvMsg,
			Source: nodeID,
		}
		if rsp.UsageReport != nil {
			smContext.HandleReports(nil, rsp.UsageReport, nil, upf.NodeID, reportResaon)
		}

		// set all session rules in UPF's PFCPSessionContext to state RULE_SYNCED
		pfcpSessionContext.MarkAsSyncedToUPFRecursive()
	} else {
		logger.PduSessLog.Errorf("Received SessionModificationReject for %s from UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionUpdateFailed,
			Err:    fmt.Errorf("cause[%d] if not request accepted", rsp.Cause.CauseValue),
			RcvMsg: rcvMsg,
			Source: nodeID,
		}
	}
}

func restorePfcpSession(
	pfcpSessionContext *smf_context.PFCPSessionContext,
	resCh chan<- smf_context.SendPfcpResult,
) {
	defer pfcpSessionContext.Restoring.Unlock()

	upf := pfcpSessionContext.UPF
	nodeID := upf.GetNodeIDString()

	select {
	case <-upf.Association.Done():
		logger.PduSessLog.Warnf("UPF[%s] not associated, do not restore %s",
			nodeID, pfcpSessionContext.PDUSessionParams())
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Source: nodeID,
		}
		return
	default:
	}

	logger.PduSessLog.Infof("Sending SessionRecoveryRequest for %s to UPF[%s]",
		pfcpSessionContext.PDUSessionParams(), nodeID)

	rcvMsg, err := pfcp_message.SendPfcpSessionRecoveryRequest(pfcpSessionContext)
	if err != nil {
		logger.PduSessLog.Errorf("SessionRecoveryRequest for %s to [%s] error: %+v",
			pfcpSessionContext.PDUSessionParams(), nodeID, err)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Err:    err,
			Source: nodeID,
		}
		return
	}

	// the recovery request is a SessionEstablishmentRequest with all PDRs of the PFCPSessionContext
	// therefore, a PFCPSessionEstablishmentResponse comes back
	rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionEstablishmentResponse)
	if rsp.UPFSEID != nil {
		pfcpSessionContext.RemoteSEID = rsp.UPFSEID.Seid
		logger.PduSessLog.Infof("Received UPFSEID: %+v", rsp.UPFSEID)
	}

	if rsp.Cause != nil && rsp.Cause.CauseValue == pfcpType.CauseRequestAccepted {
		logger.PduSessLog.Infof("Received SessionRecoveryAccept for %s from UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionEstablishSuccess,
			RcvMsg: rcvMsg,
			Source: nodeID,
		}

		// set all session rules in UPF's PFCPSessionContext to state RULE_SYNCED
		pfcpSessionContext.MarkAsSyncedToUPFRecursive()
	} else {
		logger.PduSessLog.Errorf("Received SessionRecoveryReject for %s from UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Err:    fmt.Errorf("cause[%d] if not request accepted", rsp.Cause.CauseValue),
			RcvMsg: rcvMsg,
			Source: nodeID,
		}
	}
}

func releasePfcpSession(
	pfcpSessionContext *smf_context.PFCPSessionContext,
	resCh chan<- smf_context.SendPfcpResult,
) {
	upf := pfcpSessionContext.UPF
	nodeID := upf.GetNodeIDString()

	select {
	case <-upf.Association.Done():
		logger.PduSessLog.Warnf("UPF[%s] not associated, do not release %s",
			nodeID, pfcpSessionContext.PDUSessionParams())
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Source: nodeID,
		}
		return
	default:
	}

	logger.PduSessLog.Infof("Sending SessionReleaseRequest for %s to UPF[%s]",
		pfcpSessionContext.PDUSessionParams(), nodeID)

	rcvMsg, err := pfcp_message.SendPfcpSessionReleaseRequest(pfcpSessionContext)
	if err != nil {
		logger.PduSessLog.Errorf("SessionReleaseRequest for %s to UPF[%s] error: %+v",
			pfcpSessionContext.PDUSessionParams(), nodeID, err)
		resCh <- smf_context.SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Err:    err,
			Source: nodeID,
		}
		return
	}

	rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionDeletionResponse)
	if rsp.Cause != nil && rsp.Cause.CauseValue == pfcpType.CauseRequestAccepted {
		logger.PduSessLog.Infof("Received SessionReleaseAccept for %s from UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		resCh <- smf_context.SendPfcpResult{
			RcvMsg: rcvMsg,
			Status: smf_context.SessionReleaseSuccess,
			Source: nodeID,
		}
		// set all session rules in UPF's PFCPSessionContext to state RULE_SYNCED
		pfcpSessionContext.MarkAsSyncedToUPFRecursive()
	} else {
		logger.PduSessLog.Errorf("Received SessionReleaseReject for %s from UPF[%s]",
			pfcpSessionContext.PDUSessionParams(), nodeID)
		resCh <- smf_context.SendPfcpResult{
			RcvMsg: rcvMsg,
			Status: smf_context.SessionReleaseFailed,
			Err:    fmt.Errorf("cause[%d] if not request accepted", rsp.Cause.CauseValue),
			Source: nodeID,
		}
	}
}

// Collects the PFCP responses from all UPFs involved
// in the data path(s) of the PDU session.
// If one UPF failed, failure is reported.
// If one UPF timed out, the status of its partner is reported.
func waitAllPfcpRsp(
	pfcpPoolLen int,
	expectedState smf_context.PFCPSessionResponseStatus,
	failedState smf_context.PFCPSessionResponseStatus,
	resChan <-chan smf_context.SendPfcpResult,
) smf_context.PFCPSessionResponseStatus {
	pfcpState := expectedState
	timedOutUPFs := make(map[uuid.UUID]*smf_context.UPF)

	for i := 0; i < pfcpPoolLen; i++ {
		res := <-resChan

		ip := res.Source
		logger.PfcpLog.Infof("Handle PFCP response from UPF[%s]", ip)
		upf := smf_context.GetUserPlaneInformation().NodeIDToUPF[ip]

		if upf == nil {
			logger.PfcpLog.Errorf("Cannot find UPF Node ID %s in UserPlaneInformation", ip)
			return failedState
		}
		// UPF can be disassociated at this point due to delayed heartbeat
		// which detected a false-positive failure
		// simply do not process such a response
		select {
		case <-upf.Association.Done():
			logger.CtxLog.Warnf("UPF[%s] no longer associated, do not process late PFCP response",
				upf.GetNodeIDString())
			timedOutUPFs[upf.ID] = upf
			continue
		default:
		}
		// check if the PFCP update was received correctly
		if res.Status == smf_context.SessionEstablishFailed ||
			res.Status == smf_context.SessionUpdateFailed {
			// one of the UPFs failed, report failure
			logger.PfcpLog.Errorf("Session management process failed due to at least one UPF reporting %s",
				res.Status)

		} else if res.Status == smf_context.SessionReleaseFailed {
			// one of the UPFs failed, report failure
			logger.PfcpLog.Errorf("Session management process failed due to at least one UPF reporting %s",
				res.Status)
		}
	}
	// success or timeout

	// check the timeouted UPFs
	if len(timedOutUPFs) > 0 {
		if len(timedOutUPFs) == pfcpPoolLen {
			logger.PfcpLog.Errorln("All UPFs timed out")
			return failedState
		}
		for _, upf := range timedOutUPFs {
			logger.PfcpLog.Infof("UPF[%s] timed out", upf.GetNodeIDString())
		}
	}

	// at this point all went well and the pfcpState is success
	return pfcpState
}
