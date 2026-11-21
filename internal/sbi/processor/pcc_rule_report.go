package processor

import (
	"errors"
	"net/http"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

// PCC Rule Error Report, TS 29.512 clauses 4.2.3.2 and 4.2.4.15.

// pccRuleEventCause is the application error reported when the PCC rules provisioned in an
// SM policy update request cannot be installed or activated (TS 29.512 table 5.7.3-2).
const pccRuleEventCause = "PCC_RULE_EVENT"

// reportPccRuleFailures reports the PCC rules the SMF failed to install, activate or enforce to
// the PCF in an SM policy update (TS 29.512 clause 4.2.4.15). It is sent asynchronously,
// without the SM context lock held by the caller.
func (p *Processor) reportPccRuleFailures(
	smContext *smf_context.SMContext, ruleReports []models.Pcf_SMPolCtrl_RuleReport,
) {
	if len(ruleReports) == 0 {
		return
	}
	pcfProfile, smPolicyID := smContext.SelectedPCFProfile, smContext.SMPolicyID
	go func() {
		if err := p.Consumer().SendSMPolicyAssociationUpdateByRuleReports(
			&pcfProfile, smPolicyID, ruleReports); err != nil {
			logger.PduSessLog.Errorf("Report PCC rule failures failed: %v", err)
		}
	}()
}

// reportApplyPccRulesFailure reports the PCC rules of an SM policy decision that err says
// the SMF could not install, if any.
func (p *Processor) reportApplyPccRulesFailure(smContext *smf_context.SMContext, err error) {
	var ruleErr *smf_context.PccRuleError
	if errors.As(err, &ruleErr) {
		p.reportPccRuleFailures(smContext, ruleErr.RuleReports)
	}
}

// reportQosFlowFailures reports the PCC rules bound to the QoS flows the AN failed to set up
// or modify.
func (p *Processor) reportQosFlowFailures(smContext *smf_context.SMContext) {
	p.reportPccRuleFailures(smContext, smContext.TakeQosFlowFailureReports())
}

// pccRuleErrorReport returns the ErrorReport rejecting an SM policy update request whose PCC
// rules cannot be installed (TS 29.512 clause 4.2.3.2), or nil if err is not about PCC rules.
func pccRuleErrorReport(err error) *models.Pcf_SMPolCtrl_ErrorReport {
	var ruleErr *smf_context.PccRuleError
	if !errors.As(err, &ruleErr) {
		return nil
	}
	return &models.Pcf_SMPolCtrl_ErrorReport{
		Error: &models.ProblemDetails{
			Title:  "PCC rules not installed",
			Status: http.StatusBadRequest,
			Detail: ruleErr.Error(),
			Cause:  pccRuleEventCause,
		},
		RuleReports: ruleErr.RuleReports,
	}
}
