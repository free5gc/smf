package processor

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

// partialPSA2PFCPClient accepts Session Establishment for okIP and rejects it
// for failIP, and records every Session Deletion Request it receives.
type partialPSA2PFCPClient struct {
	fakeActivePFCPClient
	okIP, failIP string
	deletions    chan *message.SessionDeletionRequest
}

func (f *partialPSA2PFCPClient) SendSessionEstablishmentRequest(
	_ context.Context, request *message.SessionEstablishmentRequest, addr *net.UDPAddr, localSEID uint64,
) (*message.SessionEstablishmentResponse, error) {
	cause := ie.CauseRequestAccepted
	if addr.IP.String() == f.failIP {
		cause = ie.CauseRequestRejected
	}
	return message.NewSessionEstablishmentResponse(0, 0, localSEID, request.Sequence(), 0,
		ie.NewFSEID(localSEID+1000, addr.IP, nil),
		ie.NewCause(cause),
	), nil
}

func (f *partialPSA2PFCPClient) SendSessionDeletionRequest(
	_ context.Context, request *message.SessionDeletionRequest, _ *net.UDPAddr, localSEID uint64,
) (*message.SessionDeletionResponse, error) {
	f.deletions <- request
	return message.NewSessionDeletionResponse(0, 0, localSEID, request.Sequence(), 0,
		ie.NewCause(ie.CauseRequestAccepted)), nil
}

// TestEstablishPSA2RollsBackOnPartialFailure reproduces the gap tracked by the
// former "TODO: remove failed PSA2 path": when PSA2 establishment succeeds on
// one UPF but fails on another, the newly established session must be deleted
// instead of left dangling, and the caller must learn establishment failed.
func TestEstablishPSA2RollsBackOnPartialFailure(t *testing.T) {
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info: &factory.Info{Version: "1.0.0", Description: "PSA2 rollback regression"},
		Configuration: &factory.Configuration{
			Sbi:  &factory.Sbi{RegisterIPv4: "127.0.0.1", BindingIPv4: "127.0.0.1"},
			PFCP: &factory.PFCP{ExternalAddr: "127.0.0.1", NodeID: "127.0.0.1"},
		},
	}))
	sm := smf_context.NewSMContext("imsi-208930000000122", 10)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })

	ulclUPF := smf_context.NewUPF(&pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("192.0.2.130").To4(),
	}, nil)
	ulclUPF.EstablishAssociation(context.Background())
	t.Cleanup(ulclUPF.CancelAssociation)
	ulclNode := smf_context.NewDataPathNode()
	ulclNode.UPF = ulclUPF

	okIP, failIP := "192.0.2.131", "192.0.2.132"
	makeNode := func(ip string) *smf_context.DataPathNode {
		upf := smf_context.NewUPF(&pfcptype.NodeID{
			NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP(ip).To4(),
		}, nil)
		upf.EstablishAssociation(context.Background())
		t.Cleanup(upf.CancelAssociation)
		node := smf_context.NewDataPathNode()
		node.UPF = upf
		node.UpLinkTunnel.PDR = &smf_context.PDR{PDRID: 1, FAR: &smf_context.FAR{FARID: 1}}
		sm.PFCPContext[ip] = &smf_context.PFCPSessionContext{LocalSEID: uint64(net.ParseIP(ip).To4()[3])}
		return node
	}
	okNode, failNode := makeNode(okIP), makeNode(failIP)

	ulclNode.AddNext(okNode)
	okNode.AddPrev(ulclNode)
	okNode.AddNext(failNode)
	failNode.AddPrev(okNode)

	sm.BPManager = &smf_context.BPManager{
		ActivatingPath: &smf_context.DataPath{Activated: true, FirstDPNode: ulclNode},
		ULCL:           ulclUPF,
	}

	client := &partialPSA2PFCPClient{okIP: okIP, failIP: failIP, deletions: make(chan *message.SessionDeletionRequest, 1)}
	processor := &Processor{}
	processor.SetActivePFCPClient(client)

	err := processor.EstablishPSA2(sm)
	require.Error(t, err, "PSA2 establishment must report failure when any UPF rejects it")

	deletion := <-client.deletions
	require.Equal(t, uint64(1131), deletion.SEID(), "the session that succeeded must be the one rolled back")
	require.Equal(t, uint64(0), sm.PFCPContext[okIP].RemoteSEID, "rolled-back session must not be left dangling")
	require.Equal(t, uint64(0), sm.PFCPContext[failIP].RemoteSEID,
		"a session that never established has nothing to roll back")
}
