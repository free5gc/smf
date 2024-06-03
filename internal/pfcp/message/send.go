package message

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/pfcp/pfcpUdp"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/udp"
)

var seq uint32

func getSeqNumber() uint32 {
	return atomic.AddUint32(&seq, 1)
}

func SendPfcpAssociationSetupRequest(addr *net.UDPAddr) (resMsg *pfcpUdp.Message, err error) {
	logger.PfcpLog.Tracef("Build AssociationSetupRequest to UPF [%s]", addr.String())
	pfcpMsg, err := BuildPfcpAssociationSetupRequest()
	if err != nil {
		return nil, fmt.Errorf("build PFCP Association Setup Request failed: %v", err)
	}
	logger.PfcpLog.Tracef("Create PFCP message to UPF [%s]", addr.String())

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_SETUP_REQUEST,
			SequenceNumber: getSeqNumber(),
		},
		Body: pfcpMsg,
	}

	logger.PfcpLog.Tracef("Send AssociationSetupRequest to UPF [%s]", addr.String())
	resMsg, err = udp.SendPfcpRequest(message, addr, context.Background())
	// pass empty context as association is not established yet

	if err != nil {
		return nil, err
	}

	logger.PfcpLog.Tracef("Returned from send AssociationSetupRequest to UPF [%s]", addr.String())

	if resMsg.MessageType() != pfcp.PFCP_ASSOCIATION_SETUP_RESPONSE {
		return resMsg, fmt.Errorf("received unexpected response message")
	}

	return resMsg, nil
}

func SendPfcpAssociationSetupResponse(addr *net.UDPAddr, cause pfcpType.Cause) {
	pfcpMsg, err := BuildPfcpAssociationSetupResponse(cause)
	if err != nil {
		logger.PfcpLog.Errorf("build PFCP Association Setup Response failed: %v", err)
		return
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_SETUP_RESPONSE,
			SequenceNumber: 1,
		},
		Body: pfcpMsg,
	}

	udp.SendPfcpResponse(message, addr)
}

func SendPfcpAssociationReleaseRequest(addr *net.UDPAddr) (resMsg *pfcpUdp.Message, err error) {
	pfcpMsg, err := BuildPfcpAssociationReleaseRequest()
	if err != nil {
		logger.PfcpLog.Errorf("build PFCP Association Release Request failed: %v", err)
		return nil, err
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_RELEASE_REQUEST,
			SequenceNumber: 1,
		},
		Body: pfcpMsg,
	}

	resMsg, err = udp.SendPfcpRequest(message, addr, context.Background())

	if err != nil {
		return nil, err
	}

	if resMsg.MessageType() != pfcp.PFCP_ASSOCIATION_RELEASE_RESPONSE {
		return resMsg, fmt.Errorf("received unexpected response message")
	}

	return resMsg, nil
}

func SendPfcpAssociationReleaseResponse(addr *net.UDPAddr, cause pfcpType.Cause) {
	pfcpMsg, err := BuildPfcpAssociationReleaseResponse(cause)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Association Release Response failed: %v", err)
		return
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_RELEASE_RESPONSE,
			SequenceNumber: 1,
		},
		Body: pfcpMsg,
	}

	udp.SendPfcpResponse(message, addr)
}

func SendPfcpSessionEstablishmentRequest(
	pfcpContext *smf_context.PFCPSessionContext,
) (resMsg *pfcpUdp.Message, err error) {
	upf := pfcpContext.UPF
	nodeID := upf.GetNodeIDString()
	localSEID := pfcpContext.LocalSEID

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not build SessionEstablishmentRequest", nodeID)
	default:
	}

	logger.PfcpLog.Tracef("Build PFCP Session Establishment Request to UPF[%s]", nodeID)

	pfcpMsg, _ := BuildPfcpSessionEstablishmentRequest(pfcpContext)

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_ESTABLISHMENT_REQUEST,
			SEID:            0,
			SequenceNumber:  getSeqNumber(),
			MessagePriority: 0,
		},
		Body: pfcpMsg,
	}

	upaddr := upf.PFCPAddr()

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not send SessionEstablishmentRequest", nodeID)
	default:
	}

	resMsg, err = udp.SendPfcpRequest(message, upaddr, upf.Association)
	if err != nil {
		return nil, err
	}

	if resMsg.MessageType() != pfcp.PFCP_SESSION_ESTABLISHMENT_RESPONSE {
		return resMsg, fmt.Errorf("received unexpected type response message: %+v", resMsg.PfcpMessage.Header)
	}

	if resMsg.PfcpMessage.Header.SEID != localSEID {
		return resMsg, fmt.Errorf("received unexpected SEID response message: %+v, exptcted: %d",
			resMsg.PfcpMessage.Header, localSEID)
	}

	return resMsg, nil
}

func SendPfcpSessionModificationRequest(
	pfcpContext *smf_context.PFCPSessionContext,
) (resMsg *pfcpUdp.Message, err error) {
	upf := pfcpContext.UPF
	nodeID := upf.GetNodeIDString()
	localSEID := pfcpContext.LocalSEID
	remoteSEID := pfcpContext.RemoteSEID

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not build SessionModificationRequest", nodeID)
	default:
	}

	logger.PfcpLog.Tracef("Build PFCP Session Mofification Request to UPF[%s]", nodeID)

	pfcpMsg, err := BuildPfcpSessionModificationRequest(pfcpContext)

	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Modification Request failed: %v", err)
		return nil, err
	}

	seqNum := getSeqNumber()
	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_MODIFICATION_REQUEST,
			SEID:            remoteSEID,
			SequenceNumber:  seqNum,
			MessagePriority: 12,
		},
		Body: pfcpMsg,
	}

	upaddr := upf.PFCPAddr()

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not send SessionModificationRequest", nodeID)
	default:
	}

	resMsg, err = udp.SendPfcpRequest(message, upaddr, upf.Association)

	if err != nil {
		return nil, err
	}

	if resMsg.MessageType() != pfcp.PFCP_SESSION_MODIFICATION_RESPONSE {
		return resMsg, fmt.Errorf("received unexpected type response message: %+v", resMsg.PfcpMessage.Header)
	}

	if resMsg.PfcpMessage.Header.SEID != localSEID {
		return resMsg, fmt.Errorf("received unexpected SEID response message: %+v, exptcted: %d",
			resMsg.PfcpMessage.Header, localSEID)
	}

	return resMsg, nil
}

func SendPfcpSessionReleaseRequest(
	pfcpContext *smf_context.PFCPSessionContext,
) (resMsg *pfcpUdp.Message, err error) {
	upf := pfcpContext.UPF
	nodeID := upf.GetNodeIDString()
	localSEID := pfcpContext.LocalSEID
	remoteSEID := pfcpContext.RemoteSEID

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not build SessionReleaseRequest", nodeID)
	//case <-upf.SessionManagement.Done():
	//	return nil, fmt.Errorf("UPF[%s] canceled session management, do not build SessionReleaseRequest", nodeID)
	default:
	}

	logger.PfcpLog.Tracef("Build PFCP Session Release Request to UPF[%s]", nodeID)

	pfcpMsg, err := BuildPfcpSessionDeletionRequest()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Release Request failed: %v", err)
		return nil, err
	}
	seqNum := getSeqNumber()
	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_DELETION_REQUEST,
			SEID:            remoteSEID,
			SequenceNumber:  seqNum,
			MessagePriority: 12,
		},
		Body: pfcpMsg,
	}

	upaddr := upf.PFCPAddr()

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not send SessionReleaseRequest", nodeID)
	//case <-upf.SessionManagement.Done():
	//	return nil, fmt.Errorf("UPF[%s] canceled session management, do not send SessionReleaseRequest", nodeID)
	default:
	}

	resMsg, err = udp.SendPfcpRequest(message, upaddr, upf.Association)
	if err != nil {
		return nil, err
	}

	if resMsg.MessageType() != pfcp.PFCP_SESSION_DELETION_RESPONSE {
		return resMsg, fmt.Errorf("received unexpected type response message: %+v", resMsg.PfcpMessage.Header)
	}

	if resMsg.PfcpMessage.Header.SEID != localSEID {
		return resMsg, fmt.Errorf("received unexpected SEID response message: %+v, exptcted: %d",
			resMsg.PfcpMessage.Header, localSEID)
	}

	return resMsg, nil
}

func SendPfcpSessionRecoveryRequest(
	pfcpContext *smf_context.PFCPSessionContext,
) (resMsg *pfcpUdp.Message, err error) {
	upf := pfcpContext.UPF
	nodeID := upf.GetNodeIDString()
	localSEID := pfcpContext.LocalSEID

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not build SessionRecoveryRequest", nodeID)
	default:
	}

	logger.PfcpLog.Tracef("Build PFCP Session Recovery Request to UPF[%s]", nodeID)

	pfcpMsg, err := BuildPfcpSessionRecoveryRequest(pfcpContext)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Recovery Request failed: %v", err)
		return nil, err
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_ESTABLISHMENT_REQUEST,
			SEID:            0,
			SequenceNumber:  getSeqNumber(),
			MessagePriority: 0,
		},
		Body: pfcpMsg,
	}

	upaddr := upf.PFCPAddr()

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not send SessionRecoveryRequest", nodeID)
	default:
	}

	resMsg, err = udp.SendPfcpRequest(message, upaddr, upf.Association)
	if err != nil {
		return nil, err
	}

	if resMsg.MessageType() != pfcp.PFCP_SESSION_ESTABLISHMENT_RESPONSE {
		return resMsg, fmt.Errorf("received unexpected type response message: %+v", resMsg.PfcpMessage.Header)
	}

	if resMsg.PfcpMessage.Header.SEID != localSEID {
		return resMsg, fmt.Errorf("received unexpected SEID response message: %+v, exptcted: %d",
			resMsg.PfcpMessage.Header, localSEID)
	}

	return resMsg, nil
}

func SendPfcpSessionDeletionRequest(
	upf *smf_context.UPF,
	smContext *smf_context.SMContext,
) (resMsg *pfcpUdp.Message, err error) {
	nodeID := upf.GetNodeIDString()
	localSEID := smContext.PFCPSessionContexts[upf.GetID()].LocalSEID
	remoteSEID := smContext.PFCPSessionContexts[upf.GetID()].RemoteSEID

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not build SessionModificationRequest", nodeID)
	default:
	}

	logger.PfcpLog.Tracef("Build PFCP Session Deletion Request to UPF[%s]", nodeID)

	pfcpMsg, err := BuildPfcpSessionDeletionRequest()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Deletion Request failed: %v", err)
		return nil, err
	}

	seqNum := getSeqNumber()
	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_DELETION_REQUEST,
			SEID:            remoteSEID,
			SequenceNumber:  seqNum,
			MessagePriority: 12,
		},
		Body: pfcpMsg,
	}

	upaddr := upf.PFCPAddr()

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not send SessionModificationRequest", nodeID)
	default:
	}

	resMsg, err = udp.SendPfcpRequest(message, upaddr, upf.Association)
	if err != nil {
		return nil, err
	}

	if resMsg.MessageType() != pfcp.PFCP_SESSION_DELETION_RESPONSE {
		return resMsg, fmt.Errorf("received unexpected type response message: %+v", resMsg.PfcpMessage.Header)
	}

	if resMsg.PfcpMessage.Header.SEID != localSEID {
		return resMsg, fmt.Errorf("received unexpected SEID response message: %+v, exptcted: %d",
			resMsg.PfcpMessage.Header, localSEID)
	}

	return resMsg, nil
}

func SendPfcpSessionReportResponse(
	addr *net.UDPAddr,
	cause pfcpType.Cause,
	seqFromUPF uint32,
	SEID uint64,
) {
	pfcpMsg, err := BuildPfcpSessionReportResponse(cause)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Report Response failed: %v", err)
		return
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_PRESENT,
			MessageType:    pfcp.PFCP_SESSION_REPORT_RESPONSE,
			SequenceNumber: seqFromUPF,
			SEID:           SEID,
		},
		Body: pfcpMsg,
	}

	udp.SendPfcpResponse(message, addr)
}

func SendPfcpHeartbeatRequest(upf *smf_context.UPF) (resMsg *pfcpUdp.Message, err error) {
	nodeID := upf.GetNodeIDString()

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not build HeartbeatRequest", nodeID)
	default:
	}

	pfcpMsg, err := BuildPfcpHeartbeatRequest()
	if err != nil {
		return nil, fmt.Errorf("BuildPFCPHeartbeatRequest failed: %w", err)
	}

	reqMsg := &pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_HEARTBEAT_REQUEST,
			SequenceNumber: getSeqNumber(),
		},
		Body: pfcpMsg,
	}

	upfAddr := upf.PFCPAddr()

	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] is not associated, do not send HeartbeatRequest", nodeID)
	default:
	}

	resMsg, err = udp.SendPfcpRequest(reqMsg, upfAddr, upf.Association)
	if err != nil {
		return nil, err
	}

	if resMsg == nil {
		// occurs when the heartbeat miss tolerance is not yet reached
		// or when a PFCP message was sent to a no longer associated UPF
		return nil, nil
	}

	if resMsg.MessageType() != pfcp.PFCP_HEARTBEAT_RESPONSE {
		return resMsg, fmt.Errorf("received unexpected response message")
	}

	return resMsg, nil
}

func SendHeartbeatResponse(addr *net.UDPAddr, seq uint32) {
	pfcpMsg := pfcp.HeartbeatResponse{
		RecoveryTimeStamp: &pfcpType.RecoveryTimeStamp{
			RecoveryTimeStamp: udp.ServerStartTime,
		},
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_HEARTBEAT_RESPONSE,
			SequenceNumber: seq,
		},
		Body: pfcpMsg,
	}

	udp.SendPfcpResponse(message, addr)
}
