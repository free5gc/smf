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

type tiPFCPClient struct {
	fakeActivePFCPClient
	requests chan *message.SessionModificationRequest
}

func (f *tiPFCPClient) SendSessionModificationRequest(
	_ context.Context, request *message.SessionModificationRequest, _ *net.UDPAddr, localSEID uint64,
) (*message.SessionModificationResponse, error) {
	f.requests <- request
	return message.NewSessionModificationResponse(0, 0, localSEID, request.Sequence(), 0,
		ie.NewCause(ie.CauseRequestAccepted)), nil
}

func TestActivateUPFSessionIncludesAllFilterPDRs(t *testing.T) {
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info: &factory.Info{Version: "1.0.0", Description: "TI PFCP regression"},
		Configuration: &factory.Configuration{
			Sbi:  &factory.Sbi{RegisterIPv4: "127.0.0.1", BindingIPv4: "127.0.0.1"},
			PFCP: &factory.PFCP{ExternalAddr: "127.0.0.1", NodeID: "127.0.0.1"},
		},
	}))
	upf := smf_context.NewUPF(&pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("192.0.2.119").To4(),
	}, nil)
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)
	sm := smf_context.NewSMContext("imsi-208930000000119", 10)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.PFCPContext["192.0.2.119"] = &smf_context.PFCPSessionContext{LocalSEID: 123, RemoteSEID: 456}
	qer := &smf_context.QER{QERID: 10, QFI: &pfcptype.QFI{QFI: 1}}
	far := &smf_context.FAR{FARID: 20}
	makePDR := func(id uint16, flow string) *smf_context.PDR {
		return &smf_context.PDR{
			PDRID: id, FAR: far, QER: []*smf_context.QER{qer},
			PDI: smf_context.PDI{SDFFilter: &pfcptype.SDFFilter{Fd: true, FlowDescription: []byte(flow)}},
		}
	}
	first := makePDR(1, "permit out ip from 10.100.1.0/24 to assigned")
	second := makePDR(3, "permit out ip from 10.100.2.0/24 to assigned")
	disabled := &smf_context.PDR{PDRID: 2, FAR: &smf_context.FAR{FARID: 21}, Disabled: true}
	node := smf_context.NewDataPathNode()
	node.UPF = upf
	node.UpLinkTunnel.PDR, node.UpLinkTunnel.AdditionalPDRs = first, []*smf_context.PDR{second}
	node.DownLinkTunnel.PDR = disabled
	sm.Tunnel.DataPathPool[1] = &smf_context.DataPath{Activated: true, FirstDPNode: node}
	client := &tiPFCPClient{requests: make(chan *message.SessionModificationRequest, 1)}
	processor := &Processor{}
	processor.SetActivePFCPClient(client)
	processor.ActivateUPFSession(sm, nil)
	request := <-client.requests
	wire := make([]byte, request.MarshalLen())
	require.NoError(t, request.MarshalTo(wire))
	parsed, err := message.ParseSessionModificationRequest(wire)
	require.NoError(t, err)
	require.Len(t, parsed.CreatePDR, 2)
	require.Len(t, parsed.CreateQER, 1)
	for i, pdr := range parsed.CreatePDR {
		filter, decodeErr := pdr.SDFFilter()
		require.NoError(t, decodeErr)
		want := first
		if i == 1 {
			want = second
		}
		require.Equal(t, string(want.PDI.SDFFilter.FlowDescription), filter.FlowDescription)
	}
	first.State, second.State = smf_context.RULE_REMOVE, smf_context.RULE_REMOVE
	processor.ActivateUPFSession(sm, nil)
	request = <-client.requests
	require.Len(t, request.RemovePDR, 2)
	require.Len(t, request.RemoveQER, 1, "remove the shared QER once, after its last PDR")
}
