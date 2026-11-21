package context

import (
	"slices"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
)

// Access network information (NetLoc) reported to the PCF, TS 29.512 clauses 4.2.4.9 and 4.2.5.2.

// SmPolicySuppFeat lists the Npcf_SMPolicyControl features supported by the SMF
// (TS 29.512 clause 5.8): features 1 to 4 and NetLoc (6).
const SmPolicySuppFeat = "2F"

// netLocFeature is the number of the "NetLoc" feature of Npcf_SMPolicyControl (TS 29.512 clause 5.8).
const netLocFeature = 6

// RecordSmPolicyDecision keeps what the SMF needs to know about access network information
// reporting from an SM policy decision: the features negotiated with the PCF (in the decision
// creating the SM policy association) and whether the AN_INFO policy control request trigger is set.
func (c *SMContext) RecordSmPolicyDecision(decision *models.Pcf_SMPolCtrl_SmPolicyDecision) {
	if decision == nil {
		return
	}
	if decision.SuppFeat != "" {
		c.SmPolicySuppFeat = decision.SuppFeat
	}
	// Provided triggers replace the previously provided ones.
	if decision.PolicyCtrlReqTriggers != nil {
		c.AnInfoTriggerSet = slices.Contains(decision.PolicyCtrlReqTriggers,
			models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_AN_INFO)
	}
}

// NetLocSupported reports whether the NetLoc feature is negotiated with the PCF.
func (c *SMContext) NetLocSupported() bool {
	suppFeat, err := openapi.NewSupportedFeature(c.SmPolicySuppFeat)
	return err == nil && suppFeat.GetFeature(netLocFeature)
}

// UpdateAccessNetworkInfo keeps the UE location and time zone received from the AMF current.
func (c *SMContext) UpdateAccessNetworkInfo(ueLocation *models.UserLocation, ueTimeZone string) {
	if c.Smf_PDUSess_SmContextCreateData == nil {
		return
	}
	if ueLocation != nil {
		c.UeLocation = ueLocation
	}
	if ueTimeZone != "" {
		c.UeTimeZone = ueTimeZone
	}
}

// PolicyUserLocation returns the UE location to provide to the PCF, or nil if not available.
func (c *SMContext) PolicyUserLocation() *models.UserLocation {
	if c.Smf_PDUSess_SmContextCreateData == nil || c.UeLocation == nil {
		return nil
	}
	ueLocation := *c.UeLocation
	if ueLocation.NrLocation != nil && ueLocation.NrLocation.AgeOfLocationInformation < 0 {
		nrLoc := *ueLocation.NrLocation
		nrLoc.AgeOfLocationInformation = 0
		ueLocation.NrLocation = &nrLoc
	}
	return &ueLocation
}

// PolicyUeTimeZone returns the UE time zone to provide to the PCF, or "" if not available.
func (c *SMContext) PolicyUeTimeZone() string {
	if c.Smf_PDUSess_SmContextCreateData == nil {
		return ""
	}
	return c.UeTimeZone
}

// PolicyServingNetwork returns the serving PLMN (or SNPN) to provide to the PCF, or nil if not available.
func (c *SMContext) PolicyServingNetwork() *models.PlmnIdNid {
	if c.Smf_PDUSess_SmContextCreateData == nil || c.ServingNetwork == nil {
		return nil
	}
	return &models.PlmnIdNid{
		Mcc: c.ServingNetwork.Mcc,
		Mnc: c.ServingNetwork.Mnc,
		Nid: c.ServingNetwork.Nid,
	}
}

// AccessNetworkInfoReport returns the SM policy update reporting the access network information
// requested by the PCF in decision (TS 29.512 clause 4.2.4.9), or nil if none is requested.
// The SMF reports the information it has.
func (c *SMContext) AccessNetworkInfoReport(
	decision *models.Pcf_SMPolCtrl_SmPolicyDecision,
) *models.Pcf_SMPolCtrl_SmPolicyUpdateContextData {
	if decision == nil || !c.AnInfoTriggerSet || !c.NetLocSupported() {
		return nil
	}
	var wantLoc, wantTz bool
	for _, ruleData := range decision.LastReqRuleData {
		for _, reqData := range ruleData.ReqData {
			switch reqData {
			case models.Pcf_SMPolCtrl_RequestedRuleDataType_USER_LOC_INFO:
				wantLoc = true
			case models.Pcf_SMPolCtrl_RequestedRuleDataType_MS_TIME_ZONE:
				wantTz = true
			}
		}
	}
	if !wantLoc && !wantTz {
		return nil
	}
	report := &models.Pcf_SMPolCtrl_SmPolicyUpdateContextData{
		RepPolicyCtrlReqTriggers: []models.Pcf_SMPolCtrl_PolicyControlRequestTrigger{
			models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_AN_INFO,
		},
	}
	if wantLoc {
		if report.UserLocationInfo = c.PolicyUserLocation(); report.UserLocationInfo == nil {
			// User location not available: the serving network instead
			report.ServingNetwork = c.PolicyServingNetwork()
		}
	}
	if wantTz {
		report.UeTimeZone = c.PolicyUeTimeZone()
	}
	return report
}

// SmPolicyDeleteData returns the data provided to the PCF when terminating the SM policy
// association: the access network information if NetLoc is supported (TS 29.512 clause 4.2.5.2).
func (c *SMContext) SmPolicyDeleteData() models.Pcf_SMPolCtrl_SmPolicyDeleteData {
	var deleteData models.Pcf_SMPolCtrl_SmPolicyDeleteData
	if !c.NetLocSupported() {
		return deleteData
	}
	if deleteData.UserLocationInfo = c.PolicyUserLocation(); deleteData.UserLocationInfo == nil {
		deleteData.ServingNetwork = c.PolicyServingNetwork()
	}
	deleteData.UeTimeZone = c.PolicyUeTimeZone()
	return deleteData
}
