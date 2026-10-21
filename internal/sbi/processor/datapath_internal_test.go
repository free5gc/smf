package processor

import (
	"context"
	"fmt"
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

func TestStaticULCLPhasesSendEveryQoSClassifier(t *testing.T) {
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info: &factory.Info{Version: "1.0.0", Description: "static QoS PFCP regression"},
		Configuration: &factory.Configuration{
			Sbi:  &factory.Sbi{RegisterIPv4: "127.0.0.1", BindingIPv4: "127.0.0.1"},
			PFCP: &factory.PFCP{ExternalAddr: "127.0.0.1", NodeID: "127.0.0.1"},
		},
	}))
	sm := smf_context.NewSMContext("imsi-208930000000120", 10)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	var nodes []*smf_context.DataPathNode
	for index := 0; index < 3; index++ {
		ip := fmt.Sprintf("192.0.2.%d", 120+index)
		upf := smf_context.NewUPF(&pfcptype.NodeID{
			NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP(ip).To4(),
		}, nil)
		upf.EstablishAssociation(context.Background())
		t.Cleanup(upf.CancelAssociation)
		node := smf_context.NewDataPathNode()
		node.UPF = upf
		sm.PFCPContext[ip] = &smf_context.PFCPSessionContext{LocalSEID: uint64(120 + index), RemoteSEID: 456}
		qers := []*smf_context.QER{
			{QERID: 10, QFI: &pfcptype.QFI{QFI: 4}},
			{QERID: 11, QFI: &pfcptype.QFI{QFI: 1}},
		}
		urrs := []*smf_context.URR{{URRID: 30, MeasureMethod: "vol"}, {URRID: 31, MeasureMethod: "vol"}}
		for _, urr := range urrs {
			sm.RegisterUrr(upf.UUID(), urr)
		}
		for direction, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
			far := &smf_context.FAR{FARID: uint32(20 + direction)}
			for flow, description := range []string{
				"permit out ip from 10.100.100.10/32 to assigned",
				"permit out ip from 10.100.100.0/24 to assigned",
			} {
				if direction == 0 && flow == 0 {
					continue // Dedicated flow exists only in downlink.
				}
				pdr := &smf_context.PDR{
					PDRID: uint16(1 + direction*2 + flow), Precedence: uint32(5 + flow), FAR: far,
					QER: []*smf_context.QER{qers[flow]},
					PDI: smf_context.PDI{SDFFilter: &pfcptype.SDFFilter{Fd: true, FlowDescription: []byte(description)}},
				}
				sm.PDRAppendURRs(upf.UUID(), pdr, []*smf_context.URR{urrs[flow]})
				if tunnel.PDR == nil {
					tunnel.PDR = pdr
				} else {
					tunnel.AdditionalPDRs = append(tunnel.AdditionalPDRs, pdr)
				}
			}
		}
		if index > 0 {
			nodes[index-1].AddNext(node)
			node.AddPrev(nodes[index-1])
		}
		nodes = append(nodes, node)
	}
	path := &smf_context.DataPath{
		Activated: true, FirstDPNode: nodes[0], Destination: smf_context.Destination{DestinationIP: "10.100.100.0/24"},
	}
	sm.BPManager = &smf_context.BPManager{ActivatingPath: path, ULCL: nodes[1].UPF}
	client := &tiPFCPClient{requests: make(chan *message.SessionModificationRequest, 4)}
	processor := &Processor{}
	processor.SetActivePFCPClient(client)
	check := func(request *message.SessionModificationRequest, node *smf_context.DataPathNode,
		tunnels ...*smf_context.GTPTunnel,
	) {
		wire := make([]byte, request.MarshalLen())
		require.NoError(t, request.MarshalTo(wire))
		parsed, err := message.ParseSessionModificationRequest(wire)
		require.NoError(t, err)
		expected := make(map[uint16]*smf_context.PDR)
		expectedQERs, expectedURRs := make(map[uint32]bool), make(map[uint32]bool)
		for _, tunnel := range tunnels {
			for _, pdr := range tunnel.PDRs() {
				expected[pdr.PDRID] = pdr
				expectedQERs[pdr.QER[0].QERID], expectedURRs[pdr.URR[0].URRID] = true, true
			}
		}
		require.Len(t, parsed.CreatePDR, len(expected))
		for _, grouped := range parsed.CreatePDR {
			id, idErr := grouped.PDRID()
			require.NoError(t, idErr)
			pdr := expected[id]
			require.NotNil(t, pdr)
			filter, filterErr := grouped.SDFFilter()
			require.NoError(t, filterErr)
			require.Equal(t, string(pdr.PDI.SDFFilter.FlowDescription), filter.FlowDescription)
			priority, priorityErr := grouped.Precedence()
			require.NoError(t, priorityErr)
			require.Equal(t, pdr.Precedence, priority, "ULCL must not overwrite policy priority with 30")
			qerID, qerErr := grouped.QERID()
			require.NoError(t, qerErr)
			require.Equal(t, pdr.QER[0].QERID, qerID)
			urrID, urrErr := grouped.URRID()
			require.NoError(t, urrErr)
			require.Equal(t, pdr.URR[0].URRID, urrID)
		}
		state := pfcpStateForTunnels(node.UPF, tunnels...)
		require.Len(t, state.qerList, len(expectedQERs))
		require.Len(t, state.urrList, len(expectedURRs))
	}
	processor.EstablishPSA2(sm)
	check(<-client.requests, nodes[2], nodes[2].UpLinkTunnel)
	require.NoError(t, processor.EstablishULCL(sm))
	check(<-client.requests, nodes[1], nodes[1].UpLinkTunnel, nodes[1].DownLinkTunnel)
	processor.UpdatePSA2DownLink(sm)
	downlink := <-client.requests
	check(downlink, nodes[2], nodes[2].DownLinkTunnel)
	require.Len(t, downlink.CreateQER, 1, "downlink-only flow QER must be installed at its own phase")
	processor.UpdateRANAndIUPFUpLink(sm)
	check(<-client.requests, nodes[0], nodes[0].UpLinkTunnel, nodes[0].DownLinkTunnel)
	require.Equal(t, smf_context.AddPSASuccess, sm.BPManager.BPStatus)
}
