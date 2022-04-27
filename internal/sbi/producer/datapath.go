package producer

import (
	"context"
	"fmt"

	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
)

type PFCPState struct {
	upf     *smf_context.UPF
	pdrList []*smf_context.PDR
	farList []*smf_context.FAR
	barList []*smf_context.BAR
	qerList []*smf_context.QER
}

type SendPfcpResult struct {
	Status smf_context.PFCPSessionResponseStatus
	Err    error
}

// ActivateUPFSessionAndNotifyUE send all datapaths to UPFs and send result to UE
// It returns after all PFCP response have been returned or timed out,
// and before sending N1N2MessageTransfer request if it is needed.
func ActivateUPFSessionAndNotifyUE(smContext *smf_context.SMContext) []SendPfcpResult {
	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	pfcpPool := make(map[string]*PFCPState)

	for _, dataPath := range smContext.Tunnel.DataPathPool {
		if dataPath.Activated {
			for curDataPathNode := dataPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
				pdrList := make([]*smf_context.PDR, 0, 2)
				farList := make([]*smf_context.FAR, 0, 2)
				qerList := make([]*smf_context.QER, 0, 2)

				if curDataPathNode.UpLinkTunnel != nil && curDataPathNode.UpLinkTunnel.PDR != nil {
					pdrList = append(pdrList, curDataPathNode.UpLinkTunnel.PDR)
					farList = append(farList, curDataPathNode.UpLinkTunnel.PDR.FAR)
					if curDataPathNode.UpLinkTunnel.PDR.QER != nil {
						qerList = append(qerList, curDataPathNode.UpLinkTunnel.PDR.QER...)
					}
				}
				if curDataPathNode.DownLinkTunnel != nil && curDataPathNode.DownLinkTunnel.PDR != nil {
					pdrList = append(pdrList, curDataPathNode.DownLinkTunnel.PDR)
					farList = append(farList, curDataPathNode.DownLinkTunnel.PDR.FAR)
					// skip send QER because uplink and downlink shared one QER
				}

				pfcpState := pfcpPool[curDataPathNode.GetNodeIP()]
				if pfcpState == nil {
					pfcpPool[curDataPathNode.GetNodeIP()] = &PFCPState{
						upf:     curDataPathNode.UPF,
						pdrList: pdrList,
						farList: farList,
						qerList: qerList,
					}
				} else {
					pfcpState.pdrList = append(pfcpState.pdrList, pdrList...)
					pfcpState.farList = append(pfcpState.farList, farList...)
					pfcpState.qerList = append(pfcpState.qerList, qerList...)
				}
			}
		}
	}

	resChan := make(chan SendPfcpResult)

	for ip, pfcp := range pfcpPool {
		sessionContext, exist := smContext.PFCPContext[ip]
		if !exist || sessionContext.RemoteSEID == 0 {
			go establishPfcpSession(smContext, pfcp, resChan)
		} else {
			go modifyExistingPfcpSession(smContext, pfcp, resChan)
		}
	}

	// collect all responses
	resList := make([]SendPfcpResult, 0, len(pfcpPool))
	for i := 0; i < len(pfcpPool); i++ {
		resList = append(resList, <-resChan)
	}

	return resList
}

func establishPfcpSession(smContext *smf_context.SMContext, state *PFCPState, resCh chan SendPfcpResult) {
	logger.PduSessLog.Infoln("Sending PFCP Session Establishment Request")

	rcvMsg, err := pfcp_message.SendPfcpSessionEstablishmentRequest(
		state.upf, smContext, state.pdrList, state.farList, state.barList, state.qerList)
	if err != nil {
		logger.PduSessLog.Warnf("Sending PFCP Session Establishment Request error: %+v", err)
		resCh <- SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Err:    err,
		}
		sendPDUSessionEstablishmentReject(smContext, nasMessage.Cause5GSMNetworkFailure)
		return
	}

	rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionEstablishmentResponse)
	if rsp.UPFSEID != nil {
		NodeIDtoIP := rsp.NodeID.ResolveNodeIdToIp().String()
		pfcpSessionCtx := smContext.PFCPContext[NodeIDtoIP]
		pfcpSessionCtx.RemoteSEID = rsp.UPFSEID.Seid
	}

	if rsp.Cause != nil && rsp.Cause.CauseValue == pfcpType.CauseRequestAccepted {
		logger.PduSessLog.Infoln("Received PFCP Session Establishment Accepted Response")
		resCh <- SendPfcpResult{
			Status: smf_context.SessionEstablishSuccess,
		}
	} else {
		logger.PduSessLog.Infoln("Received PFCP Session Establishment Not Accepted Response")
		resCh <- SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Err:    fmt.Errorf("cause[%d] if not request accepted", rsp.Cause.CauseValue),
		}
		// TODO: set appropriate 5GSM cause according to PFCP cause value
		sendPDUSessionEstablishmentReject(smContext, nasMessage.Cause5GSMNetworkFailure)
		return
	}

	// The following is the process after receiving a successful pfcp response

	// This lock is acuired when all PFCP results has been send to resCh
	// and ActivateUPFSessionAndNotifyUE has processed them.
	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	ANUPF := smContext.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode
	if rsp.Cause != nil && rsp.Cause.CauseValue == pfcpType.CauseRequestAccepted &&
		ANUPF.UPF.NodeID.ResolveNodeIdToIp().Equal(rsp.NodeID.ResolveNodeIdToIp()) {
		n1n2Request := models.N1N2MessageTransferRequest{}

		if smNasBuf, err := smf_context.BuildGSMPDUSessionEstablishmentAccept(smContext); err != nil {
			logger.PduSessLog.Errorf("Build GSM PDUSessionEstablishmentAccept failed: %s", err)
		} else {
			n1n2Request.BinaryDataN1Message = smNasBuf
		}
		if n2Pdu, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext); err != nil {
			logger.PduSessLog.Errorf("Build PDUSessionResourceSetupRequestTransfer failed: %s", err)
		} else {
			n1n2Request.BinaryDataN2Information = n2Pdu
		}

		n1n2Request.JsonData = &models.N1N2MessageTransferReqData{
			PduSessionId: smContext.PDUSessionID,
			N1MessageContainer: &models.N1MessageContainer{
				N1MessageClass:   "SM",
				N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
			},
			N2InfoContainer: &models.N2InfoContainer{
				N2InformationClass: models.N2InformationClass_SM,
				SmInfo: &models.N2SmInformation{
					PduSessionId: smContext.PDUSessionID,
					N2InfoContent: &models.N2InfoContent{
						NgapIeType: models.NgapIeType_PDU_RES_SETUP_REQ,
						NgapData: &models.RefToBinaryData{
							ContentId: "N2SmInformation",
						},
					},
					SNssai: smContext.Snssai,
				},
			},
		}

		rspData, _, err := smContext.
			CommunicationClient.
			N1N2MessageCollectionDocumentApi.
			N1N2MessageTransfer(context.Background(), smContext.Supi, n1n2Request)
		smContext.SMContextState = smf_context.Active
		if err != nil {
			logger.PfcpLog.Warnf("Send N1N2Transfer failed")
		}
		if rspData.Cause == models.N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
			logger.PfcpLog.Warnf("%v", rspData.Cause)
		}
	}
}

func sendPDUSessionEstablishmentReject(smContext *smf_context.SMContext, nasErrorCause uint8) {
	n1n2Request := models.N1N2MessageTransferRequest{}
	if smNasBuf, err := smf_context.BuildGSMPDUSessionEstablishmentReject(
		smContext, nasMessage.Cause5GSMNetworkFailure); err != nil {
		logger.PduSessLog.Errorf("Build GSM PDUSessionEstablishmentReject failed: %s", err)
	} else {
		n1n2Request.BinaryDataN1Message = smNasBuf
	}
	n1n2Request.JsonData = &models.N1N2MessageTransferReqData{
		PduSessionId: smContext.PDUSessionID,
		N1MessageContainer: &models.N1MessageContainer{
			N1MessageClass:   "SM",
			N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
		},
	}
	rspData, _, err := smContext.
		CommunicationClient.
		N1N2MessageCollectionDocumentApi.
		N1N2MessageTransfer(context.Background(), smContext.Supi, n1n2Request)
	smContext.SMContextState = smf_context.InActive
	if err != nil {
		logger.PfcpLog.Warnf("Send N1N2Transfer failed")
	}
	if rspData.Cause == models.N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
		logger.PfcpLog.Warnf("%v", rspData.Cause)
	}
}

func modifyExistingPfcpSession(smContext *smf_context.SMContext, state *PFCPState, resCh chan SendPfcpResult) {
	logger.PduSessLog.Infoln("Sending PFCP Session Modification Request")

	rcvMsg, err := pfcp_message.SendPfcpSessionModificationRequest(
		state.upf, smContext, state.pdrList, state.farList, state.barList, state.qerList)
	if err != nil {
		logger.PduSessLog.Warnf("Sending PFCP Session Modification Request error: %+v", err)
		resCh <- SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Err:    err,
		}
		return
	}

	logger.PduSessLog.Infoln("Received PFCP Session Modification Response")

	rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionModificationResponse)
	if rsp.Cause != nil && rsp.Cause.CauseValue == pfcpType.CauseRequestAccepted {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionUpdateSuccess,
		}
	} else {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionUpdateFailed,
			Err:    fmt.Errorf("cause[%d] if not request accepted", rsp.Cause.CauseValue),
		}
	}
}

func updateAnUpfPfcpSession(smContext *smf_context.SMContext,
	pdrList []*smf_context.PDR, farList []*smf_context.FAR,
	barList []*smf_context.BAR, qerList []*smf_context.QER,
) smf_context.PFCPSessionResponseStatus {
	logger.PduSessLog.Infoln("Sending PFCP Session Modification Request to AN UPF")

	defaultPath := smContext.Tunnel.DataPathPool.GetDefaultPath()
	ANUPF := defaultPath.FirstDPNode
	rcvMsg, err := pfcp_message.SendPfcpSessionModificationRequest(
		ANUPF.UPF, smContext, pdrList, farList, barList, qerList)
	if err != nil {
		logger.PduSessLog.Warnf("Sending PFCP Session Modification Request to AN UPF error: %+v", err)
		return smf_context.SessionUpdateFailed
	}

	rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionModificationResponse)
	if rsp.Cause == nil || rsp.Cause.CauseValue != pfcpType.CauseRequestAccepted {
		logger.PduSessLog.Warn("Received PFCP Session Modification Not Accepted Response from AN UPF")
		return smf_context.SessionUpdateFailed
	}

	logger.PduSessLog.Info("Received PFCP Session Modification Accepted Response from AN UPF")

	if smf_context.SMF_Self().ULCLSupport && smContext.BPManager != nil {
		if smContext.BPManager.BPStatus == smf_context.UnInitialized {
			logger.PfcpLog.Infoln("Add PSAAndULCL")
			// TODO: handle error cases
			AddPDUSessionAnchorAndULCL(smContext)
			smContext.BPManager.BPStatus = smf_context.AddingPSA
		}
	}

	return smf_context.SessionUpdateSuccess
}

func ReleaseTunnel(smContext *smf_context.SMContext) []SendPfcpResult {
	resChan := make(chan SendPfcpResult)

	deletedPFCPNode := make(map[string]bool)
	for _, dataPath := range smContext.Tunnel.DataPathPool {
		var targetNodes []*smf_context.DataPathNode
		for curDataPathNode := dataPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
			targetNodes = append(targetNodes, curDataPathNode)
		}
		dataPath.DeactivateTunnelAndPDR(smContext)
		for _, curDataPathNode := range targetNodes {
			curUPFID, err := curDataPathNode.GetUPFID()
			if err != nil {
				logger.PduSessLog.Error(err)
				continue
			}
			if _, exist := deletedPFCPNode[curUPFID]; !exist {
				go deletePfcpSession(curDataPathNode.UPF, smContext, resChan)
				deletedPFCPNode[curUPFID] = true
			}
		}
	}

	// collect all responses
	resList := make([]SendPfcpResult, 0, len(deletedPFCPNode))
	for i := 0; i < len(deletedPFCPNode); i++ {
		resList = append(resList, <-resChan)
	}

	return resList
}

func deletePfcpSession(upf *smf_context.UPF, ctx *smf_context.SMContext, resCh chan<- SendPfcpResult) {
	logger.PduSessLog.Infoln("Sending PFCP Session Deletion Request")

	rcvMsg, err := pfcp_message.SendPfcpSessionDeletionRequest(upf, ctx)
	if err != nil {
		logger.PduSessLog.Warnf("Sending PFCP Session Deletion Request error: %+v", err)
		resCh <- SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Err:    err,
		}
		return
	}

	rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionDeletionResponse)
	if rsp.Cause != nil && rsp.Cause.CauseValue == pfcpType.CauseRequestAccepted {
		logger.PduSessLog.Info("Received PFCP Session Deletion Accepted Response")
		resCh <- SendPfcpResult{
			Status: smf_context.SessionReleaseSuccess,
		}
	} else {
		logger.PduSessLog.Warn("Received PFCP Session Deletion Not Accepted Response")
		resCh <- SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Err:    fmt.Errorf("cause[%d] if not request accepted", rsp.Cause.CauseValue),
		}
	}
}
