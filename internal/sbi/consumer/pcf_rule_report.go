package consumer

import (
	"fmt"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/pcf/SMPolCtrl"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/util"
)

// SendSMPolicyAssociationUpdateByRuleReports reports the PCC rules the SMF failed to install,
// activate or enforce (PCC Rule Error Report, TS 29.512 clause 4.2.4.15). It takes copies of
// the PCF profile and SM policy ID so that it can run without holding the SM context lock.
func (s *npcfService) SendSMPolicyAssociationUpdateByRuleReports(
	pcfProfile *models.Nrf_NFDisc_NFProfile, smPolicyID string,
	ruleReports []models.Pcf_SMPolCtrl_RuleReport,
) error {
	pcfURI := util.SearchNFServiceUri(pcfProfile, models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL)
	client := s.getSMPolicyControlClient(pcfURI)
	if client == nil {
		return fmt.Errorf("SM Policy Control PCF API root unavailable")
	}

	ctx, _, err := smf_context.GetSelf().
		GetTokenCtxForNFInstance(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL,
			models.Nrf_NFMgmt_NFType_PCF, pcfProfile.NfInstanceId)
	if err != nil {
		return err
	}

	request := &SMPolCtrl.UpdateSMPolicyRequest{
		SmPolicyId: &smPolicyID,
		RequestBody: &models.Pcf_SMPolCtrl_SmPolicyUpdateContextData{
			RuleReports: ruleReports,
		},
	}
	// The PCF removes the reported PCC rules (TS 29.512 clause 4.2.4.15): the returned decision
	// is not enforced again.
	if _, err = client.IndividualSMPolicyDocumentApi.UpdateSMPolicy(ctx, request); err != nil {
		return fmt.Errorf("update sm policy [%s] with PCC rule reports failed: %s", smPolicyID, err)
	}
	return nil
}
