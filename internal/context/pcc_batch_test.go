package context_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

func batchChargingDecision() *models.Pcf_SMPolCtrl_SmPolicyDecision {
	session := tiPCC("session-charging", "default-qos", "traffic", 255)
	session.FlowInfos[0].FlowDescription = pccUpdateDefaultFlow
	session.RefChgData = []string{"charging"}
	service := tiPCC("service-charging", "service-qos", "traffic", 10)
	service.RefChgData = []string{"charging"}
	return &models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
			session.PccRuleId: session, service.PccRuleId: service,
		},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
			"default-qos": {QosId: "default-qos", DefQosFlowIndication: true},
			"service-qos": {QosId: "service-qos", Var5qi: 9, MaxbrUl: "1 Mbps", MaxbrDl: "1 Mbps"},
		},
		TraffContDecs: map[string]*models.Pcf_SMPolCtrl_TrafficControlData{"traffic": {TcId: "traffic"}},
		ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{
			"charging": {
				ChgId: "charging", RatingGroup: 42, Offline: true,
				MeteringMethod: models.Pcf_SMPolCtrl_MeteringMethod_VOLUME,
				ReportingLevel: models.Pcf_SMPolCtrl_ReportingLevel_RAT_GR_LEVEL,
			},
		},
	}
}

// TS 29.512 V17.11.0 clauses 4.2.3.16 and 4.2.4.15: a failed
// classifier must not prevent independent valid PCC rules from being installed.
func TestPCCBatchClassifierFailure(t *testing.T) {
	for _, kind := range []string{"missing", "invalid", "invalid second filter"} {
		t.Run(kind, func(t *testing.T) {
			sm := newTIPolicyContext(t)
			sm.UrrReportThreshold = 3000
			decision := batchChargingDecision()
			bad := tiPCC("bad", "default-qos", "traffic", 20)
			bad.RefChgData = []string{"charging"}
			switch kind {
			case "missing":
				bad.FlowInfos = nil
			case "invalid":
				bad.FlowInfos[0].FlowDescription = "invalid filter"
			default:
				bad.FlowInfos = append(bad.FlowInfos, models.Pcf_SMPolCtrl_FlowInformation{
					FlowDescription: "invalid filter", FlowDirection: models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				})
			}
			decision.PccRules[bad.PccRuleId] = bad
			err := sm.ApplyPccRules(decision)
			require.Error(t, err)
			require.NotContains(t, sm.PCCRules, "bad")
			for _, id := range []string{"session-charging", "service-charging"} {
				require.Contains(t, sm.PCCRules, id, "a malformed sibling must not abort valid charging rules")
				path := sm.PCCRules[id].Datapath
				require.True(t, path.Activated)
				urrs := path.GetChargingUrr(sm)
				require.NotEmpty(t, urrs)
				for _, urr := range urrs {
					require.NotNil(t, sm.ChargingInfo[urr.URRID])
				}
			}
			require.Len(t, sm.Tunnel.DataPathPool, 2, "no path should be allocated for the rejected classifier")
			require.Empty(t, sm.DataPathToBeRemoved)
			var failures *smf_context.PCCRuleValidationError
			require.ErrorAs(t, err, &failures)
			require.False(t, failures.AllRulesFailed)
			code := models.Pcf_SMPolCtrl_FailureCode_INCOR_FLOW_INFO
			if kind == "missing" {
				code = models.Pcf_SMPolCtrl_FailureCode_MISS_FLOW_INFO
			}
			require.Equal(t, []models.Pcf_SMPolCtrl_RuleReport{{
				PccRuleIds: []string{"bad"}, RuleStatus: models.Pcf_SMPolCtrl_RuleStatus_INACTIVE, FailureCode: code,
			}}, failures.RuleReports)
			require.NoError(t, sm.SelectDefaultDataPath())
			require.Len(t, sm.Tunnel.DataPathPool, 2, "do not replace the charged default with an uncharged fallback")
		})
	}
}

func TestPCCBatchKeepsFailedActiveRule(t *testing.T) {
	sm := newTIPolicyContext(t)
	sm.UrrReportThreshold = 3000
	initial := batchChargingDecision()
	require.NoError(t, sm.ApplyPccRules(initial))
	old := sm.PCCRules["session-charging"]
	urrs := old.Datapath.GetChargingUrr(sm)
	info := sm.ChargingInfo[urrs[0].URRID]
	model := *initial.PccRules["session-charging"]
	model.FlowInfos = []models.Pcf_SMPolCtrl_FlowInformation{{FlowDescription: "invalid"}}
	added := tiPCC("added", "default-qos", "traffic", 30)
	added.RefChgData = []string{"charging"}
	var failures *smf_context.PCCRuleValidationError
	err := sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"session-charging": &model, "added": added},
	})
	require.ErrorAs(t, err, &failures)
	require.False(t, failures.AllRulesFailed)
	require.Equal(t, models.Pcf_SMPolCtrl_RuleStatus_ACTIVE, failures.RuleReports[0].RuleStatus)
	require.Same(t, old, sm.PCCRules["session-charging"])
	require.Contains(t, sm.PCCRules, "added")
	sm.PostRemoveDataPath()
	require.Same(t, old.Datapath, sm.Tunnel.DataPathPool.GetDefaultPath())
	require.Equal(t, urrs, old.Datapath.GetChargingUrr(sm))
	require.Same(t, info, sm.ChargingInfo[urrs[0].URRID])
	require.NotEqual(t, smf_context.RULE_REMOVE, old.Datapath.FirstDPNode.UpLinkTunnel.PDR.State)
}

func TestPCCBatchApplicationAndPredefinedRules(t *testing.T) {
	oldConfig := factory.UERoutingConfig
	factory.UERoutingConfig = &factory.RoutingConfig{PfdDatas: []*factory.PfdDataForApp{{
		AppID: "known", Pfds: []factory.PfdContent{{FlowDescriptions: []string{pccUpdateDefaultFlow}}},
	}}}
	t.Cleanup(func() { factory.UERoutingConfig = oldConfig })
	sm := newTIPolicyContext(t)
	decision := batchChargingDecision()
	good := decision.PccRules["session-charging"]
	good.FlowInfos, good.AppId = nil, "known"
	bad := *good
	bad.PccRuleId, bad.AppId = "unknown-app", "unknown"
	decision.PccRules[bad.PccRuleId] = &bad
	decision.PccRules["unknown-predefined"] = &models.Pcf_SMPolCtrl_PccRule{PccRuleId: "unknown-predefined"}
	var failures *smf_context.PCCRuleValidationError
	require.ErrorAs(t, sm.ApplyPccRules(decision), &failures)
	require.Equal(t, []models.Pcf_SMPolCtrl_RuleReport{
		{
			PccRuleIds: []string{"unknown-app"}, FailureCode: models.Pcf_SMPolCtrl_FailureCode_APP_ID_ERR,
			RuleStatus: models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
		},
		{
			PccRuleIds: []string{"unknown-predefined"}, FailureCode: models.Pcf_SMPolCtrl_FailureCode_UNK_RULE_ID,
			RuleStatus: models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
		},
	}, failures.RuleReports)
	require.Contains(t, sm.PCCRules, good.PccRuleId)
	require.Equal(t, "known", sm.PCCRules[good.PccRuleId].AppId)
	require.Empty(t, sm.PCCRules[good.PccRuleId].FlowInfos, "PFD resolution must not rewrite the policy")
	require.NotNil(t, sm.PCCRules[good.PccRuleId].Datapath.FirstDPNode.UpLinkTunnel.PDR.PDI.SDFFilter)
	old := sm.PCCRules[good.PccRuleId]
	changed := *good
	changed.AppId = "another-app"
	require.ErrorAs(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{changed.PccRuleId: &changed},
	}), &failures)
	require.True(t, failures.AllRulesFailed)
	require.Equal(t, models.Pcf_SMPolCtrl_FailureCode_APP_ID_ERR, failures.RuleReports[0].FailureCode)
	require.Equal(t, models.Pcf_SMPolCtrl_RuleStatus_ACTIVE, failures.RuleReports[0].RuleStatus)
	require.Same(t, old, sm.PCCRules[changed.PccRuleId])
}

func TestPCCBatchAllInvalidAndDeletion(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "all invalid", true: "deletion still accepted"}[remove], func(t *testing.T) {
			sm := newTIPolicyContext(t)
			sm.UrrReportThreshold = 3000
			require.NoError(t, sm.ApplyPccRules(batchChargingDecision()))
			old := sm.PCCRules["session-charging"]
			poolSize := len(sm.Tunnel.DataPathPool)
			decision := &models.Pcf_SMPolCtrl_SmPolicyDecision{PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
				"bad": {PccRuleId: "bad", Precedence: 10},
			}}
			if remove {
				decision.PccRules["service-charging"] = nil
			}
			var failures *smf_context.PCCRuleValidationError
			require.ErrorAs(t, sm.ApplyPccRules(decision), &failures)
			require.Equal(t, !remove, failures.AllRulesFailed)
			require.Same(t, old, sm.PCCRules["session-charging"])
			require.Len(t, sm.Tunnel.DataPathPool, poolSize)
			if remove {
				require.NotContains(t, sm.PCCRules, "service-charging")
				require.Len(t, sm.DataPathToBeRemoved, 1)
			} else {
				require.Empty(t, sm.DataPathToBeRemoved)
			}
			sm.PostRemoveDataPath()
			require.Same(t, old.Datapath, sm.Tunnel.DataPathPool.GetDefaultPath())
		})
	}
}
