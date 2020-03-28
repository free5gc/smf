package pfcp_message

import (
	"fmt"
	"gofree5gc/lib/pfcp"
	"gofree5gc/lib/pfcp/pfcpType"
	"gofree5gc/src/smf/logger"
	"gofree5gc/src/smf/smf_context"
	"gofree5gc/src/smf/smf_pfcp/pfcp_udp"
	"net"
)

var seq uint32

func getSeqNumber() uint32 {
	seq++
	return seq
}

func SendPfcpAssociationSetupRequest(addr *net.UDPAddr) {
	pfcpMsg, err := BuildPfcpAssociationSetupRequest()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Association Setup Request failed: %v", err)
		return
	}

	message := pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_SETUP_REQUEST,
			SequenceNumber: getSeqNumber(),
		},
		Body: pfcpMsg,
	}

	pfcp_udp.SendPfcp(message, addr)
}

func SendPfcpAssociationSetupResponse(addr *net.UDPAddr, cause pfcpType.Cause) {
	pfcpMsg, err := BuildPfcpAssociationSetupResponse(cause)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Association Setup Response failed: %v", err)
		return
	}

	message := pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_SETUP_RESPONSE,
			SequenceNumber: 1,
		},
		Body: pfcpMsg,
	}

	pfcp_udp.SendPfcp(message, addr)
}

func SendPfcpAssociationReleaseRequest(addr *net.UDPAddr) {
	pfcpMsg, err := BuildPfcpAssociationReleaseRequest()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Association Release Request failed: %v", err)
		return
	}

	message := pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_RELEASE_REQUEST,
			SequenceNumber: 1,
		},
		Body: pfcpMsg,
	}

	pfcp_udp.SendPfcp(message, addr)
}

// Deprecated: PFCP Association Release Procedure should be initiated by the CP function
func SendPfcpAssociationReleaseResponse(addr *net.UDPAddr, cause pfcpType.Cause) {
	pfcpMsg, err := BuildPfcpAssociationReleaseResponse(cause)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Association Release Response failed: %v", err)
		return
	}

	message := pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_RELEASE_RESPONSE,
			SequenceNumber: 1,
		},
		Body: pfcpMsg,
	}

	pfcp_udp.SendPfcp(message, addr)
}

func SendPfcpSessionEstablishmentRequest(raddr *net.UDPAddr, ctx *smf_context.SMContext) {
	pfcpMsg, err := BuildPfcpSessionEstablishmentRequest(ctx)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Establishment Request failed: %v", err)
		return
	}

	message := pfcp.Message{
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

	pfcp_udp.SendPfcp(message, raddr)
}

func SendPfcpSessionEstablishmentRequestForULCL(raddr *net.UDPAddr, ctx *smf_context.SMContext, pdrList []*smf_context.PDR, farList []*smf_context.FAR, barList []*smf_context.BAR) {
	pfcpMsg, err := BuildPfcpSessionEstablishmentRequestForULCL(ctx, pdrList, farList, barList)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Establishment Request failed: %v", err)
		return
	}

	logger.PduSessLog.Traceln("PFCP MSG: ")
	logger.PduSessLog.Traceln(pfcpMsg)
	fmt.Println("PFCP MSG: ")
	fmt.Println(pfcpMsg)
	message := pfcp.Message{
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

	logger.PduSessLog.Traceln("[SMF] Send SendPfcpSessionEstablishmentRequestForULCL")
	logger.PduSessLog.Traceln("Send to addr ", raddr.String())
	fmt.Println("[SMF] Send SendPfcpSessionEstablishmentRequestForULCL")
	fmt.Println("Send to addr ", raddr.String())
	pfcp_udp.SendPfcp(message, raddr)
}

// Deprecated: PFCP Session Establishment Procedure should be initiated by the CP function
func SendPfcpSessionEstablishmentResponse(addr *net.UDPAddr) {
	pfcpMsg, err := BuildPfcpSessionEstablishmentResponse()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Establishment Response failed: %v", err)
		return
	}

	message := pfcp.Message{
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

	pfcp_udp.SendPfcp(message, addr)
}

func SendPfcpSessionModificationRequest(raddr *net.UDPAddr, ctx *smf_context.SMContext, pdrList []*smf_context.PDR, farList []*smf_context.FAR, barList []*smf_context.BAR) (seqNum uint32) {

	pfcpMsg, err := BuildPfcpSessionModificationRequest(ctx, pdrList, farList, barList)

	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Modification Request failed: %v", err)
		return
	}

	seqNum = getSeqNumber()
	message := pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_MODIFICATION_REQUEST,
			SEID:            ctx.RemoteSEID,
			SequenceNumber:  seqNum,
			MessagePriority: 12,
		},
		Body: pfcpMsg,
	}

	pfcp_udp.SendPfcp(message, raddr)
	return seqNum
}

// Deprecated: PFCP Session Modification Procedure should be initiated by the CP function
func SendPfcpSessionModificationResponse(addr *net.UDPAddr) {
	pfcpMsg, err := BuildPfcpSessionModificationResponse()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Modification Response failed: %v", err)
		return
	}

	message := pfcp.Message{
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

	pfcp_udp.SendPfcp(message, addr)
}

func SendPfcpSessionDeletionRequest(addr *net.UDPAddr, ctx *smf_context.SMContext) (seqNum uint32) {
	pfcpMsg, err := BuildPfcpSessionDeletionRequest()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Deletion Request failed: %v", err)
		return
	}
	seqNum = getSeqNumber()
	message := pfcp.Message{
		Header: pfcp.Header{
			Version:         pfcp.PfcpVersion,
			MP:              1,
			S:               pfcp.SEID_PRESENT,
			MessageType:     pfcp.PFCP_SESSION_DELETION_REQUEST,
			SEID:            ctx.RemoteSEID,
			SequenceNumber:  getSeqNumber(),
			MessagePriority: 12,
		},
		Body: pfcpMsg,
	}

	pfcp_udp.SendPfcp(message, addr)

	return seqNum
}

// Deprecated: PFCP Session Deletion Procedure should be initiated by the CP function
func SendPfcpSessionDeletionResponse(addr *net.UDPAddr) {
	pfcpMsg, err := BuildPfcpSessionDeletionResponse()
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Deletion Response failed: %v", err)
		return
	}

	message := pfcp.Message{
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

	pfcp_udp.SendPfcp(message, addr)
}

func SendPfcpSessionReportResponse(addr *net.UDPAddr, cause pfcpType.Cause, seqFromUPF uint32, SEID uint64) {
	pfcpMsg, err := BuildPfcpSessionReportResponse(cause)
	if err != nil {
		logger.PfcpLog.Errorf("Build PFCP Session Report Response failed: %v", err)
		return
	}

	message := pfcp.Message{
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

	pfcp_udp.SendPfcp(message, addr)
}
