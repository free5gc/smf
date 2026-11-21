package context

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
)

// The PCC rules of a decision are enforced all together: when one fails, the others are not
// enforced either. A PCC rule installed before keeps its previous definition.
func TestNewPccRuleErrorReportsTheDecision(t *testing.T) {
	c := &SMContext{PCCRules: map[string]*PCCRule{
		"PccRuleId-1": NewPCCRule(&models.Pcf_SMPolCtrl_PccRule{PccRuleId: "PccRuleId-1"}),
		"PccRuleId-4": NewPCCRule(&models.Pcf_SMPolCtrl_PccRule{PccRuleId: "PccRuleId-4"}),
	}}
	decision := &models.Pcf_SMPolCtrl_SmPolicyDecision{PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
		"PccRuleId-1": {PccRuleId: "PccRuleId-1"},
		"PccRuleId-2": {PccRuleId: "PccRuleId-2"},
		"PccRuleId-3": {PccRuleId: "PccRuleId-3"},
		"PccRuleId-4": nil, // removed
		"PccRuleId-5": {PccRuleId: "PccRuleId-5"},
	}}
	cause := errors.New("no QFI available")

	err := c.newPccRuleError(decision, "PccRuleId-3",
		ruleFailure{code: models.Pcf_SMPolCtrl_FailureCode_MAX_NR_QO_S_FLOW, err: cause})

	require.ErrorIs(t, err, cause)
	require.Equal(t, []models.Pcf_SMPolCtrl_RuleReport{
		{
			PccRuleIds:  []string{"PccRuleId-3"},
			RuleStatus:  models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
			FailureCode: models.Pcf_SMPolCtrl_FailureCode_MAX_NR_QO_S_FLOW,
		},
		{
			PccRuleIds:  []string{"PccRuleId-1"},
			RuleStatus:  models.Pcf_SMPolCtrl_RuleStatus_ACTIVE,
			FailureCode: models.Pcf_SMPolCtrl_FailureCode_RES_ALLO_FAIL,
		},
		{
			PccRuleIds:  []string{"PccRuleId-2", "PccRuleId-5"},
			RuleStatus:  models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
			FailureCode: models.Pcf_SMPolCtrl_FailureCode_RES_ALLO_FAIL,
		},
	}, err.RuleReports)
}

func TestFlowInfoFailureCode(t *testing.T) {
	withFlow := NewPCCRule(&models.Pcf_SMPolCtrl_PccRule{
		FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{FlowDescription: "permit out ip from any to assigned"}},
	})
	withApp := NewPCCRule(&models.Pcf_SMPolCtrl_PccRule{AppId: "app"})
	withNone := NewPCCRule(&models.Pcf_SMPolCtrl_PccRule{})

	require.Equal(t, models.Pcf_SMPolCtrl_FailureCode_INCOR_FLOW_INFO, flowInfoFailureCode(withFlow))
	require.Equal(t, models.Pcf_SMPolCtrl_FailureCode_APP_ID_ERR, flowInfoFailureCode(withApp))
	require.Equal(t, models.Pcf_SMPolCtrl_FailureCode_MISS_FLOW_INFO, flowInfoFailureCode(withNone))
}

func TestTakeQosFlowFailureReports(t *testing.T) {
	bound := func(id string, qfi uint8) *PCCRule {
		pcc := NewPCCRule(&models.Pcf_SMPolCtrl_PccRule{PccRuleId: id})
		pcc.SetQFI(qfi)
		return pcc
	}
	c := &SMContext{PCCRules: map[string]*PCCRule{
		"PccRuleId-1": bound("PccRuleId-1", 1),
		"PccRuleId-2": bound("PccRuleId-2", 2),
		"PccRuleId-3": bound("PccRuleId-3", 2),
	}}
	require.Nil(t, c.TakeQosFlowFailureReports())

	c.recordFailedQosFlow(2)
	c.recordFailedQosFlow(9) // bound to no PCC rule
	require.Equal(t, []models.Pcf_SMPolCtrl_RuleReport{{
		PccRuleIds:  []string{"PccRuleId-2", "PccRuleId-3"},
		RuleStatus:  models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
		FailureCode: models.Pcf_SMPolCtrl_FailureCode_RES_ALLO_FAIL,
	}}, c.TakeQosFlowFailureReports())
	require.Nil(t, c.TakeQosFlowFailureReports(), "reported once")

	c.recordFailedQosFlow(9)
	require.Nil(t, c.TakeQosFlowFailureReports())
}
