package context_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	goPfcpMessage "github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/util/flowdesc"
)

func staticMECPath(sm *smf_context.SMContext) *smf_context.DataPath {
	return &smf_context.DataPath{
		FirstDPNode: sm.Tunnel.DataPathPool.GetDefaultPath().CopyFirstDPNode(),
		Destination: smf_context.Destination{DestinationIP: "10.100.100.0/24"},
	}
}

func TestStaticULCLRetainsDefaultFlowQER(t *testing.T) {
	for _, qfi := range []uint8{1, 7} {
		t.Run(string(rune('0'+qfi)), func(t *testing.T) {
			sm := newTIPolicyContext(t)
			sm.SelectedSessionRule().DefQosQFI = qfi
			installDefault(t, sm)
			// This dedicated service is disjoint from the MEC route, as in ci-test.
			service := tiPCC("service", "service-qos", "", 100)
			service.FlowInfos[0].FlowDescription = "permit out ip from 1.1.1.1/32 to assigned"
			require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
				PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"service": service},
				QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
					"service-qos": {QosId: "service-qos", Var5qi: 9, MaxbrUl: "10 Mbps", MaxbrDl: "10 Mbps"},
				},
			}))
			mec := staticMECPath(sm)
			mec.FirstDPNode.AddNext(nil) // The ci-test MEC path terminates at the I-UPF.
			bindings, bindingErr := mec.ULCLQoSBindings(sm)
			require.NoError(t, bindingErr)
			require.Equal(t, qfi, bindings[0].QFI)
			require.NoError(t, mec.ActivateTunnelAndPDR(sm, 255, bindings...))
			require.False(t, mec.IsDefaultPath, "routing role is independent of QoS flow binding")
			assertMarkingQFI(t, mec, qfi)
			original := sm.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode
			for node := mec.FirstDPNode; node != nil; node = node.Next() {
				for i, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
					oldTunnel := original.UpLinkTunnel
					if i == 1 {
						oldTunnel = original.DownLinkTunnel
					}
					require.Len(t, tunnel.PDR.QER, 2)
					for _, qer := range tunnel.PDR.QER {
						require.Contains(t, oldTunnel.PDR.QER, qer, "reuse the existing per-UPF QERs")
						if qer.MBR != nil {
							require.Nil(t, qer.QFI, "shared AMBR must not acquire a marking instruction")
						}
					}
					filter, err := flowdesc.Decode(string(tunnel.PDR.PDI.SDFFilter.FlowDescription))
					require.NoError(t, err)
					require.Equal(t, mec.Destination.DestinationIP, filter.Src)
				}
				original = original.Next()
			}
		})
	}
}

func TestStaticULCLPortClassifier(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	rule := smf_context.NewPCCRule(tiPCC("dns", "qos", "", 100))
	rule.QFI = 4
	rule.FlowInfos[0].FlowDescription = "permit out 17 from 10.100.100.0/24 53 to assigned"
	sm.PCCRules[rule.PccRuleId] = rule
	mec := staticMECPath(sm)
	mec.Destination.DestinationPort = "443"
	bindings, bindingErr := mec.ULCLQoSBindings(sm)
	require.NoError(t, bindingErr)
	require.NoError(t, mec.ActivateTunnelAndPDR(sm, 255, bindings...))
	for node := mec.FirstDPNode; node != nil; node = node.Next() {
		for _, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
			filter, err := flowdesc.Decode(string(tunnel.PDR.PDI.SDFFilter.FlowDescription))
			require.NoError(t, err)
			require.Equal(t, flowdesc.PortRanges{{Start: 443, End: 443}}, filter.SrcPorts)
		}
	}
}

func TestStaticULCLPFCPReferencesExistingMarkingQER(t *testing.T) {
	sm := newTIPolicyContext(t)
	self := smf_context.GetSelf()
	originalAddress := self.ExternalAddr
	self.ExternalAddr = "127.0.0.1"
	t.Cleanup(func() { self.ExternalAddr = originalAddress })
	installDefault(t, sm)
	for node := sm.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode; node != nil; node = node.Next() {
		for _, qer := range node.UpLinkTunnel.PDR.QER {
			qer.State = smf_context.RULE_CREATE // Already installed by the default path.
		}
	}
	mec := staticMECPath(sm)
	bindings, bindingErr := mec.ULCLQoSBindings(sm)
	require.NoError(t, bindingErr)
	require.NoError(t, mec.ActivateTunnelAndPDR(sm, 255, bindings...))
	for node := mec.FirstDPNode; node != nil; node = node.Next() {
		sm.PFCPContext[node.GetNodeIP()].RemoteSEID = 123
		ul, dl := node.UpLinkTunnel.PDR, node.DownLinkTunnel.PDR
		request, err := pfcp_message.BuildSessionModificationRequest(node.GetNodeIP(), node.UPF.UUID(), sm,
			[]*smf_context.PDR{ul, dl}, []*smf_context.FAR{ul.FAR, dl.FAR}, nil, ul.QER, nil)
		require.NoError(t, err)
		wire := make([]byte, request.MarshalLen())
		require.NoError(t, request.MarshalTo(wire))
		parsed, err := goPfcpMessage.ParseSessionModificationRequest(wire)
		require.NoError(t, err)
		require.Empty(t, parsed.CreateQER, "existing QERs need references, not duplicate creation")
		require.Len(t, parsed.CreatePDR, 2)
		for _, pdr := range parsed.CreatePDR {
			children, decodeErr := pdr.CreatePDR()
			require.NoError(t, decodeErr)
			var ids []uint32
			for _, child := range children {
				if child.Type == ie.QERID {
					id, idErr := child.QERID()
					require.NoError(t, idErr)
					ids = append(ids, id)
				}
			}
			require.ElementsMatch(t, []uint32{ul.QER[0].QERID, ul.QER[1].QERID}, ids)
		}
	}
}
