package handler_test

import (
	// "net"
	"testing"

	// "github.com/free5gc/pfcp"
	// "github.com/free5gc/pfcp/pfcpType"
	// "github.com/free5gc/pfcp/pfcpUdp"
	// "github.com/free5gc/smf/internal/logger"
	// "github.com/free5gc/smf/internal/pfcp/handler"
	// "github.com/stretchr/testify/assert"
)

func TestHandlePfcpAssociationSetupRequest(t *testing.T) {
	// remoteAddr := &net.UDPAddr{
	// 	IP:   net.ParseIP("192.168.1.1"),
	// 	Port: 12345,
	// }

	// testPfcpReq := &pfcp.Message{
	// 	Header: pfcp.Header{
	// 		Version:         1,
	// 		MP:              0,
	// 		S:               0,
	// 		MessageType:     pfcp.PFCP_ASSOCIATION_SETUP_REQUEST,
	// 		MessageLength:   9,
	// 		SEID:            0,
	// 		SequenceNumber:  1,
	// 		MessagePriority: 0,
	// 	},
	// 	Body: pfcp.PFCPAssociationSetupRequest{
	// 		NodeID: &pfcpType.NodeID{
	// 			NodeIdType: 0,
	// 			IP:         net.ParseIP("192.168.1.1").To4(),
	// 		},
	// 	},
	// }
	// msg := pfcpUdp.NewMessage(remoteAddr, testPfcpReq)
	// mockLogger := &logger.MockLogger{}
	// handler.HandlePfcpAssociationSetupRequest(msg)
	// assert.Equal(t, true, mockLogger.ErrorfInvoked)
}

// func TestHandlePfcpAssociationReleaseRequest(t *testing.T) {
// }
