package context_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

func newTIPolicyContext(t testing.TB) *smf_context.SMContext {
	t.Helper()
	initConfig()
	upi, err := smf_context.NewUserPlaneInformation(&userPlaneConfig)
	require.NoError(t, err)
	smf_context.GetSelf().UserPlaneInformation = upi
	for _, node := range upi.UPFs {
		node.UPF.EstablishAssociation(context.Background())
	}
	sm := smf_context.NewSMContext("imsi-208930000000098", 10)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000098", Dnn: "internet", SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
	}
	sm.SelectedPDUSessionType = 1
	sm.SessionRules["session"] = smf_context.NewSessionRule(&models.Pcf_SMPolCtrl_SessionRule{
		SessRuleId: "session", AuthSessAmbr: &models.Ambr{Uplink: "1000 Kbps", Downlink: "1000 Kbps"},
		AuthDefQos: &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8}},
	})
	sm.SelectedSessionRuleID = "session"
	sm.DnnConfiguration = models.Udm_SDM_DnnConfiguration{
		SessionAmbr: &models.Ambr{Uplink: "1 Gbps", Downlink: "1 Gbps"},
	}
	require.NoError(t, sm.AllocUeIP())
	return sm
}

func tiPCC(id, qos, tc string, precedence int32) *models.Pcf_SMPolCtrl_PccRule {
	rule := &models.Pcf_SMPolCtrl_PccRule{
		PccRuleId: id, Precedence: precedence,
		FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{
			FlowDescription: "permit out ip from 10.100.0.0/16 to assigned",
			FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
		}},
	}
	if qos != "" {
		rule.RefQosData = []string{qos}
	}
	if tc != "" {
		rule.RefTcData = []string{tc}
	}
	return rule
}

func installDefault(t testing.TB, sm *smf_context.SMContext) *models.Pcf_SMPolCtrl_PccRule {
	t.Helper()
	rule := tiPCC("default", "", "", 255)
	rule.FlowInfos[0].FlowDescription = "permit out ip from any to assigned"
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"default": rule},
	}))
	return rule
}

func assertMarkingQFI(t *testing.T, dp *smf_context.DataPath, qfi uint8) {
	t.Helper()
	for node := dp.FirstDPNode; node != nil; node = node.Next() {
		for _, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
			count := 0
			for _, qer := range tunnel.PDR.QER {
				if qer.QFI != nil {
					count++
					require.Equal(t, qfi, qer.QFI.QFI)
				}
			}
			require.Equal(t, 1, count, "each PDR needs exactly its bound flow's marking QER")
		}
	}
}

func TestTIDefaultFlowBindingAndWithdrawal(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	for _, tcID := range []string{"mec-route", "normal-route"} {
		tc := &models.Pcf_SMPolCtrl_TrafficControlData{TcId: tcID}
		if tcID == "mec-route" {
			tc.RouteToLocs = []models.RouteToLocation{{Dnai: "mec"}}
		}
		require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
			PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"ti": tiPCC("ti", "default-ref", tcID, 10)},
			QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
				"default-ref": {QosId: "default-ref", DefQosFlowIndication: true},
			},
			TraffContDecs: map[string]*models.Pcf_SMPolCtrl_TrafficControlData{tcID: tc},
		}))
		require.Equal(t, sm.SelectedSessionRule().DefQosQFI, sm.PCCRules["ti"].QFI)
		assertMarkingQFI(t, sm.PCCRules["ti"].Datapath, sm.SelectedSessionRule().DefQosQFI)
		require.Empty(t, sm.AdditionalQosFlows, "the default indication is not an additional QoS profile")
		sm.PostRemoveDataPath()
		assertMarkingQFI(t, sm.Tunnel.DataPathPool.GetDefaultPath(), sm.SelectedSessionRule().DefQosQFI)
	}
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"ti": nil},
	}))
	sm.PostRemoveDataPath()
	require.Len(t, sm.PCCRules, 1)
	assertMarkingQFI(t, sm.Tunnel.DataPathPool.GetDefaultPath(), sm.SelectedSessionRule().DefQosQFI)
	for _, id := range sm.QerUpfMap {
		node := sm.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode
		if qer := node.UPF.GetQERById(id); qer != nil {
			require.NotNil(t, qer.QFI)
		}
	}
}

func TestTIPartialRulePreservesSharedFlowOnRemoval(t *testing.T) {
	sm := newTIPolicyContext(t)
	installDefault(t, sm)
	service := tiPCC("service", "service-qos", "", 100)
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"service": service},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
			"service-qos": {QosId: "service-qos", Var5qi: 9, MaxbrUl: "10 Mbps", MaxbrDl: "10 Mbps"},
		},
	}))
	qfi := sm.PCCRules["service"].QFI
	flow := sm.AdditionalQosFlows[qfi]
	flow.State = smf_context.QoSFlowSet
	// Full coverage changes the original service's routing reference only.
	for _, tcID := range []string{"full-mec", "full-normal"} {
		updated := *service
		updated.RefTcData = []string{tcID}
		tc := &models.Pcf_SMPolCtrl_TrafficControlData{TcId: tcID}
		if tcID == "full-mec" {
			tc.RouteToLocs = []models.RouteToLocation{{Dnai: "mec"}}
		}
		require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
			PccRules:      map[string]*models.Pcf_SMPolCtrl_PccRule{"service": &updated},
			TraffContDecs: map[string]*models.Pcf_SMPolCtrl_TrafficControlData{tcID: tc},
		}))
		require.Equal(t, qfi, sm.PCCRules["service"].QFI)
		require.Same(t, flow, sm.AdditionalQosFlows[qfi])
		sm.PostRemoveDataPath()
		assertMarkingQFI(t, sm.PCCRules["service"].Datapath, qfi)
	}
	partial := tiPCC("partial", "service-qos", "mec", 99)
	partial.FlowInfos[0].FlowDescription = "permit out ip from 10.100.1.0/24 to assigned"
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"partial": partial},
		TraffContDecs: map[string]*models.Pcf_SMPolCtrl_TrafficControlData{
			"mec": {TcId: "mec", RouteToLocs: []models.RouteToLocation{{Dnai: "mec"}}},
		},
	}))
	require.Equal(t, qfi, sm.PCCRules["partial"].QFI)
	require.Same(t, flow, sm.AdditionalQosFlows[qfi])
	require.Equal(t, smf_context.QoSFlowSet, flow.State)
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"partial": nil},
	}))
	sm.PostRemoveDataPath()
	for node := sm.PCCRules["service"].Datapath.FirstDPNode; node != nil; node = node.Next() {
		for _, qer := range node.DownLinkTunnel.PDR.QER {
			require.Same(t, qer, node.UPF.GetQERById(qer.QERID), "live service QER must survive TI withdrawal")
		}
	}
	assertMarkingQFI(t, sm.PCCRules["service"].Datapath, qfi)
	old := sm.PCCRules["service"].Datapath
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"service": nil},
	}))
	sm.PostRemoveDataPath()
	require.NotContains(t, sm.AdditionalQosFlows, qfi)
	for node := old.FirstDPNode; node != nil; node = node.Next() {
		for _, qer := range node.DownLinkTunnel.PDR.QER {
			if qer.QFI != nil && qer.QFI.QFI == qfi {
				require.Nil(t, node.UPF.GetQERById(qer.QERID), "last owner must release its QER")
			}
		}
	}
}

func TestTIDefaultPathReplacementKeepsMarking(t *testing.T) {
	sm := newTIPolicyContext(t)
	rule := installDefault(t, sm)
	old := sm.PCCRules["default"].Datapath
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"default": rule},
	}))
	replacement := sm.PCCRules["default"].Datapath
	require.NotSame(t, old, replacement)
	require.False(t, old.IsDefaultPath)
	require.Same(t, replacement, sm.Tunnel.DataPathPool.GetDefaultPath())
	assertMarkingQFI(t, replacement, sm.SelectedSessionRule().DefQosQFI)
	sm.PostRemoveDataPath()
	require.Same(t, replacement, sm.Tunnel.DataPathPool.GetDefaultPath())
	assertMarkingQFI(t, replacement, sm.SelectedSessionRule().DefQosQFI)
}

func TestTIAllFiltersAndDirectionsReachPDRs(t *testing.T) {
	sm := newTIPolicyContext(t)
	sm.UrrReportTime = time.Second
	installDefault(t, sm)
	rule := tiPCC("ti", "default-ref", "", 10)
	rule.FlowInfos = []models.Pcf_SMPolCtrl_FlowInformation{
		{FlowDescription: "permit out ip from 10.100.1.0/24 to assigned", FlowDirection: "DOWNLINK"},
		{FlowDescription: "permit out ip from 10.100.2.0/24 to assigned", FlowDirection: "UPLINK"},
		{
			FlowDescription: "permit out 17 from 10.100.3.0/24 53 to assigned", FlowDirection: "BIDIRECTIONAL",
			TosTrafficClass: "fcff", Spi: "00000042", FlowLabel: "000123",
		},
	}
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"ti": rule},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
			"default-ref": {QosId: "default-ref", DefQosFlowIndication: true},
		},
	}))
	for node := sm.PCCRules["ti"].Datapath.FirstDPNode; node != nil; node = node.Next() {
		for i, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
			require.Len(t, tunnel.AdditionalPDRs, 1)
			want := rule.FlowInfos[1-i].FlowDescription
			require.Equal(t, want, string(tunnel.PDR.PDI.SDFFilter.FlowDescription))
			extra := tunnel.AdditionalPDRs[0]
			require.NotEqual(t, tunnel.PDR.PDRID, extra.PDRID)
			require.Same(t, tunnel.PDR.FAR, extra.FAR)
			require.Equal(t, tunnel.PDR.QER, extra.QER)
			require.Equal(t, []byte{0xfc, 0xff}, extra.PDI.SDFFilter.TosTrafficClass)
			require.Equal(t, rule.FlowInfos[2].FlowDescription, string(extra.PDI.SDFFilter.FlowDescription))
		}
	}
	old := sm.PCCRules["ti"].Datapath
	bad := tiPCC("ti", "default-ref", "", 10)
	bad.FlowInfos = append(bad.FlowInfos, models.Pcf_SMPolCtrl_FlowInformation{FlowDescription: "bad filter"})
	require.Error(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"ti": bad},
	}))
	require.Same(t, old, sm.PCCRules["ti"].Datapath)
	require.Empty(t, sm.DataPathToBeRemoved)
	rule.FlowInfos = rule.FlowInfos[:1] // Only downlink: no filterless uplink catch-all.
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"ti": rule},
	}))
	require.True(t, sm.PCCRules["ti"].Datapath.FirstDPNode.UpLinkTunnel.PDR.Disabled)
	sm.PostRemoveDataPath()
	for _, node := range smf_context.GetSelf().UserPlaneInformation.UPFs {
		session := sm.PFCPContext[node.UPF.NodeID.ResolveNodeIdToIp().String()]
		require.NotNil(t, session)
		counts := make(map[uint32]int)
		for _, pdr := range session.PDRs {
			for _, urr := range pdr.URR {
				counts[urr.URRID]++
			}
		}
		require.NotEmpty(t, counts)
		for id, entry := range sm.UrrTable[node.UPF.UUID()] {
			require.Equal(t, counts[id], entry.RefCount)
			if counts[id] > 0 {
				require.NotEqual(t, smf_context.RULE_REMOVE, entry.State)
			}
		}
	}
}

func TestTIDefaultIndicationUsesSessionQFI(t *testing.T) {
	sm := newTIPolicyContext(t)
	sm.SelectedSessionRule().DefQosQFI = 7
	installDefault(t, sm)
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"ti": tiPCC("ti", "default-ref", "", 10)},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
			"default-ref": {QosId: "default-ref", DefQosFlowIndication: true},
		},
	}))
	require.Equal(t, uint8(7), sm.PCCRules["ti"].QFI)
	assertMarkingQFI(t, sm.PCCRules["ti"].Datapath, 7)
	require.Empty(t, sm.AdditionalQosFlows)
	for i := 0; i < 8; i++ {
		qfi := sm.AssignQFI(string(rune('a' + i)))
		require.NotEqual(t, uint8(7), qfi, "default QFI must stay reserved")
	}
}
