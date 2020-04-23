package message_test

import (
	"free5gc/src/smf/context"
	"net"
	"testing"
	"time"

	// "free5gc/lib/pfcp/pfcpType"
	"free5gc/lib/pfcp/pfcpUdp"
	"free5gc/src/smf/pfcp/message"
	"free5gc/src/smf/pfcp/pfcp_udp"
)

var testAddr *net.UDPAddr

// Adjust waiting time in millisecond if PFCP packets are not captured
var testWaitingTime int = 500

var dummyContext *context.SMContext

func init() {
	smfContext := context.SMF_Self()

	smfContext.CPNodeID.NodeIdType = 0
	smfContext.CPNodeID.NodeIdValue = net.ParseIP("127.0.0.1").To4()

	pfcp_udp.Run()

	testAddr = &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: pfcpUdp.PFCP_PORT,
	}
	dummyContext = context.NewSMContext("imsi-20893000000001", 3)

}

func TestSendPfcpAssociationSetupRequest(t *testing.T) {
	message.SendPfcpAssociationSetupRequest(testAddr)
	time.Sleep(1000 * time.Millisecond)
}

func TestSendPfcpSessionEstablishmentResponse(t *testing.T) {
	message.SendPfcpSessionEstablishmentResponse(testAddr)
	time.Sleep(1000 * time.Millisecond)
}

func TestSendPfcpSessionEstablishmentRequest(t *testing.T) {
	message.SendPfcpSessionEstablishmentRequest(testAddr, dummyContext)
	time.Sleep(time.Duration(testWaitingTime) * time.Millisecond)
}

// func TestSendPfcpAssociationSetupResponse(t *testing.T) {
// 	cause := pfcpType.Cause{
// 		CauseValue: pfcpType.CauseRequestAccepted,
// 	}
// 	message.SendPfcpAssociationSetupResponse(testAddr, cause)
// 	time.Sleep(1000 * time.Millisecond)
// }

// func TestSendPfcpAssociationReleaseRequest(t *testing.T) {
// 	message.SendPfcpAssociationReleaseRequest(testAddr)
// 	time.Sleep(1000 * time.Millisecond)
// }

// func TestSendPfcpAssociationReleaseResponse(t *testing.T) {
// 	cause := pfcpType.Cause{
// 		CauseValue: pfcpType.CauseRequestAccepted,
// 	}
// 	message.SendPfcpAssociationReleaseResponse(testAddr, cause)
// 	time.Sleep(1000 * time.Millisecond)
// }

// func TestSendPfcpSessionEstablishmentResponse(t *testing.T) {
// 	message.SendPfcpSessionEstablishmentResponse(testAddr)
// 	time.Sleep(1000 * time.Millisecond)
// }

// func TestSendPfcpSessionModificationRequest(t *testing.T) {
// 	message.SendPfcpSessionModificationRequest(testAddr, nil, nil, nil, nil)
// 	time.Sleep(1000 * time.Millisecond)
// }

// func TestSendPfcpSessionModificationResponse(t *testing.T) {
// 	message.SendPfcpSessionModificationResponse(testAddr)
// 	time.Sleep(1000 * time.Millisecond)
// }

// func TestSendPfcpSessionDeletionRequest(t *testing.T) {
// 	message.SendPfcpSessionDeletionRequest(testAddr)
// 	time.Sleep(1000 * time.Millisecond)
// }

// func TestSendPfcpSessionDeletionResponse(t *testing.T) {
// 	message.SendPfcpSessionDeletionResponse(testAddr)
// 	time.Sleep(1000 * time.Millisecond)
// }
