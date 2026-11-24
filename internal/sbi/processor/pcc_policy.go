package processor

import (
	"errors"
	"fmt"
	"maps"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

// TS 29.512 V17.11.0 clause 4.2.4.15: validation failures in a PCF
// create/update response are reported in a subsequent SM policy update.
// Valid siblings remain installed even if delivery of the report fails.
func (p *Processor) applyPccRulesAndReport(
	sm *smf_context.SMContext, decision *models.Pcf_SMPolCtrl_SmPolicyDecision,
) error {
	current := decision
	// Bound corrective exchanges if the PCF repeatedly returns invalid rules.
	// This is a local guard, not a retry policy or a 3GPP-specified limit.
	const maxCorrectiveExchanges = 3
	for round := 0; ; round++ {
		err := sm.ApplyPccRules(current)
		if err == nil {
			return nil
		}
		var failures *smf_context.PCCRuleValidationError
		if !errors.As(err, &failures) {
			return err
		}
		if round == maxCorrectiveExchanges {
			return fmt.Errorf("PCF corrective PCC decisions exceeded %d exchanges", maxCorrectiveExchanges)
		}
		sm.Log.Warnf("PCC classifier validation: %+v", failures.RuleReports)
		response, reportErr := p.Consumer().SendSMPolicyRuleReport(sm, failures.RuleReports)
		if reportErr != nil {
			return reportErr
		}
		if response == nil {
			return nil
		}
		if err = sm.ApplySessionRules(response); err != nil {
			return err
		}
		// The UE modification caller builds its NAS rules from decision after
		// this call. Include corrective PCC operations in that same decision.
		if decision.PccRules == nil {
			decision.PccRules = make(map[string]*models.Pcf_SMPolCtrl_PccRule)
		}
		maps.Copy(decision.PccRules, response.PccRules)
		current = response
	}
}
