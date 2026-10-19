package context_test

import (
	"fmt"
	"net/netip"
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

func activateStaticMEC(t testing.TB, sm *smf_context.SMContext) *smf_context.DataPath {
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
	qfi := installULCLService(t, sm, "service", staticServiceDescription, 100, "BIDIRECTIONAL")
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
	installULCLService(t, sm, "service", staticServiceDescription, 1, "BIDIRECTIONAL")
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
	service.FlowInfos[0].FlowDescription = staticServiceDescription
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
	rule.FlowInfos[0].FlowDescription = staticServiceDescription
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

const staticServiceDescription = "permit out ip from 10.100.100.10/32 to assigned"

func staticClassifiers(path *smf_context.DataPath) []*smf_context.PDR {
	var result []*smf_context.PDR
	for node := path.FirstDPNode; node != nil; node = node.Next() {
		for _, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
			result = append(result, tunnel.PDR)
			result = append(result, tunnel.AdditionalPDRs...)
		}
	}
	return result
}

func TestStaticULCLPreservesUnchangedClassifiers(t *testing.T) {
	for _, kind := range []string{"empty", "unrelated QoS", "identical QoS", "equivalent QoS", "identical PCC"} {
		t.Run(kind, func(t *testing.T) {
			sm := newTIPolicyContext(t)
			installDefault(t, sm)
			installULCLService(t, sm, "inside", staticServiceDescription, 100, "BIDIRECTIONAL")
			installULCLService(t, sm, "outside", "permit out ip from 1.1.1.1/32 to assigned", 120, "BIDIRECTIONAL")
			path := activateStaticMEC(t, sm)
			before := staticClassifiers(path)
			for iteration := 0; iteration < 3; iteration++ {
				decision := &models.Pcf_SMPolCtrl_SmPolicyDecision{}
				switch kind {
				case "unrelated QoS", "identical QoS":
					qos := *sm.QosDatas["outside-qos"]
					if kind == "unrelated QoS" {
						qos.MaxbrDl = fmt.Sprintf("%d Mbps", 30+iteration)
					}
					decision.QosDecs = map[string]*models.Pcf_SMPolCtrl_QosData{"outside-qos": &qos}
				case "equivalent QoS":
					qos := *sm.QosDatas["inside-qos"]
					qos.MaxbrUl, qos.MaxbrDl = fmt.Sprintf("%d Kbps", 10000), "20000 Kbps"
					decision.QosDecs = map[string]*models.Pcf_SMPolCtrl_QosData{"inside-qos": &qos}
				case "identical PCC":
					decision.PccRules = map[string]*models.Pcf_SMPolCtrl_PccRule{
						"inside": sm.PCCRules["inside"].Pcf_SMPolCtrl_PccRule,
					}
				}
				require.NoError(t, sm.ApplyPccRules(decision))
				after := staticClassifiers(path)
				require.Len(t, after, len(before))
				for i := range before {
					require.Same(t, before[i], after[i], "unchanged PDR must retain both its object and ID")
				}
				for node := path.FirstDPNode; node != nil; node = node.Next() {
					require.Empty(t, node.UpLinkTunnel.RetiringPDRs)
					require.Empty(t, node.DownLinkTunnel.RetiringPDRs)
				}
				sm.PostRemoveDataPath()
			}
		})
	}
}

func TestStaticULCLReplacesOnlyChangedQoSClassifiers(t *testing.T) {
	sm := newTIPolicyContext(t)
	self := smf_context.GetSelf()
	address := self.ExternalAddr
	self.ExternalAddr = "192.0.2.1"
	t.Cleanup(func() { self.ExternalAddr = address })
	installDefault(t, sm)
	changedQFI := installULCLService(t, sm, "changed", staticServiceDescription,
		100, "BIDIRECTIONAL")
	installULCLService(t, sm, "kept", "permit out ip from 10.100.100.20/32 to assigned", 120, "BIDIRECTIONAL")
	path := activateStaticMEC(t, sm)
	before := staticClassifiers(path)
	qosUpdateRequests(t, sm)
	qos := *sm.QosDatas["changed-qos"]
	qos.MaxbrDl = "30 Mbps"
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{"changed-qos": &qos},
	}))
	after := staticClassifiers(path)
	require.Len(t, after, len(before))
	for i, old := range before {
		if markingQFI(t, old) == changedQFI {
			require.NotEqual(t, old.PDRID, after[i].PDRID)
			require.Equal(t, smf_context.RULE_REMOVE, old.State)
		} else {
			require.Same(t, old, after[i])
			require.Equal(t, smf_context.RULE_CREATE, old.State)
		}
	}
	request := qosUpdateRequests(t, sm)[path.FirstDPNode.GetNodeIP()]
	for _, group := range append(request.CreatePDR, request.RemovePDR...) {
		id, err := group.PDRID()
		require.NoError(t, err)
		for _, old := range before {
			if markingQFI(t, old) != changedQFI {
				require.NotEqual(t, old.PDRID, id, "unchanged static PDR must not appear on the wire")
			}
		}
	}
}

func TestStaticULCLFilterReorderingPreservesPDRs(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	service := tiPCC("service", "qos", "", 100)
	service.FlowInfos[0].FlowDescription = staticServiceDescription
	second := service.FlowInfos[0]
	second.FlowDescription = "permit out ip from 10.100.100.20/32 to assigned"
	service.FlowInfos = append(service.FlowInfos, second)
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"service": service},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
			"qos": {QosId: "qos", Var5qi: 9},
		},
	}))
	path := activateStaticMEC(t, sm)
	before := staticClassifiers(path)
	service.FlowInfos = []models.Pcf_SMPolCtrl_FlowInformation{second, service.FlowInfos[0]}
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"service": service},
	}))
	after := staticClassifiers(path)
	var beforeIDs, afterIDs []uint16
	for _, pdr := range before {
		beforeIDs = append(beforeIDs, pdr.PDRID)
	}
	for _, pdr := range after {
		afterIDs = append(afterIDs, pdr.PDRID)
	}
	require.ElementsMatch(t, beforeIDs, afterIDs)
}

func BenchmarkStaticULCLNoopPolicyUpdate(b *testing.B) {
	for _, count := range []int{1, 16, 64} {
		b.Run(fmt.Sprintf("classifiers=%d", count), func(b *testing.B) {
			sm := newTIPolicyContext(b)
			installDefault(b, sm)
			decision := &models.Pcf_SMPolCtrl_SmPolicyDecision{
				PccRules: make(map[string]*models.Pcf_SMPolCtrl_PccRule),
				QosDecs:  map[string]*models.Pcf_SMPolCtrl_QosData{"qos": {QosId: "qos", Var5qi: 9}},
			}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("service-%d", i)
				rule := tiPCC(id, "qos", "", int32(100+i))
				rule.FlowInfos[0].FlowDescription = fmt.Sprintf("permit out ip from 10.100.100.%d/32 to assigned", 1+i)
				decision.PccRules[id] = rule
			}
			require.NoError(b, sm.ApplyPccRules(decision))
			path := activateStaticMEC(b, sm)
			before := staticClassifiers(path)
			empty := &models.Pcf_SMPolCtrl_SmPolicyDecision{}
			b.ReportAllocs()
			for b.Loop() {
				if err := sm.ApplyPccRules(empty); err != nil {
					b.Fatal(err)
				}
				sm.PostRemoveDataPath()
			}
			after := staticClassifiers(path)
			for i := range before {
				require.Same(b, before[i], after[i])
			}
		})
	}
}

func TestStaticULCLReconcilesChargingWithoutResettingLiveUsage(t *testing.T) {
	sm := newTIPolicyContext(t)
	base := installDefault(t, sm)
	base.RefChgData = []string{"session-charge"}
	service := tiPCC("service", "qos", "", 100)
	service.FlowInfos[0].FlowDescription = staticServiceDescription
	service.RefChgData = []string{"flow-charge"}
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"default": base, "service": service},
		QosDecs:  map[string]*models.Pcf_SMPolCtrl_QosData{"qos": {QosId: "qos", Var5qi: 9}},
		ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{
			"session-charge": {ChgId: "session-charge", RatingGroup: 4, Offline: true},
			"flow-charge":    {ChgId: "flow-charge", RatingGroup: 5, Online: true},
		},
	}))
	sm.PostRemoveDataPath()
	path := activateStaticMEC(t, sm)
	before := staticClassifiers(path)
	upfID := path.FirstDPNode.UPF.UUID()
	refs := make(map[uint32]int)
	var flowURR uint32
	for id, entry := range sm.UrrTable[upfID] {
		refs[id] = entry.RefCount
		entry.Rule.VolumeQuota = 12345
		if info := sm.ChargingInfo[id]; info != nil && info.RatingGroup == 5 {
			flowURR = id
		}
	}
	require.NotZero(t, flowURR)
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{}))
	for i, pdr := range staticClassifiers(path) {
		require.Same(t, before[i], pdr)
	}
	for id, count := range refs {
		require.Equal(t, count, sm.UrrTable[upfID][id].RefCount)
		require.Equal(t, uint64(12345), sm.UrrTable[upfID][id].Rule.VolumeQuota)
	}
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{
			"flow-charge": {ChgId: "flow-charge", RatingGroup: 5, Offline: true},
		},
	}))
	for i, pdr := range staticClassifiers(path) {
		if markingQFI(t, pdr) == sm.SelectedSessionRule().DefQosQFI {
			require.Same(t, before[i], pdr, "session-only charging did not change")
		} else {
			require.NotEqual(t, before[i].PDRID, pdr.PDRID)
			for _, urr := range pdr.URR {
				if info := sm.ChargingInfo[urr.URRID]; info != nil && info.RatingGroup == 5 {
					require.NotEqual(t, flowURR, urr.URRID)
					require.Equal(t, models.Chf_ConvCharging_QuotaManagementIndicator_OFFLINE_CHARGING, info.ChargingMethod)
				}
			}
		}
	}
	require.Zero(t, sm.UrrTable[upfID][flowURR].RefCount)
	require.Equal(t, smf_context.RULE_REMOVE, sm.GetUrrState(upfID, flowURR))
}

func TestStaticULCLDirectionChangeRetiresOnlyRemovedDirection(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	qfi := installULCLService(t, sm, "service", staticServiceDescription, 100, "BIDIRECTIONAL")
	path := activateStaticMEC(t, sm)
	ul, dl := path.FirstDPNode.UpLinkTunnel, path.FirstDPNode.DownLinkTunnel
	oldUL := staticPacketPDR(t, ul, "10.100.100.10", 6, 443, 12345)
	oldDL := staticPacketPDR(t, dl, "10.100.100.10", 6, 443, 12345)
	defaultUL := staticPacketPDR(t, ul, "10.100.100.20", 6, 443, 12345)
	rule := tiPCC("service", "service-qos", "", 100)
	rule.FlowInfos[0].FlowDescription = staticServiceDescription
	rule.FlowInfos[0].FlowDirection = models.Pcf_SMPolCtrl_FlowDirectionRm_DOWNLINK
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"service": rule},
	}))
	require.Equal(t, qfi, markingQFI(t, oldDL))
	require.Same(t, oldDL, staticPacketPDR(t, dl, "10.100.100.10", 6, 443, 12345))
	require.Same(t, defaultUL, staticPacketPDR(t, ul, "10.100.100.10", 6, 443, 12345))
	require.Equal(t, []*smf_context.PDR{oldUL}, ul.RetiringPDRs)
	require.Empty(t, dl.RetiringPDRs)
}
