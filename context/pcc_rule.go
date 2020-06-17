package context

import "free5gc/lib/openapi/models"

// PCCRule - Policy and Charging Rule
type PCCRule struct {
	// shall include attribute
	PCCRuleID  string
	Precedence int32

	// maybe include attribute
	AppID     string
	FlowInfos []models.FlowInformation

	// Reference Data
	RefTrafficControlData *TrafficControlData
}

// NewPCCRuleFromModel - create PCC rule from OpenAPI models
func NewPCCRuleFromModel(pccModel *models.PccRule) *PCCRule {
	if pccModel == nil {
		return nil
	}
	pccRule := new(PCCRule)

	pccRule.PCCRuleID = pccModel.PccRuleId
	pccRule.Precedence = pccModel.Precedence
	pccRule.AppID = pccModel.AppId
	pccRule.FlowInfos = pccModel.FlowInfos

	return pccRule
}
