package udp_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/pfcp/pfcpUdp"
	smf_context "github.com/free5gc/smf/internal/context"
	smf_pfcp "github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/internal/pfcp/udp"
)

const testPfcpClientPort = 12345

func TestRun(t *testing.T) {
	// Set SMF Node ID

	smfContext := smf_context.GetSelf()

	smfContext.CPNodeID = pfcpType.NodeID{
		NodeIdType: pfcpType.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("127.0.0.1").To4(),
	}
	smfContext.ExternalAddr = "127.0.0.1"
	smfContext.ListenAddr = "127.0.0.1"

	smfContext.PfcpContext, smfContext.PfcpCancelFunc = context.WithCancel(context.Background())
	udp.Run(smf_pfcp.Dispatch)

	testPfcpReq := pfcp.Message{
		Header: pfcp.Header{
			Version:         1,
			MP:              0,
			S:               0,
			MessageType:     pfcp.PFCP_ASSOCIATION_SETUP_REQUEST,
			MessageLength:   9,
			SEID:            0,
			SequenceNumber:  1,
			MessagePriority: 0,
		},
		Body: pfcp.PFCPAssociationSetupRequest{
			NodeID: &pfcpType.NodeID{
				NodeIdType: 0,
				IP:         net.ParseIP("192.168.1.1").To4(),
			},
		},
	}

	srcAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: testPfcpClientPort,
	}
	dstAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: pfcpUdp.PFCP_PORT,
	}

	err := pfcpUdp.SendPfcpMessage(testPfcpReq, srcAddr, dstAddr)
	require.Nil(t, err)

	err = udp.ClosePfcp()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)
}
