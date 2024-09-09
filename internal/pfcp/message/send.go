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
	logger.PfcpLog.Tracef("Build AssociationSetupRequest to UPF [%s]", addr)
	pfcpMsg, err := BuildPfcpAssociationSetupRequest()
	if err != nil {
		return nil, fmt.Errorf("Build PFCP Association Setup Request failed: %v", err)
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
		logger.PfcpLog.Errorf("Build PFCP Association Setup Response failed: %v", err)
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
		logger.PfcpLog.Errorf("Build PFCP Association Release Request failed: %v", err)
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

	pfcpMsg, err := BuildPfcpSessionEstablishmentRequest(pfcpContext)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Establishment Request failed: %v", err)
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
		return nil, fmt.Errorf("UPF[%s] is not associated, do not send SessionEstablishmentRequest", nodeID)
	default:
	}

	logger.PduSessLog.Tracef("[SMF] Send SendPfcpSessionEstablishmentRequest to addr %s", upaddr)

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

// Deprecated: PFCP Session Establishment Procedure should be initiated by the CP function
func SendPfcpSessionEstablishmentResponse(addr *net.UDPAddr) {
	pfcpMsg, err := BuildPfcpSessionEstablishmentResponse()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Establishment Response failed: %v", err)
		return
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_ESTABLISHMENT_RESPONSE,
			SEID:            123456789123456789,
			SequenceNumber:  1,
			MessagePriority: 12,
		},
		Body: pfcpMsg,
	}

	udp.SendPfcpResponse(message, addr)
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

// Deprecated: PFCP Session Modification Procedure should be initiated by the CP function
func SendPfcpSessionModificationResponse(addr *net.UDPAddr) {
	pfcpMsg, err := BuildPfcpSessionModificationResponse()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Modification Response failed: %v", err)
		return
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_MODIFICATION_RESPONSE,
			SEID:            123456789123456789,
			SequenceNumber:  1,
			MessagePriority: 12,
		},
		Body: pfcpMsg,
	}

	udp.SendPfcpResponse(message, addr)
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
	default:
	}

	logger.PfcpLog.Tracef("Build PFCP Session Release Request to UPF[%s]", nodeID)

	pfcpMsg, err := BuildPfcpSessionDeletionRequest()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Release Request failed: %v", err)
		return
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

// Deprecated: PFCP Session Deletion Procedure should be initiated by the CP function
func SendPfcpSessionDeletionResponse(addr *net.UDPAddr) {
	pfcpMsg, err := BuildPfcpSessionDeletionResponse()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Deletion Response failed: %v", err)
		return
	}

	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_DELETION_RESPONSE,
			SEID:            123456789123456789,
			SequenceNumber:  1,
			MessagePriority: 12,
		},
		Body: pfcpMsg,
	}

	udp.SendPfcpResponse(message, addr)
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
		return
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

func SendPfcpSessionReportResponse(
	addr *net.UDPAddr,
	cause pfcpType.Cause,
	seqFromUPF uint32,
	seid uint64,
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
			SEID:           seid,
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
