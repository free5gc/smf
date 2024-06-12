package udp_test

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey"
	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/pfcp/pfcpUdp"
	smf_context "github.com/free5gc/smf/internal/context"
	smf_pfcp "github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/smf/pkg/factory"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
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

var testConfig = factory.Config{
	Info: &factory.Info{
		Version:     "1.0.0",
		Description: "SMF procdeure test configuration",
	},
	Configuration: &factory.Configuration{
		Sbi: &factory.Sbi{
			Scheme:       "http",
			RegisterIPv4: "127.0.0.1",
			BindingIPv4:  "127.0.0.1",
			Port:         8000,
		},
	},
}

var testNodeID = pfcpType.NodeID{
	NodeIdType: 0,
	IP:         net.ParseIP("127.0.0.3").To4(),
}

func initSmfContext() {
	context.InitSmfContext(&testConfig)
}

func TestSendPfcpRequest(t *testing.T) {
	//init smf context
	initSmfContext()
	context.GetSelf().CPNodeID = pfcpType.NodeID{
		NodeIdType: pfcpType.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("127.0.0.1").To4(),
	}
	udp.Server = pfcpUdp.NewPfcpServer(net.ParseIP("127.0.0.1").To4().String())

	//build message
	pfcpMsg := &pfcp.PFCPAssociationSetupRequest{}
	message := &pfcp.Message{
		Header: pfcp.Header{
			Version:        pfcp.PfcpVersion,
			MP:             0,
			S:              pfcp.SEID_NOT_PRESENT,
			MessageType:    pfcp.PFCP_ASSOCIATION_SETUP_REQUEST,
			SequenceNumber: 1,
		},
		Body: pfcpMsg,
	}
	addr := &net.UDPAddr{
		IP:   testNodeID.ResolveNodeIdToIp(),
		Port: pfcpUdp.PFCP_PORT,
	}

	Convey("test SendPfcpRequest", t, func() {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(udp.Server), "WriteRequestTo",
			func(_ *pfcpUdp.PfcpServer, _ *pfcp.Message, _ *net.UDPAddr) (*pfcpUdp.Message, error) {
				return nil, nil
			})
		defer patches.Reset()
		_, err := udp.SendPfcpRequest(message, addr)
		So(err, ShouldBeNil)
	})
}
