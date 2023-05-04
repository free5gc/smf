package message_test

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	smf_pfcp "github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/udp"
)

func TestSendPfcpAssociationSetupRequest(t *testing.T) {
}

func TestSendPfcpSessionEstablishmentResponse(t *testing.T) {
}

func TestSendPfcpSessionEstablishmentRequest(t *testing.T) {
}

func TestSendHeartbeatResponse(t *testing.T) {
	udp.Run(smf_pfcp.Dispatch)

	udp.ServerStartTime = time.Now()
	var seq uint32 = 1
	addr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 7001,
	}
	message.SendHeartbeatResponse(addr, seq)

	err := udp.Server.Close()
	require.NoError(t, err)
}
