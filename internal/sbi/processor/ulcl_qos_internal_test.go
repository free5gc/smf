package processor

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/message"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

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
