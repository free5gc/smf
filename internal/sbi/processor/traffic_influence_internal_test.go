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

func TestActivateUPFSessionSkipsUnchangedUPFsButSendsURROperations(t *testing.T) {
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info: &factory.Info{Version: "1.0.0", Description: "incremental PFCP regression"},
		Configuration: &factory.Configuration{
			Sbi:  &factory.Sbi{RegisterIPv4: "127.0.0.1", BindingIPv4: "127.0.0.1"},
			PFCP: &factory.PFCP{ExternalAddr: "127.0.0.1", NodeID: "127.0.0.1"},
		},
	}))
	sm := smf_context.NewSMContext("imsi-208930000000121", 10)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	client := &tiPFCPClient{requests: make(chan *message.SessionModificationRequest, 4)}
	processor := &Processor{}
	processor.SetActivePFCPClient(client)
	var upfs []*smf_context.UPF
	for i := 0; i < 2; i++ {
		ip := net.IPv4(192, 0, 2, byte(125+i))
		upf := smf_context.NewUPF(&pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: ip.To4()}, nil)
		upf.EstablishAssociation(context.Background())
		t.Cleanup(upf.CancelAssociation)
		upfs = append(upfs, upf)
		far := &smf_context.FAR{FARID: 1, State: smf_context.RULE_CREATE}
		qer := &smf_context.QER{QERID: 1, State: smf_context.RULE_CREATE, QFI: &pfcptype.QFI{QFI: 4}}
		urr := &smf_context.URR{URRID: uint32(50 + i), MeasureMethod: "vol"}
		sm.RegisterUrr(upf.UUID(), urr)
		sm.SetUrrState(upf.UUID(), urr.URRID, smf_context.RULE_CREATE)
		pdr := &smf_context.PDR{
			PDRID: 1, State: smf_context.RULE_CREATE, FAR: far,
			QER: []*smf_context.QER{qer}, URR: []*smf_context.URR{urr},
		}
		node := smf_context.NewDataPathNode()
		node.UPF, node.UpLinkTunnel.PDR = upf, pdr
		sm.Tunnel.DataPathPool[int64(i+1)] = &smf_context.DataPath{Activated: true, FirstDPNode: node}
		sm.PFCPContext[ip.String()] = &smf_context.PFCPSessionContext{
			LocalSEID: uint64(100 + i), RemoteSEID: uint64(200 + i), PDRs: map[uint16]*smf_context.PDR{1: pdr},
		}
	}
	processor.ActivateUPFSession(sm, nil)
	require.Empty(t, client.requests, "no rules changed on either UPF")
	for _, state := range []smf_context.RuleState{smf_context.RULE_UPDATE, smf_context.RULE_QUERY} {
		sm.SetUrrState(upfs[0].UUID(), 50, state)
		processor.ActivateUPFSession(sm, nil)
		require.Len(t, client.requests, 1, "the other UPF must stay idle")
		request := <-client.requests
		require.Equal(t, uint64(200), request.SEID())
		if state == smf_context.RULE_UPDATE {
			require.Len(t, request.UpdateURR, 1)
		} else {
			require.Len(t, request.QueryURR, 1)
		}
	}
	processor.ActivateUPFSession(sm, nil)
	require.Empty(t, client.requests, "a completed URR operation must not cause another empty request")
}
