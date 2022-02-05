package consumer

import (
	"context"
	"fmt"
	"regexp"

	"github.com/pkg/errors"

	"github.com/free5gc/nas/nasConvert"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

// SendSMPolicyAssociationCreate create the session management association to the PCF
func SendSMPolicyAssociationCreate(smContext *smf_context.SMContext) (string, *models.SmPolicyDecision, error) {
	if smContext.SMPolicyClient == nil {
		return "", nil, errors.Errorf("smContext not selected PCF")
	}

	smPolicyData := models.SmPolicyContextData{}

	smPolicyData.Supi = smContext.Supi
	smPolicyData.PduSessionId = smContext.PDUSessionID
	smPolicyData.NotificationUri = fmt.Sprintf("%s://%s:%d/nsmf-callback/sm-policies/%s",
		smf_context.SMF_Self().URIScheme,
		smf_context.SMF_Self().RegisterIPv4,
		smf_context.SMF_Self().SBIPort,
		smContext.Ref,
	)
	smPolicyData.Dnn = smContext.Dnn
	smPolicyData.PduSessionType = nasConvert.PDUSessionTypeToModels(smContext.SelectedPDUSessionType)
	smPolicyData.AccessType = smContext.AnType
	smPolicyData.RatType = smContext.RatType
	smPolicyData.Ipv4Address = smContext.PDUAddress.To4().String()
	smPolicyData.SubsSessAmbr = smContext.DnnConfiguration.SessionAmbr
	smPolicyData.SubsDefQos = smContext.DnnConfiguration.Var5gQosProfile
	smPolicyData.SliceInfo = smContext.Snssai
	smPolicyData.ServingNetwork = &models.NetworkId{
		Mcc: smContext.ServingNetwork.Mcc,
		Mnc: smContext.ServingNetwork.Mnc,
	}
	smPolicyData.SuppFeat = "F"

	var smPolicyID string
	var smPolicyDecision *models.SmPolicyDecision
	if smPolicyDecisionFromPCF, httpRsp, err := smContext.SMPolicyClient.
		DefaultApi.SmPoliciesPost(context.Background(), smPolicyData); err != nil {
		return "", nil, err
	} else {
		smPolicyDecision = &smPolicyDecisionFromPCF

		loc := httpRsp.Header.Get("Location")
		if smPolicyID = extractSMPolicyIDFromLocation(loc); len(smPolicyID) == 0 {
			return "", nil, fmt.Errorf("SMPolicy ID parse failed")
		}
	}

	return smPolicyID, smPolicyDecision, nil
}

var smPolicyRegexp = regexp.MustCompile(`http[s]?\://.*/npcf-smpolicycontrol/v\d+/sm-policies/(.*)`)

func extractSMPolicyIDFromLocation(location string) string {
	match := smPolicyRegexp.FindStringSubmatch(location)
	if len(match) > 1 {
		return match[1]
	}
	// not match submatch
	return ""
}

func SendSMPolicyAssociationTermination(smContext *smf_context.SMContext) error {
	if smContext.SMPolicyClient == nil {
		return errors.Errorf("smContext not selected PCF")
	}

	if _, err := smContext.SMPolicyClient.DefaultApi.SmPoliciesSmPolicyIdDeletePost(
		context.Background(), smContext.SMPolicyID, models.SmPolicyDeleteData{}); err != nil {
		return fmt.Errorf("SM Policy termination failed: %v", err)
	}

	return nil
}
