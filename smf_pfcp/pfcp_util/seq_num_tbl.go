package pfcp_util

import (
	"fmt"
	//"gofree5gc/src/smf/smf_pfcp/pfcp_udp"
	"gofree5gc/lib/pfcp"
	"gofree5gc/src/smf/logger"
)

type SeqNumTblItem struct {
	PacketState PacketState
	MessageType pfcp.MessageType
}

type SeqNumTbl struct {
	from_smf_seq_tbl map[uint32]*SeqNumTblItem
	to_smf_seq_tbl   map[uint32]*SeqNumTblItem
}

func NewSeqNumTbl() *SeqNumTbl {
	var snt SeqNumTbl
	snt.from_smf_seq_tbl = make(map[uint32]*SeqNumTblItem)
	snt.to_smf_seq_tbl = make(map[uint32]*SeqNumTblItem)
	return &snt
}

func (snt SeqNumTbl) RecvCheckAndPutItem(msg *pfcp.Message) (Success bool) {

	Item := new(SeqNumTblItem)
	seqNum := msg.Header.SequenceNumber
	Success = false
	switch msg.Header.MessageType {
	case pfcp.PFCP_HEARTBEAT_REQUEST:
		logger.PfcpLog.Warnf("PFCP Heartbeat Request handling is not implemented")
	case pfcp.PFCP_HEARTBEAT_RESPONSE:
		logger.PfcpLog.Warnf("PFCP Heartbeat Response handling is not implemented")
	case pfcp.PFCP_PFD_MANAGEMENT_REQUEST:
		logger.PfcpLog.Warnf("PFCP PFD Management Request handling is not implemented")
	case pfcp.PFCP_PFD_MANAGEMENT_RESPONSE:
		logger.PfcpLog.Warnf("PFCP PFD Management Response handling is not implemented")
	case pfcp.PFCP_ASSOCIATION_SETUP_REQUEST:
		Item.PacketState = RECV_REQUEST
		Item.MessageType = pfcp.PFCP_ASSOCIATION_SETUP_REQUEST
	case pfcp.PFCP_ASSOCIATION_SETUP_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(RECV_RESPONSE))
		return
	case pfcp.PFCP_ASSOCIATION_UPDATE_REQUEST:
		Item.PacketState = RECV_REQUEST
		Item.MessageType = pfcp.PFCP_ASSOCIATION_SETUP_RESPONSE
	case pfcp.PFCP_ASSOCIATION_UPDATE_RESPONSE:
		logger.PfcpLog.Warnf("PFCP Association Update Response handling is not implemented")
	case pfcp.PFCP_ASSOCIATION_RELEASE_REQUEST:
		Item.PacketState = RECV_REQUEST
		Item.MessageType = pfcp.PFCP_ASSOCIATION_RELEASE_REQUEST
	case pfcp.PFCP_ASSOCIATION_RELEASE_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(RECV_RESPONSE))
		return
	case pfcp.PFCP_VERSION_NOT_SUPPORTED_RESPONSE:
		logger.PfcpLog.Warnf("PFCP Version Not Support Response handling is not implemented")
	case pfcp.PFCP_NODE_REPORT_REQUEST:
		logger.PfcpLog.Warnf("PFCP Node Report Request handling is not implemented")
	case pfcp.PFCP_NODE_REPORT_RESPONSE:
		logger.PfcpLog.Warnf("PFCP Node Report Response handling is not implemented")
	case pfcp.PFCP_SESSION_SET_DELETION_REQUEST:
		logger.PfcpLog.Warnf("PFCP Session Set Deletion Request handling is not implemented")
	case pfcp.PFCP_SESSION_SET_DELETION_RESPONSE:
		logger.PfcpLog.Warnf("PFCP Session Set Deletion Response handling is not implemented")
	case pfcp.PFCP_SESSION_ESTABLISHMENT_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(RECV_RESPONSE))
		return
	case pfcp.PFCP_SESSION_MODIFICATION_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(RECV_RESPONSE))
		return
	case pfcp.PFCP_SESSION_DELETION_RESPONSE:
		logger.PfcpLog.Warnf("PFCP Session Deletion Response handling is not implemented")
	case pfcp.PFCP_SESSION_REPORT_REQUEST:
		logger.PfcpLog.Warnf("PFCP Session Report Response handling is not implemented")
	case pfcp.PFCP_SESSION_REPORT_RESPONSE:
		logger.PfcpLog.Warnf("PFCP Session Report Response handling is not implemented")
	default:
		logger.PfcpLog.Errorf("Unknown PFCP message type: %d", msg.Header.MessageType)
		return

	}

	_, exist := snt.to_smf_seq_tbl[seqNum]
	if !exist {
		snt.to_smf_seq_tbl[seqNum] = Item
		Success = true
	} else {
		logger.PfcpLog.Errorf("\n[SMF PFCP]Sequence Number %d already exists.\n", seqNum)
		logger.PfcpLog.Errorf("\n[SMF PFCP]Message Type %d\n", Item.MessageType)
	}

	return
}

func (snt SeqNumTbl) SendCheckAndPutItem(msg *pfcp.Message) (Success bool) {
	Item := new(SeqNumTblItem)
	seqNum := msg.Header.SequenceNumber
	Success = false

	switch msg.Header.MessageType {
	case pfcp.PFCP_ASSOCIATION_SETUP_REQUEST:
		Item.PacketState = SEND_REQUEST
		Item.MessageType = pfcp.PFCP_ASSOCIATION_SETUP_REQUEST
	case pfcp.PFCP_ASSOCIATION_SETUP_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(SEND_RESPONSE))
		return
	case pfcp.PFCP_ASSOCIATION_RELEASE_REQUEST:
		Item.PacketState = SEND_REQUEST
		Item.MessageType = pfcp.PFCP_ASSOCIATION_RELEASE_REQUEST
	case pfcp.PFCP_ASSOCIATION_RELEASE_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(SEND_RESPONSE))
		return
	case pfcp.PFCP_SESSION_ESTABLISHMENT_REQUEST:
		Item.PacketState = SEND_REQUEST
		Item.MessageType = pfcp.PFCP_SESSION_ESTABLISHMENT_REQUEST
	case pfcp.PFCP_SESSION_ESTABLISHMENT_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(SEND_RESPONSE))
		return
	case pfcp.PFCP_SESSION_MODIFICATION_REQUEST:
		Item.PacketState = SEND_REQUEST
		Item.MessageType = pfcp.PFCP_SESSION_MODIFICATION_REQUEST
	case pfcp.PFCP_SESSION_MODIFICATION_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(SEND_RESPONSE))
		return
	case pfcp.PFCP_SESSION_DELETION_REQUEST:
		Item.PacketState = SEND_REQUEST
		Item.MessageType = pfcp.PFCP_SESSION_DELETION_REQUEST
	case pfcp.PFCP_SESSION_DELETION_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(SEND_RESPONSE))
		return
	case pfcp.PFCP_SESSION_REPORT_RESPONSE:
		Success = snt.RemoveItem(seqNum, uint8(SEND_RESPONSE))
		return
	default:
		logger.PfcpLog.Errorf("\nUnknown PFCP message type: %d\n", msg.Header.MessageType)
		return

	}

	_, exist := snt.from_smf_seq_tbl[seqNum]
	if !exist {
		snt.from_smf_seq_tbl[seqNum] = Item
		Success = true
	} else {
		logger.PfcpLog.Errorf("\n[SMF PFCP]Sequence Number %d already exists.\n", seqNum)
		logger.PfcpLog.Errorf("\n[SMF PFCP]Message Type %d\n", Item.MessageType)
	}

	return
}

func (snt SeqNumTbl) RemoveItem(seqNum uint32, newStateInInt uint8) (Success bool) {
	newState := PacketState(newStateInInt)
	Success = false

	var item *SeqNumTblItem
	var exist bool

	if newState == SEND_RESPONSE {
		item, exist = snt.to_smf_seq_tbl[seqNum]

		if !exist {
			logger.PfcpLog.Warnf("\n[SMF PFCP] Can't send response without having corresponding request.\n")
			logger.PfcpLog.Warnf("\n[SMF PFCP] Packet sequence number: %d\n", seqNum)
			return
		}
	} else if newState == RECV_RESPONSE {
		item, exist = snt.from_smf_seq_tbl[seqNum]

		if !exist {
			logger.PfcpLog.Warnf("\n[SMF PFCP] Can't receive response without having corresponding request.\n")
			logger.PfcpLog.Warnf("\n[SMF PFCP] Packet sequence number: %d\n", seqNum)
			return
		}
	}

	switch newState {
	case SEND_RESPONSE:
		if item.PacketState != RECV_REQUEST {
			logger.PfcpLog.Warnf("\n[SMF PFCP] Wrong Packet State when sending response.\n")
			logger.PfcpLog.Warnf("\n[SMF PFCP] Respone message type %d\n", item.MessageType)
			logger.PfcpLog.Warnf("\n[SMF PFCP] Packet State: %d\n", item.PacketState)
			logger.PfcpLog.Warnf("\n[SMF PFCP] Packet sequence number: %d\n", seqNum)
			return
		}

		delete(snt.to_smf_seq_tbl, seqNum)
		Success = true
	case RECV_RESPONSE:
		if item.PacketState != SEND_REQUEST {
			logger.PfcpLog.Warnf("\n[SMF PFCP] Wrong Packet State when receiving response.\n")
			logger.PfcpLog.Warnf("\n[SMF PFCP] Respone message type %d\n", item.MessageType)
			logger.PfcpLog.Warnf("\n[SMF PFCP] Packet State: %d\n", item.PacketState)
			logger.PfcpLog.Warnf("\n[SMF PFCP] Packet sequence number: %d\n", seqNum)
			return
		}

		delete(snt.from_smf_seq_tbl, seqNum)
		Success = true
	default:
		logger.PfcpLog.Errorf("\nWrong Packet State: %d\n", newState)
	}

	return
}

func (snt SeqNumTbl) PrintTable() {
	fmt.Println("Table from smf")
	fmt.Printf("Seq Num\tMsg Type\tPacket State\n")
	for seqNum, item := range snt.from_smf_seq_tbl {
		fmt.Printf("%d\t%d\t%d\n", seqNum, item.MessageType, item.PacketState)
	}

	fmt.Println("Table to smf")
	fmt.Printf("Seq Num\tMsg Type\tPacket State\n")
	for seqNum, item := range snt.to_smf_seq_tbl {
		fmt.Printf("%d\t%d\t%d\n", seqNum, item.MessageType, item.PacketState)
	}
}
