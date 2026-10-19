package context_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

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
