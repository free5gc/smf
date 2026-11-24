package context

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

// PCCRuleValidationError reports classifier validation failures. Valid siblings
// have been applied unless AllRulesFailed is true. Allocation errors remain
// ordinary errors; they must not be mistaken for a partial validation success.
type PCCRuleValidationError struct {
	RuleReports    []models.Pcf_SMPolCtrl_RuleReport
	AllRulesFailed bool
}

func (e *PCCRuleValidationError) Error() string {
	return fmt.Sprintf("classifier validation failed for %d PCC rule(s)", len(e.RuleReports))
}

// TS 29.512 V17.11.0 table 5.6.2.6-1 note 3 permits appId instead of
// flowInfos. Resolve it before allocating a path, using the existing local PFD
// workaround (the UPF does not support native PFD installation).
func pccFlowInfos(rule *models.Pcf_SMPolCtrl_PccRule) ([]models.Pcf_SMPolCtrl_FlowInformation, error) {
	if len(rule.FlowInfos) != 0 {
		return rule.FlowInfos, nil
	}
	if factory.UERoutingConfig != nil {
		for _, app := range factory.UERoutingConfig.PfdDatas {
			if app != nil && app.AppID == rule.AppId && len(app.Pfds) != 0 && len(app.Pfds[0].FlowDescriptions) != 0 {
				return []models.Pcf_SMPolCtrl_FlowInformation{{
					FlowDescription: app.Pfds[0].FlowDescriptions[0],
					FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				}}, nil
			}
		}
	}
	return nil, fmt.Errorf("no PFD matched for AppID [%s]", rule.AppId)
}

func (c *SMContext) validatePCCClassifiers(decision *models.Pcf_SMPolCtrl_SmPolicyDecision) (
	map[string]*PCCRule, map[string]bool, *PCCRuleValidationError,
) {
	candidates := make(map[string]*PCCRule)
	rejected := make(map[string]bool)
	reports := &PCCRuleValidationError{}
	ids := make([]string, 0, len(decision.PccRules))
	for id := range decision.PccRules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		model := decision.PccRules[id]
		if model == nil {
			continue
		} // Rule removal must not fail (clause 4.2.3.16).
		old := c.PCCRules[id]
		candidate := NewPCCRule(model)
		// Clause 4.2.6.2.1 permits omitted attributes on modification.
		// Do not label such an update MISS_FLOW_INFO. Leave its existing
		// handling unchanged; the missing-classifier check concerns initial rules.
		if old != nil && len(model.FlowInfos) == 0 && model.AppId == "" {
			candidates[id] = candidate
			continue
		}
		var code models.Pcf_SMPolCtrl_FailureCode
		// TS 29.512 V17.11.0 table 5.6.3.9-1: distinguish unknown
		// predefined IDs, missing initial classifiers, unsupported flow information,
		// and unknown applications. Do not report every error as RES_ALLO_FAIL.
		switch {
		case old == nil && reflect.DeepEqual(*model, models.Pcf_SMPolCtrl_PccRule{
			PccRuleId: model.PccRuleId, RefCondData: model.RefCondData,
		}):
			code = models.Pcf_SMPolCtrl_FailureCode_UNK_RULE_ID
		case old == nil && len(model.FlowInfos) == 0 && model.AppId == "":
			code = models.Pcf_SMPolCtrl_FailureCode_MISS_FLOW_INFO
		case old != nil && old.AppId != "" && model.AppId != "" && old.AppId != model.AppId:
			// The provisioned appId is immutable (table 5.6.2.6-1 note 3).
			code = models.Pcf_SMPolCtrl_FailureCode_APP_ID_ERR
		default:
			infos, resolveErr := pccFlowInfos(candidate.Pcf_SMPolCtrl_PccRule)
			if resolveErr != nil {
				code = models.Pcf_SMPolCtrl_FailureCode_APP_ID_ERR
			} else if _, _, validateErr := directionalSDFFilters(infos); validateErr != nil {
				code = models.Pcf_SMPolCtrl_FailureCode_INCOR_FLOW_INFO
			}
		}
		if code == "" {
			candidates[id] = candidate
			continue
		}
		rejected[id] = true
		status := models.Pcf_SMPolCtrl_RuleStatus_INACTIVE
		if old != nil && old.Datapath != nil && old.Datapath.Activated {
			status = models.Pcf_SMPolCtrl_RuleStatus_ACTIVE
		}
		// Clauses 4.2.3.16 and 4.2.4.15: a rejected new rule is inactive;
		// an unaffected active rule remains active after a rejected modification.
		reports.RuleReports = append(reports.RuleReports, models.Pcf_SMPolCtrl_RuleReport{
			PccRuleIds: []string{id}, FailureCode: code, RuleStatus: status,
		})
	}
	reports.AllRulesFailed = len(rejected) != 0 && len(rejected) == len(decision.PccRules)
	return candidates, rejected, reports
}
