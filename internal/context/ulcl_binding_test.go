package context_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/util/flowdesc"
)

func installULCLService(t *testing.T, sm *smf_context.SMContext, id, description string,
	precedence int32, direction models.Pcf_SMPolCtrl_FlowDirectionRm,
) uint8 {
	t.Helper()
	rule := tiPCC(id, id+"-qos", "", precedence)
	rule.FlowInfos[0].FlowDescription, rule.FlowInfos[0].FlowDirection = description, direction
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{id: rule},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
			id + "-qos": {QosId: id + "-qos", Var5qi: 9, MaxbrUl: "10 Mbps", MaxbrDl: "20 Mbps"},
		},
	}))
	return sm.PCCRules[id].QFI
}

func activateStaticMEC(t *testing.T, sm *smf_context.SMContext) *smf_context.DataPath {
	t.Helper()
	path := staticMECPath(sm)
	path.FirstDPNode.AddNext(nil)
	bindings, err := path.ULCLQoSBindings(sm)
	require.NoError(t, err)
	require.NoError(t, path.ActivateTunnelAndPDR(sm, 255, bindings...))
	sm.Tunnel.AddDataPath(path)
	return path
}

// Evaluate an example packet against the emitted classifiers, including their
// priorities. Expected QFIs come from the original PCC binding, not the resolver.
func staticPacketPDR(t *testing.T, tunnel *smf_context.GTPTunnel, remote string,
	protocol uint8, remotePort, localPort uint16,
) *smf_context.PDR {
	t.Helper()
	address := netip.MustParseAddr(remote)
	var selected *smf_context.PDR
	for _, pdr := range tunnel.PDRs() {
		if pdr.Disabled || pdr.State == smf_context.RULE_REMOVE {
			continue
		}
		filter, err := flowdesc.Decode(string(pdr.PDI.SDFFilter.FlowDescription))
		require.NoError(t, err)
		if filter.Src != "any" {
			prefix, prefixErr := netip.ParsePrefix(filter.Src)
			if prefixErr != nil {
				ip := netip.MustParseAddr(filter.Src)
				prefix = netip.PrefixFrom(ip, ip.BitLen())
			}
			if !prefix.Contains(address) {
				continue
			}
		}
		if filter.Proto != flowdesc.ProtocolNumberAny && filter.Proto != protocol {
			continue
		}
		contains := func(ports flowdesc.PortRanges, port uint16) bool {
			if len(ports) == 0 {
				return true
			}
			for _, interval := range ports {
				if interval.Start <= port && port <= interval.End {
					return true
				}
			}
			return false
		}
		if !contains(filter.SrcPorts, remotePort) || !contains(filter.DstPorts, localPort) {
			continue
		}
		if selected == nil || pdr.Precedence < selected.Precedence {
			selected = pdr
		}
	}
	return selected
}

func markingQFI(t *testing.T, pdr *smf_context.PDR) uint8 {
	t.Helper()
	require.NotNil(t, pdr)
	var qfi uint8
	count := 0
	for _, qer := range pdr.QER {
		if qer.QFI != nil {
			count++
			qfi = qer.QFI.QFI
		}
	}
	require.Equal(t, 1, count, "a PDR must have one unambiguous marking instruction")
	return qfi
}

func TestStaticULCLFullAndPartialPCCBinding(t *testing.T) {
	for _, source := range []string{"10.100.100.0/24", "10.100.0.0/16", "10.100.100.10/32"} {
		t.Run(source, func(t *testing.T) {
			sm := newTIPolicyContext(t)
			installDefault(t, sm)
			qfi := installULCLService(t, sm, "service", "permit out ip from "+source+" to assigned", 100, "BIDIRECTIONAL")
			flow := sm.AdditionalQosFlows[qfi]
			path := activateStaticMEC(t, sm)
			require.Same(t, flow, sm.AdditionalQosFlows[qfi], "routing must not allocate another QoS flow")
			for _, tunnel := range []*smf_context.GTPTunnel{path.FirstDPNode.UpLinkTunnel, path.FirstDPNode.DownLinkTunnel} {
				require.Equal(t, qfi, markingQFI(t, staticPacketPDR(t, tunnel, "10.100.100.10", 6, 443, 12345)))
				other := staticPacketPDR(t, tunnel, "10.100.100.20", 6, 443, 12345)
				if source == "10.100.100.10/32" {
					require.Equal(t, sm.SelectedSessionRule().DefQosQFI, markingQFI(t, other))
				} else {
					require.Equal(t, qfi, markingQFI(t, other))
				}
				require.Nil(t, staticPacketPDR(t, tunnel, "10.100.101.10", 6, 443, 12345),
					"route must not widen the PCC intersection")
			}
		})
	}
}

func TestStaticULCLPreservesDirectionPortsAndPrecedence(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	broad := installULCLService(t, sm, "broad", "permit out ip from 10.100.100.0/24 to assigned", 100, "BIDIRECTIONAL")
	dns := installULCLService(t, sm, "dns",
		"permit out 17 from 10.100.100.10/32 53 to assigned 10000-20000", 50, "DOWNLINK")
	path := activateStaticMEC(t, sm)
	ul, dl := path.FirstDPNode.UpLinkTunnel, path.FirstDPNode.DownLinkTunnel
	require.Equal(t, dns, markingQFI(t, staticPacketPDR(t, dl, "10.100.100.10", 17, 53, 15000)))
	for _, packet := range []struct {
		proto         uint8
		remote, local uint16
	}{{6, 53, 15000}, {17, 443, 15000}, {17, 53, 30000}} {
		selected := staticPacketPDR(t, dl, "10.100.100.10", packet.proto, packet.remote, packet.local)
		require.Equal(t, broad, markingQFI(t, selected))
	}
	require.Equal(t, broad, markingQFI(t, staticPacketPDR(t, ul, "10.100.100.10", 17, 53, 15000)),
		"downlink-only service must not change uplink QoS")
	for _, tunnel := range []*smf_context.GTPTunnel{ul, dl} {
		for _, pdr := range tunnel.PDRs() {
			require.Less(t, pdr.Precedence, uint32(50), "static classifiers must win over the old route")
		}
	}
	require.Equal(t, int32(50), sm.PCCRules["dns"].Precedence, "do not renumber PCC policy")
}

func TestStaticULCLRefreshAfterPCCChanges(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	path := activateStaticMEC(t, sm)
	original := path.FirstDPNode.DownLinkTunnel.PDR
	qfi := installULCLService(t, sm, "service", "permit out ip from 10.100.100.10/32 to assigned", 100, "BIDIRECTIONAL")
	selected := staticPacketPDR(t, path.FirstDPNode.DownLinkTunnel, "10.100.100.10", 6, 443, 12345)
	require.Equal(t, qfi, markingQFI(t, selected))
	require.Equal(t, smf_context.RULE_REMOVE, original.State)
	require.Contains(t, path.FirstDPNode.DownLinkTunnel.RetiringPDRs, original)
	sm.PostRemoveDataPath()
	require.Empty(t, path.FirstDPNode.DownLinkTunnel.RetiringPDRs)
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"service": nil},
	}))
	require.Equal(t, sm.SelectedSessionRule().DefQosQFI,
		markingQFI(t, staticPacketPDR(t, path.FirstDPNode.DownLinkTunnel, "10.100.100.10", 6, 443, 12345)))
	sm.PostRemoveDataPath()
	require.Empty(t, sm.AdditionalQosFlows)
}

func TestStaticULCLRejectsUnrepresentablePolicyBeforeAllocation(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	installULCLService(t, sm, "service", "permit out ip from 10.100.100.10/32 to assigned", 1, "BIDIRECTIONAL")
	path := staticMECPath(sm)
	_, err := path.ULCLQoSBindings(sm)
	require.ErrorContains(t, err, "precedence space")
	require.Nil(t, path.FirstDPNode.UpLinkTunnel.PDR)
}

func TestStaticULCLPreservesChargingAndGBR(t *testing.T) {
	sm := newTIPolicyContext(t)
	base := installDefault(t, sm)
	base.RefChgData = []string{"session-charging"}
	service := tiPCC("gbr", "gbr-qos", "", 100)
	service.FlowInfos[0].FlowDescription = "permit out ip from 10.100.100.10/32 to assigned"
	service.RefChgData = []string{"flow-charging"}
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"default": base, "gbr": service},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
			"gbr-qos": {QosId: "gbr-qos", Var5qi: 1, GbrUl: "5 Mbps", GbrDl: "5 Mbps", MaxbrUl: "10 Mbps", MaxbrDl: "20 Mbps"},
		},
		ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{
			"session-charging": {ChgId: "session-charging", RatingGroup: 4, Offline: true},
			"flow-charging":    {ChgId: "flow-charging", RatingGroup: 5, Online: true},
		},
	}))
	sm.PostRemoveDataPath()
	path := activateStaticMEC(t, sm)
	node := path.FirstDPNode
	var flowURR uint32
	for _, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
		pdr := staticPacketPDR(t, tunnel, "10.100.100.10", 6, 443, 12345)
		require.Equal(t, sm.PCCRules["gbr"].QFI, markingQFI(t, pdr))
		require.Len(t, pdr.QER, 1, "GBR classifier must not enforce Session-AMBR")
		require.NotNil(t, pdr.QER[0].GBR)
		var groups []int32
		for _, urr := range pdr.URR {
			info := sm.ChargingInfo[urr.URRID]
			if info != nil {
				groups = append(groups, info.RatingGroup)
				require.Equal(t, node.UPF.UUID(), info.UpfId)
				if info.RatingGroup == 5 {
					flowURR = urr.URRID
				}
			}
		}
		require.ElementsMatch(t, []int32{4, 5}, groups)
		other := staticPacketPDR(t, tunnel, "10.100.100.20", 6, 443, 12345)
		require.Len(t, other.QER, 2)
		require.Len(t, other.URR, 1)
		require.Equal(t, int32(4), sm.ChargingInfo[other.URR[0].URRID].RatingGroup)
	}
	require.Equal(t, 2, sm.UrrTable[node.UPF.UUID()][flowURR].RefCount)
	originalURRs := sm.PCCRules["gbr"].Datapath.GetChargingUrr(sm)
	require.NotEmpty(t, originalURRs)
	require.Equal(t, int32(5), sm.ChargingInfo[originalURRs[0].URRID].RatingGroup)
	// Updating the charging method must not revive an old online URR just
	// because it shares the same rating group and UPF.
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"gbr": service},
		ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{
			"flow-charging": {ChgId: "flow-charging", RatingGroup: 5, Offline: true},
		},
	}))
	updated := staticPacketPDR(t, node.DownLinkTunnel, "10.100.100.10", 6, 443, 12345)
	for _, urr := range updated.URR {
		if info := sm.ChargingInfo[urr.URRID]; info != nil && info.RatingGroup == 5 {
			require.Equal(t, models.Chf_ConvCharging_QuotaManagementIndicator_OFFLINE_CHARGING, info.ChargingMethod)
			require.NotEqual(t, flowURR, urr.URRID)
		}
	}
	sm.PreRemoveDataPath(path)
	sm.PostRemoveDataPath()
	require.Nil(t, sm.GetUrrRule(node.UPF.UUID(), flowURR), "retire the static anchor's last URR reference")
	require.NotNil(t, sm.ChargingInfo[flowURR], "retain identity for final usage")
}

func TestStaticULCLDoesNotOverrideExplicitTIRouting(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	rule := tiPCC("ti", "ti-default", "ti-route", 23)
	rule.FlowInfos[0].FlowDescription = "permit out ip from 10.100.100.10/32 to assigned"
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"ti": rule},
		QosDecs:  map[string]*models.Pcf_SMPolCtrl_QosData{"ti-default": {QosId: "ti-default", DefQosFlowIndication: true}},
		TraffContDecs: map[string]*models.Pcf_SMPolCtrl_TrafficControlData{
			"ti-route": {TcId: "ti-route", RouteToLocs: []models.RouteToLocation{{Dnai: "mec"}}},
		},
	}))
	path := activateStaticMEC(t, sm)
	for _, tunnel := range []*smf_context.GTPTunnel{path.FirstDPNode.UpLinkTunnel, path.FirstDPNode.DownLinkTunnel} {
		for _, pdr := range tunnel.PDRs() {
			require.Greater(t, pdr.Precedence, uint32(23), "explicit PCF routing must remain effective")
		}
	}
}
