package processor

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/wmnsk/go-pfcp/ie"
	goPfcpMessage "github.com/wmnsk/go-pfcp/message"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

type PFCPState struct {
	upf     *smf_context.UPF
	pdrList []*smf_context.PDR
	farList []*smf_context.FAR
	barList []*smf_context.BAR
	qerList []*smf_context.QER
	urrList []*smf_context.URR
}

type SessionEstablishmentPFCPClient interface {
	SendSessionEstablishmentRequest(
		context.Context, *goPfcpMessage.SessionEstablishmentRequest, *net.UDPAddr, uint64,
	) (*goPfcpMessage.SessionEstablishmentResponse, error)
}

type SessionModificationPFCPClient interface {
	SendSessionModificationRequest(
		context.Context, *goPfcpMessage.SessionModificationRequest, *net.UDPAddr, uint64,
	) (*goPfcpMessage.SessionModificationResponse, error)
}

type SessionDeletionPFCPClient interface {
	SendSessionDeletionRequest(
		context.Context, *goPfcpMessage.SessionDeletionRequest, *net.UDPAddr, uint64,
	) (*goPfcpMessage.SessionDeletionResponse, error)
}

type SendPfcpResult struct {
	Status smf_context.PFCPSessionResponseStatus
	Err    error
}

// ActivateUPFSession send all datapaths to UPFs and send result to UE
// It returns after all PFCP response have been returned or timed out,
// and before sending N1N2MessageTransfer request if it is needed.
func (p *Processor) ActivateUPFSession(
	smContext *smf_context.SMContext,
	notifyUeHander func(*smf_context.SMContext, bool),
) {
	smContext.Log.Traceln("In ActivateUPFSession")

	pfcpPool := make(map[string]*PFCPState)

	for _, dataPath := range smContext.Tunnel.DataPathPool {
		if !dataPath.Activated {
			continue
		}
		for node := dataPath.FirstDPNode; node != nil; node = node.Next() {
			pdrList := make([]*smf_context.PDR, 0, 2)
			farList := make([]*smf_context.FAR, 0, 2)
			qerList := make([]*smf_context.QER, 0, 2)
			urrList := make([]*smf_context.URR, 0, 2)

			if node.UpLinkTunnel != nil && node.UpLinkTunnel.PDR != nil {
				pdrList = append(pdrList, node.UpLinkTunnel.PDR)
				farList = append(farList, node.UpLinkTunnel.PDR.FAR)
				if node.UpLinkTunnel.PDR.QER != nil {
					qerList = append(qerList, node.UpLinkTunnel.PDR.QER...)
				}
				if node.UpLinkTunnel.PDR.URR != nil {
					urrList = append(urrList, node.UpLinkTunnel.PDR.URR...)
				}
			}
			if node.DownLinkTunnel != nil && node.DownLinkTunnel.PDR != nil {
				pdrList = append(pdrList, node.DownLinkTunnel.PDR)
				farList = append(farList, node.DownLinkTunnel.PDR.FAR)
				if node.DownLinkTunnel.PDR.URR != nil {
					urrList = append(urrList, node.DownLinkTunnel.PDR.URR...)
				}
				// skip send QER because uplink and downlink shared one QER
			}

			pfcpState := pfcpPool[node.GetNodeIP()]
			if pfcpState == nil {
				pfcpPool[node.GetNodeIP()] = &PFCPState{
					upf:     node.UPF,
					pdrList: pdrList,
					farList: farList,
					qerList: qerList,
					urrList: urrList,
				}
			} else {
				pfcpState.pdrList = append(pfcpState.pdrList, pdrList...)
				pfcpState.farList = append(pfcpState.farList, farList...)
				pfcpState.qerList = append(pfcpState.qerList, qerList...)
				pfcpState.urrList = append(pfcpState.urrList, urrList...)
			}
		}
	}

	resChan := make(chan SendPfcpResult)
	establishmentTargets := make(map[string]*PFCPState)

	for ip, pfcp := range pfcpPool {
		urrIds := []uint32{}
		for _, urr := range pfcp.urrList {
			urrIds = append(urrIds, urr.URRID)
		}
		logger.PduSessLog.Tracef("[ActivateUPFSession] About to send to UPF[%s]: %d PDRs, %d FARs, "+
			"%d URRs (aggregated IDs: %v)", ip, len(pfcp.pdrList), len(pfcp.farList), len(pfcp.urrList), urrIds)
		sessionContext, exist := smContext.PFCPContext[ip]
		if !exist || sessionContext == nil || sessionContext.RemoteSEID == 0 {
			establishmentTargets[ip] = pfcp
			go p.establishPfcpSession(smContext, pfcp, resChan)
		} else {
			go p.modifyExistingPfcpSession(smContext, pfcp, resChan, "")
		}
	}

	success := waitAllPfcpRsp(smContext, len(pfcpPool), resChan, nil)
	close(resChan)

	// Initial PDU Session Establishment is atomic across every required UPF.
	// Roll back only sessions created by this activation; never delete a
	// pre-existing PFCP session that was merely being modified.
	if !success && notifyUeHander != nil {
		if err := p.rollbackEstablishedPfcpSessions(smContext, establishmentTargets); err != nil {
			logger.PduSessLog.Errorf("rollback partially established PFCP sessions: %v", err)
		}
	}
	if notifyUeHander != nil {
		notifyUeHander(smContext, success)
	}
}

func (p *Processor) QueryReport(smContext *smf_context.SMContext, upf *smf_context.UPF,
	urrs []*smf_context.URR, reportResaon models.Chf_ConvCharging_TriggerType,
) {
	for _, urr := range urrs {
		smContext.SetUrrState(upf.UUID(), urr.URRID, smf_context.RULE_QUERY)
	}

	pfcpState := &PFCPState{
		upf:     upf,
		urrList: urrs,
	}

	resChan := make(chan SendPfcpResult)
	go p.modifyExistingPfcpSession(smContext, pfcpState, resChan, reportResaon)
	pfcpResult := <-resChan

	if pfcpResult.Err != nil {
		logger.PduSessLog.Errorf("Query URR Report by PFCP Session Mod Request fail: %v", pfcpResult.Err)
		return
	}
}

func (p *Processor) establishPfcpSession(
	smContext *smf_context.SMContext,
	state *PFCPState,
	resCh chan<- SendPfcpResult,
) {
	logger.PduSessLog.Infoln("Sending PFCP Session Establishment Request")

	fail := func(err error) {
		logger.PduSessLog.Warnf("PFCP Session Establishment failed: %+v", err)
		resCh <- SendPfcpResult{
			Status: smf_context.SessionEstablishFailed,
			Err:    err,
		}
	}
	if state == nil || state.upf == nil {
		fail(fmt.Errorf("PFCP Session Establishment has no target UPF"))
		return
	}
	associationContext, finishSessionWork, err := state.upf.BeginSessionWork()
	if err != nil {
		fail(err)
		return
	}
	defer finishSessionWork()

	nodeIP := state.upf.NodeID.ResolveNodeIdToIp()
	nodeIPString := nodeIP.String()
	sessionContext, exists := smContext.PFCPContext[nodeIPString]
	if !exists || sessionContext == nil {
		fail(fmt.Errorf("PFCP session context for UPF %s does not exist", nodeIPString))
		return
	}

	request, err := pfcp_message.BuildSessionEstablishmentRequest(
		state.upf.NodeID, nodeIPString, state.upf.UUID(), smContext,
		state.pdrList, state.farList, state.barList, state.qerList, state.urrList,
	)
	if err != nil {
		fail(fmt.Errorf("build PFCP Session Establishment Request: %w", err))
		return
	}

	client, ok := p.getActivePFCPClient().(SessionEstablishmentPFCPClient)
	if !ok {
		fail(fmt.Errorf("go-pfcp Session Establishment client is not configured"))
		return
	}
	response, err := client.SendSessionEstablishmentRequest(
		associationContext, request,
		&net.UDPAddr{IP: nodeIP, Port: pfcpPeerPort},
		sessionContext.LocalSEID,
	)
	if err != nil {
		fail(err)
		return
	}
	if response == nil {
		fail(fmt.Errorf("PFCP Session Establishment client returned a nil response"))
		return
	}
	if response.Cause == nil {
		fail(fmt.Errorf("PFCP Session Establishment Response is missing Cause"))
		return
	}

	cause, err := response.Cause.Cause()
	if err != nil {
		fail(fmt.Errorf("decode PFCP Session Establishment Cause: %w", err))
		return
	}
	if cause != ie.CauseRequestAccepted {
		fail(fmt.Errorf("PFCP Session Establishment rejected with Cause %d", cause))
		return
	}

	if response.UPFSEID == nil {
		fail(fmt.Errorf("accepted PFCP Session Establishment Response is missing UP F-SEID"))
		return
	}
	upfFSEID, err := response.UPFSEID.FSEID()
	if err != nil {
		fail(fmt.Errorf("decode PFCP Session Establishment UP F-SEID: %w", err))
		return
	}
	// Store the UPF SEID as soon as an accepted response identifies the remote
	// session. If a Created PDR is malformed, later cleanup still knows which
	// remote session must be deleted.
	sessionContext.RemoteSEID = upfFSEID.SEID
	if err = applyCreatedPDRs(response.CreatedPDR, sessionContext, state.pdrList); err != nil {
		fail(err)
		return
	}

	logger.PduSessLog.Infoln("Received PFCP Session Establishment Accepted Response")
	resCh <- SendPfcpResult{Status: smf_context.SessionEstablishSuccess}
}

func applyCreatedPDRs(
	createdPDRs []*ie.IE,
	sessionContext *smf_context.PFCPSessionContext,
	requestedPDRs []*smf_context.PDR,
) error {
	pdrByID := make(map[uint16]*smf_context.PDR, len(requestedPDRs)+len(sessionContext.PDRs))
	for id, pdr := range sessionContext.PDRs {
		pdrByID[id] = pdr
	}
	for _, pdr := range requestedPDRs {
		if pdr != nil {
			pdrByID[pdr.PDRID] = pdr
		}
	}

	for _, createdPDR := range createdPDRs {
		if createdPDR == nil {
			return fmt.Errorf("PFCP Session Establishment Response contains a nil created PDR")
		}
		children, err := createdPDR.CreatedPDR()
		if err != nil {
			return fmt.Errorf("decode Created PDR: %w", err)
		}
		var pdrIDIE, fteidIE *ie.IE
		for _, child := range children {
			switch child.Type {
			case ie.PDRID:
				pdrIDIE = child
			case ie.FTEID:
				fteidIE = child
			}
		}
		if pdrIDIE == nil {
			return fmt.Errorf("created PDR is missing PDR ID")
		}
		pdrID, err := pdrIDIE.PDRID()
		if err != nil {
			return fmt.Errorf("decode Created PDR ID: %w", err)
		}
		// F-TEID is present only when the UPF allocated it (for example, when
		// the Create PDR requested CH=1). A Created PDR without F-TEID needs no
		// local rule update.
		if fteidIE == nil {
			continue
		}
		fteid, err := fteidIE.FTEID()
		if err != nil {
			return fmt.Errorf("decode F-TEID for Created PDR %d: %w", pdrID, err)
		}
		pdr := pdrByID[pdrID]
		if pdr == nil {
			return fmt.Errorf("created PDR refers to unknown PDR ID %d", pdrID)
		}
		pdr.PDI.LocalFTeid = &pfcptype.FTEID{
			Chid:        fteid.HasChID(),
			Ch:          fteid.HasCh(),
			V4:          fteid.HasIPv4(),
			V6:          fteid.HasIPv6(),
			Teid:        fteid.TEID,
			Ipv4Address: append(net.IP(nil), fteid.IPv4Address...),
			Ipv6Address: append(net.IP(nil), fteid.IPv6Address...),
			ChooseId:    fteid.ChooseID,
		}
	}
	return nil
}

// rollbackEstablishedPfcpSessions deletes only PFCP sessions that were
// created during the failed activation and therefore now have a RemoteSEID.
// Deletions run concurrently so one slow UPF does not serialize every peer's
// transaction timeout. A RemoteSEID is cleared only after an accepted delete.
func (p *Processor) rollbackEstablishedPfcpSessions(
	smContext *smf_context.SMContext,
	establishmentTargets map[string]*PFCPState,
) error {
	type rollbackTarget struct {
		ip             string
		upf            *smf_context.UPF
		sessionContext *smf_context.PFCPSessionContext
		remoteSEID     uint64
	}
	targets := make([]rollbackTarget, 0, len(establishmentTargets))
	errList := make([]error, 0)
	for ip, state := range establishmentTargets {
		sessionContext := smContext.PFCPContext[ip]
		if sessionContext == nil || sessionContext.RemoteSEID == 0 {
			continue
		}
		if state == nil || state.upf == nil {
			errList = append(errList, fmt.Errorf("UPF %s has a remote PFCP session but no rollback target", ip))
			continue
		}
		targets = append(targets, rollbackTarget{
			ip:             ip,
			upf:            state.upf,
			sessionContext: sessionContext,
			remoteSEID:     sessionContext.RemoteSEID,
		})
	}

	type rollbackResult struct {
		target rollbackTarget
		err    error
	}
	resultCh := make(chan rollbackResult, len(targets))
	for _, target := range targets {
		go func() {
			resultCh <- rollbackResult{
				target: target,
				err: p.deleteEstablishedPfcpSession(
					target.upf, target.sessionContext.LocalSEID, target.remoteSEID,
				),
			}
		}()
	}
	for range targets {
		result := <-resultCh
		if result.err != nil {
			errList = append(errList, fmt.Errorf("UPF %s: %w", result.target.ip, result.err))
			continue
		}
		// Do not clear a newer session if another lifecycle operation replaced
		// the SEID while this deletion was in flight.
		if result.target.sessionContext.RemoteSEID == result.target.remoteSEID {
			result.target.sessionContext.RemoteSEID = 0
		}
	}
	return errors.Join(errList...)
}

func (p *Processor) deleteEstablishedPfcpSession(
	upf *smf_context.UPF,
	localSEID uint64,
	remoteSEID uint64,
) error {
	associationContext, err := upf.AssociationContext()
	if err != nil {
		return err
	}
	client, ok := p.getActivePFCPClient().(SessionDeletionPFCPClient)
	if !ok {
		return fmt.Errorf("go-pfcp Session Deletion client is not configured")
	}
	request := goPfcpMessage.NewSessionDeletionRequest(0, 0, remoteSEID, 0, 0)
	response, err := client.SendSessionDeletionRequest(
		associationContext, request,
		&net.UDPAddr{IP: upf.NodeID.ResolveNodeIdToIp(), Port: pfcpPeerPort},
		localSEID,
	)
	if err != nil {
		return err
	}
	if response == nil || response.Cause == nil {
		return fmt.Errorf("PFCP Session Deletion Response is missing Cause")
	}
	cause, err := response.Cause.Cause()
	if err != nil {
		return fmt.Errorf("decode PFCP Session Deletion Cause: %w", err)
	}
	if cause != ie.CauseRequestAccepted {
		return fmt.Errorf("PFCP Session Deletion rejected with Cause %d", cause)
	}
	return nil
}

func (p *Processor) sendSessionModificationRequest(
	smContext *smf_context.SMContext,
	state *PFCPState,
) (*goPfcpMessage.SessionModificationResponse, error) {
	if state == nil || state.upf == nil {
		return nil, fmt.Errorf("PFCP Session Modification has no target UPF")
	}
	associationContext, finishSessionWork, err := state.upf.BeginSessionWork()
	if err != nil {
		return nil, err
	}
	defer finishSessionWork()

	nodeIP := state.upf.NodeID.ResolveNodeIdToIp()
	nodeIPString := nodeIP.String()
	sessionContext := smContext.PFCPContext[nodeIPString]
	if sessionContext == nil {
		return nil, fmt.Errorf("PFCP session context for UPF %s does not exist", nodeIPString)
	}
	request, err := pfcp_message.BuildSessionModificationRequest(
		nodeIPString, state.upf.UUID(), smContext,
		state.pdrList, state.farList, state.barList, state.qerList, state.urrList,
	)
	if err != nil {
		return nil, fmt.Errorf("build PFCP Session Modification Request: %w", err)
	}
	client, ok := p.getActivePFCPClient().(SessionModificationPFCPClient)
	if !ok {
		return nil, fmt.Errorf("go-pfcp Session Modification client is not configured")
	}
	response, err := client.SendSessionModificationRequest(
		associationContext, request,
		&net.UDPAddr{IP: nodeIP, Port: pfcpPeerPort},
		sessionContext.LocalSEID,
	)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("PFCP Session Modification client returned a nil response")
	}
	return response, nil
}

func (p *Processor) modifyExistingPfcpSession(
	smContext *smf_context.SMContext,
	state *PFCPState,
	resCh chan<- SendPfcpResult,
	reportReason models.Chf_ConvCharging_TriggerType,
) {
	logger.PduSessLog.Infoln("Sending PFCP Session Modification Request")
	response, err := p.sendSessionModificationRequest(smContext, state)
	if err != nil {
		logger.PduSessLog.Warnf("Sending PFCP Session Modification Request error: %+v", err)
		resCh <- SendPfcpResult{Status: smf_context.SessionUpdateFailed, Err: err}
		return
	}
	if response.Cause == nil {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionUpdateFailed,
			Err:    fmt.Errorf("PFCP Session Modification Response is missing Cause"),
		}
		return
	}
	cause, err := response.Cause.Cause()
	if err != nil {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionUpdateFailed,
			Err:    fmt.Errorf("decode PFCP Session Modification Cause: %w", err),
		}
		return
	}
	if cause != ie.CauseRequestAccepted {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionUpdateFailed,
			Err:    fmt.Errorf("PFCP Session Modification rejected with Cause %d", cause),
		}
		return
	}

	logger.PduSessLog.Infoln("Received PFCP Session Modification Accepted Response")
	if len(response.UsageReport) != 0 {
		if err = smContext.HandleReports(response.UsageReport, state.upf.NodeID, reportReason); err != nil {
			logger.PduSessLog.Errorf(
				"decode one or more PFCP Session Modification Usage Reports; valid reports were preserved: %v",
				err,
			)
		}
	}
	resCh <- SendPfcpResult{Status: smf_context.SessionUpdateSuccess}
}

func waitAllPfcpRsp(
	smContext *smf_context.SMContext,
	pfcpPoolLen int,
	resChan <-chan SendPfcpResult,
	notifyUeHander func(*smf_context.SMContext, bool),
) bool {
	success := true
	for i := 0; i < pfcpPoolLen; i++ {
		res := <-resChan
		if res.Status == smf_context.SessionEstablishFailed ||
			res.Status == smf_context.SessionUpdateFailed {
			success = false
		}
	}
	if notifyUeHander != nil {
		notifyUeHander(smContext, success)
	}
	return success
}

func (p *Processor) EstHandler(isDone <-chan struct{},
	smContext *smf_context.SMContext, success bool,
) {
	// Waiting for Create SMContext Request completed
	if isDone != nil {
		<-isDone
	}
	if success {
		p.sendPDUSessionEstablishmentAccept(smContext)
	} else {
		// TODO: set appropriate 5GSM cause according to PFCP cause value
		p.sendPDUSessionEstablishmentReject(smContext, nasie.Cause5GSM_NwFailure)
	}
}

func ModHandler(smContext *smf_context.SMContext, success bool) {
}

func (p *Processor) sendPDUSessionEstablishmentReject(
	smContext *smf_context.SMContext,
	nasErrorCause uint8,
) {
	// Local/NF cleanup must not depend on whether AMF notification succeeds.
	// The actual SMContext removal waits for the caller-held SMLock, so this
	// defer is safe while the establishment goroutine is still finishing.
	defer p.RemoveSMContextFromAllNF(smContext, true)

	smNasBuf, err := smf_context.BuildGSMPDUSessionEstablishmentReject(
		smContext, nasErrorCause)
	if err != nil {
		logger.PduSessLog.Errorf("Build GSM PDUSessionEstablishmentReject failed: %s", err)
		return
	}

	n1n2Request := models.N1N2MessageTransferRequestBody{
		BinaryDataN1Message: &multipart.RelatedContent{ContentID: "GSM_NAS", Content: smNasBuf},
		JsonData: &models.Amf_Comm_N1N2MessageTransferReqData{
			PduSessionId: smContext.PDUSessionID,
			N1MessageContainer: &models.Amf_Comm_N1MessageContainer{
				N1MessageClass:   "SM",
				N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
			},
		},
	}

	smContext.SetState(smf_context.InActive)

	ctx, _, errToken := smf_context.GetSelf().GetTokenCtx(
		models.Nrf_NFMgmt_ServiceName_NAMF_COMM, models.Nrf_NFMgmt_NFType_AMF)
	if errToken != nil {
		logger.PduSessLog.Warnf("Get NAMF_COMM context failed: %s", errToken)
		return
	}
	rspData, err := p.Consumer().
		N1N2MessageTransfer(ctx, smContext.Supi, n1n2Request, smContext.CommunicationClientApiPrefix)
	if err != nil || rspData == nil {
		logger.ConsumerLog.Warnf("N1N2MessageTransfer for SendPDUSessionEstablishmentReject failed: %+v", err)
		return
	}

	if rspData.Cause == models.Amf_Comm_N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
		logger.PduSessLog.Warnf("%v", rspData.Cause)
	}
}

func (p *Processor) sendPDUSessionEstablishmentAccept(
	smContext *smf_context.SMContext,
) {
	smNasBuf, err := smf_context.BuildGSMPDUSessionEstablishmentAccept(smContext)
	if err != nil {
		logger.PduSessLog.Errorf("Build GSM PDUSessionEstablishmentAccept failed: %s", err)
		return
	}

	n2Pdu, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext)
	if err != nil {
		logger.PduSessLog.Errorf("Build PDUSessionResourceSetupRequestTransfer failed: %s", err)
		return
	}

	n1n2Request := models.N1N2MessageTransferRequestBody{
		BinaryDataN1Message:     &multipart.RelatedContent{ContentID: "GSM_NAS", Content: smNasBuf},
		BinaryDataN2Information: &multipart.RelatedContent{ContentID: "N2SmInformation", Content: n2Pdu},
		JsonData: &models.Amf_Comm_N1N2MessageTransferReqData{
			PduSessionId: smContext.PDUSessionID,
			N1MessageContainer: &models.Amf_Comm_N1MessageContainer{
				N1MessageClass:   "SM",
				N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
			},
			N2InfoContainer: &models.Amf_Comm_N2InfoContainer{
				N2InformationClass: models.Amf_Comm_N2InformationClass_SM,
				SmInfo: &models.Amf_Comm_N2SmInformation{
					PduSessionId: smContext.PDUSessionID,
					N2InfoContent: &models.Amf_Comm_N2InfoContent{
						NgapIeType: models.Amf_Comm_NgapIeType_PDU_RES_SETUP_REQ,
						NgapData: &models.RefToBinaryData{
							ContentId: "N2SmInformation",
						},
					},
					SNssai: smContext.SNssai,
				},
			},
		},
	}

	ctx, _, err := smf_context.GetSelf().GetTokenCtx(models.Nrf_NFMgmt_ServiceName_NAMF_COMM, models.Nrf_NFMgmt_NFType_AMF)
	if err != nil {
		logger.PduSessLog.Warnf("Get NAMF_COMM context failed: %s", err)
		return
	}

	rspData, err := p.Consumer().
		N1N2MessageTransfer(ctx, smContext.Supi, n1n2Request, smContext.CommunicationClientApiPrefix)
	if err != nil || rspData == nil {
		logger.ConsumerLog.Warnf("N1N2MessageTransfer for sendPDUSessionEstablishmentAccept failed: %+v", err)
		return
	}

	smContext.SetState(smf_context.Active)

	if rspData.Cause == models.Amf_Comm_N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
		logger.PduSessLog.Warnf("%v", rspData.Cause)
	}
}

func (p *Processor) updateAnUpfPfcpSession(
	smContext *smf_context.SMContext,
	pdrList []*smf_context.PDR,
	farList []*smf_context.FAR,
	barList []*smf_context.BAR,
	qerList []*smf_context.QER,
	urrList []*smf_context.URR,
) smf_context.PFCPSessionResponseStatus {
	defaultPath := smContext.Tunnel.DataPathPool.GetDefaultPath()
	anUPF := defaultPath.FirstDPNode
	response, err := p.sendSessionModificationRequest(smContext, &PFCPState{
		upf: anUPF.UPF, pdrList: pdrList, farList: farList,
		barList: barList, qerList: qerList, urrList: urrList,
	})
	if err != nil {
		logger.PduSessLog.Warnf("Sending PFCP Session Modification Request to AN UPF error: %+v", err)
		return smf_context.SessionUpdateFailed
	}
	cause, err := response.Cause.Cause()
	if err != nil || cause != ie.CauseRequestAccepted {
		logger.PduSessLog.Warnf(
			"Received PFCP Session Modification Not Accepted Response from AN UPF: cause=%d err=%v",
			cause, err,
		)
		return smf_context.SessionUpdateFailed
	}

	logger.PduSessLog.Info("Received PFCP Session Modification Accepted Response from AN UPF")

	if smf_context.GetSelf().ULCLSupport && smContext.BPManager != nil {
		if smContext.BPManager.BPStatus == smf_context.UnInitialized {
			logger.PfcpLog.Infoln("Add PSAAndULCL")
			if err = p.AddPDUSessionAnchorAndULCL(smContext); err != nil {
				logger.PfcpLog.Error(err)
				return smf_context.SessionUpdateFailed
			}
			smContext.BPManager.BPStatus = smf_context.AddingPSA
		}
	}

	return smf_context.SessionUpdateSuccess
}

func (p *Processor) ReleaseTunnel(smContext *smf_context.SMContext) []SendPfcpResult {
	resChan := make(chan SendPfcpResult)

	deletedPfcpNode := make(map[string]bool)
	for _, dataPath := range smContext.Tunnel.DataPathPool {
		var targetNodes []*smf_context.DataPathNode
		for node := dataPath.FirstDPNode; node != nil; node = node.Next() {
			targetNodes = append(targetNodes, node)
		}
		dataPath.DeactivateTunnelAndPDR(smContext)
		for _, node := range targetNodes {
			curUPFID, err := node.GetUPFID()
			if err != nil {
				logger.PduSessLog.Error(err)
				continue
			}
			if _, exist := deletedPfcpNode[curUPFID]; !exist {
				go p.deletePfcpSession(node.UPF, smContext, resChan)
				deletedPfcpNode[curUPFID] = true
			}
		}
	}

	// collect all responses
	resList := make([]SendPfcpResult, 0, len(deletedPfcpNode))
	for i := 0; i < len(deletedPfcpNode); i++ {
		resList = append(resList, <-resChan)
	}

	return resList
}

func (p *Processor) ReleaseDcTunnel(smContext *smf_context.SMContext) []SendPfcpResult {
	resChan := make(chan SendPfcpResult)

	deletedPfcpNode := make(map[string]bool)
	for _, dataPath := range smContext.DCTunnel.DataPathPool {
		var targetNodes []*smf_context.DataPathNode
		for node := dataPath.FirstDPNode; node != nil; node = node.Next() {
			targetNodes = append(targetNodes, node)
		}
		dataPath.DeactivateDcTunnelAndPDR(smContext)
		for _, node := range targetNodes {
			curUPFID, err := node.GetUPFID()
			if err != nil {
				logger.PduSessLog.Error(err)
				continue
			}
			if _, exist := deletedPfcpNode[curUPFID]; !exist {
				go p.deletePfcpSession(node.UPF, smContext, resChan)
				deletedPfcpNode[curUPFID] = true
			}
		}
	}

	resList := make([]SendPfcpResult, 0, len(deletedPfcpNode))
	for i := 0; i < len(deletedPfcpNode); i++ {
		resList = append(resList, <-resChan)
	}

	return resList
}

func (p *Processor) deletePfcpSession(
	upf *smf_context.UPF,
	smContext *smf_context.SMContext,
	resCh chan<- SendPfcpResult,
) {
	logger.PduSessLog.Infoln("Sending PFCP Session Deletion Request")
	if upf == nil {
		resCh <- SendPfcpResult{Status: smf_context.SessionReleaseFailed, Err: fmt.Errorf("nil UPF")}
		return
	}
	associationContext, err := upf.AssociationContext()
	if err != nil {
		resCh <- SendPfcpResult{Status: smf_context.SessionReleaseFailed, Err: err}
		return
	}
	nodeIP := upf.NodeID.ResolveNodeIdToIp()
	sessionContext := smContext.PFCPContext[nodeIP.String()]
	if sessionContext == nil {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Err:    fmt.Errorf("PFCP session context for UPF %s does not exist", nodeIP),
		}
		return
	}
	if sessionContext.RemoteSEID == 0 {
		// A concurrent association-triggered release already deleted this PFCP
		// session. Treat it as idempotent success and let the caller retry only
		// the remaining live sessions.
		resCh <- SendPfcpResult{Status: smf_context.SessionReleaseSuccess}
		return
	}
	client, ok := p.getActivePFCPClient().(SessionDeletionPFCPClient)
	if !ok {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Err:    fmt.Errorf("go-pfcp Session Deletion client is not configured"),
		}
		return
	}
	request := goPfcpMessage.NewSessionDeletionRequest(0, 0, sessionContext.RemoteSEID, 0, 0)
	response, err := client.SendSessionDeletionRequest(
		associationContext, request,
		&net.UDPAddr{IP: nodeIP, Port: pfcpPeerPort},
		sessionContext.LocalSEID,
	)
	if err != nil {
		resCh <- SendPfcpResult{Status: smf_context.SessionReleaseFailed, Err: err}
		return
	}
	if response == nil || response.Cause == nil {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Err:    fmt.Errorf("PFCP Session Deletion Response is missing Cause"),
		}
		return
	}
	cause, err := response.Cause.Cause()
	if err != nil {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Err:    fmt.Errorf("decode PFCP Session Deletion Cause: %w", err),
		}
		return
	}
	if cause != ie.CauseRequestAccepted {
		resCh <- SendPfcpResult{
			Status: smf_context.SessionReleaseFailed,
			Err:    fmt.Errorf("PFCP Session Deletion rejected with Cause %d", cause),
		}
		return
	}

	logger.PduSessLog.Info("Received PFCP Session Deletion Accepted Response")
	// The UPF has already deleted the PFCP session. Clear its remote SEID before
	// notifying the caller, and consume final reports first so subsequent SM
	// context cleanup cannot race report accounting or send a duplicate delete.
	sessionContext.RemoteSEID = 0
	if len(response.UsageReport) != 0 {
		if err = smContext.HandleReports(response.UsageReport, upf.NodeID, ""); err != nil {
			logger.PduSessLog.Errorf("decode PFCP Session Deletion Usage Report: %v", err)
		}
	}
	resCh <- SendPfcpResult{Status: smf_context.SessionReleaseSuccess}
}
