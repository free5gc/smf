package context

import (
	"sort"

	"github.com/free5gc/openapi/models"
)

// PCC Rule Error Report, TS 29.512 clauses 4.2.3.2 and 4.2.4.15.

// PccRuleError is the failure of the SMF to install, activate or modify the PCC rules of
// an SM policy decision. RuleReports identifies the affected PCC rules for the PCF.
type PccRuleError struct {
	RuleReports []models.Pcf_SMPolCtrl_RuleReport
	err         error
}

func (e *PccRuleError) Error() string { return e.err.Error() }

func (e *PccRuleError) Unwrap() error { return e.err }

// ruleFailure is the failure of one PCC rule, with the failure code reported to the PCF.
type ruleFailure struct {
	code models.Pcf_SMPolCtrl_FailureCode
	err  error
}

// newPccRuleError reports the PCC rules of decision that are not enforced because the PCC rule
// failedID failed with failure. The SMF enforces the PCC rules of a decision all together, so
// that the other PCC rules provisioned in decision are not enforced either. A PCC rule that was
// installed before keeps its previous definition (ruleStatus ACTIVE); a new PCC rule is not
// installed (ruleStatus INACTIVE).
func (c *SMContext) newPccRuleError(
	decision *models.Pcf_SMPolCtrl_SmPolicyDecision, failedID string, failure ruleFailure,
) *PccRuleError {
	status := func(id string) models.Pcf_SMPolCtrl_RuleStatus {
		if c.PCCRules[id] != nil {
			return models.Pcf_SMPolCtrl_RuleStatus_ACTIVE
		}
		return models.Pcf_SMPolCtrl_RuleStatus_INACTIVE
	}

	reports := []models.Pcf_SMPolCtrl_RuleReport{{
		PccRuleIds:  []string{failedID},
		RuleStatus:  status(failedID),
		FailureCode: failure.code,
	}}

	var others []string
	for id, rule := range decision.PccRules {
		if rule != nil && id != failedID {
			others = append(others, id)
		}
	}
	sort.Strings(others)
	byStatus := make(map[models.Pcf_SMPolCtrl_RuleStatus][]string)
	for _, id := range others {
		byStatus[status(id)] = append(byStatus[status(id)], id)
	}
	for _, st := range []models.Pcf_SMPolCtrl_RuleStatus{
		models.Pcf_SMPolCtrl_RuleStatus_ACTIVE, models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
	} {
		if ids := byStatus[st]; len(ids) != 0 {
			reports = append(reports, models.Pcf_SMPolCtrl_RuleReport{
				PccRuleIds:  ids,
				RuleStatus:  st,
				FailureCode: models.Pcf_SMPolCtrl_FailureCode_RES_ALLO_FAIL,
			})
		}
	}
	return &PccRuleError{RuleReports: reports, err: failure.err}
}

// flowInfoFailureCode returns the failure code of a PCC rule whose packet filters cannot be applied.
func flowInfoFailureCode(pcc *PCCRule) models.Pcf_SMPolCtrl_FailureCode {
	switch {
	case len(pcc.FlowInfos) != 0:
		return models.Pcf_SMPolCtrl_FailureCode_INCOR_FLOW_INFO
	case pcc.AppId != "":
		return models.Pcf_SMPolCtrl_FailureCode_APP_ID_ERR
	default:
		return models.Pcf_SMPolCtrl_FailureCode_MISS_FLOW_INFO
	}
}

// recordFailedQosFlow keeps a QoS flow the AN failed to set up or modify, to report its PCC rules.
func (c *SMContext) recordFailedQosFlow(qfi uint8) {
	c.failedQosFlows = append(c.failedQosFlows, qfi)
}

// TakeQosFlowFailureReports returns the reports of the PCC rules bound to the QoS flows the AN
// failed to set up or modify since the last call: resource allocation was unsuccessful, so the
// PCC rules are not enforced (TS 29.512 clause 4.2.4.15).
func (c *SMContext) TakeQosFlowFailureReports() []models.Pcf_SMPolCtrl_RuleReport {
	if len(c.failedQosFlows) == 0 {
		return nil
	}
	failed := make(map[uint8]bool, len(c.failedQosFlows))
	for _, qfi := range c.failedQosFlows {
		failed[qfi] = true
	}
	c.failedQosFlows = nil

	var ids []string
	for id, pcc := range c.PCCRules {
		if pcc != nil && pcc.QFI != 0 && failed[pcc.QFI] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	return []models.Pcf_SMPolCtrl_RuleReport{{
		PccRuleIds:  ids,
		RuleStatus:  models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
		FailureCode: models.Pcf_SMPolCtrl_FailureCode_RES_ALLO_FAIL,
	}}
}
