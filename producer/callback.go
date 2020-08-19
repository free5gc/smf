package producer

import (
	"context"
	"free5gc/lib/openapi/Nsmf_EventExposure"
	"free5gc/lib/openapi/models"
	smf_context "free5gc/src/smf/context"
	"free5gc/src/smf/handler/message"
	"free5gc/src/smf/logger"
	"net/http"
	"reflect"
	"strings"
)

func HandleSMPolicyUpdateNotify(rspChan chan message.HandlerResponseMessage, smContextRef string, request models.SmPolicyNotification) {
	decision := request.SmPolicyDecision
	smContext := smf_context.GetSMContext(smContextRef)
	if smContext == nil {
		logger.PduSessLog.Errorf("SMContext[%s] not found", smContextRef)
		message.SendHttpResponseMessage(rspChan, nil, http.StatusBadRequest, nil)
		return
	}

	//TODO: Response data type -
	//[200 OK] UeCampingRep
	//[200 OK] array(PartialSuccessReport)
	//[400 Bad Request] ErrorReport
	message.SendHttpResponseMessage(rspChan, nil, http.StatusNoContent, nil)

	ApplySmPolicyFromDecision(smContext, decision)
}

func SendUpPathChgEventExposureNotification(chgEvent *models.UpPathChgEvent, chgType string, sourceTR, targetTR *models.RouteToLocation) {
	notification := models.NsmfEventExposureNotification{
		NotifId: chgEvent.NotifCorreId,
		EventNotifs: []models.EventNotification{
			{
				Event:            models.SmfEvent_UP_PATH_CH,
				DnaiChgType:      models.DnaiChangeType(chgType),
				SourceTraRouting: sourceTR,
				TargetTraRouting: targetTR,
			},
		},
	}
	if sourceTR.Dnai != targetTR.Dnai {
		notification.EventNotifs[0].SourceDnai = sourceTR.Dnai
		notification.EventNotifs[0].TargetDnai = targetTR.Dnai
	}
	//TODO: sourceUeIpv4Addr, sourceUeIpv6Prefix, targetUeIpv4Addr, targetUeIpv6Prefix

	if chgEvent.NotificationUri != "" && strings.Contains(string(chgEvent.DnaiChgType), chgType) {
		logger.PduSessLog.Tracef("Send UpPathChg Event Exposure Notification [%s]", chgType)
		configuration := Nsmf_EventExposure.NewConfiguration()
		client := Nsmf_EventExposure.NewAPIClient(configuration)
		_, httpResponse, err := client.DefaultCallbackApi.SmfEventExposureNotification(context.Background(), chgEvent.NotificationUri, notification)
		if err != nil {
			if httpResponse != nil {
				logger.PduSessLog.Warnf("SMF Event Exposure Notification Error[%s]", httpResponse.Status)
			} else {
				logger.PduSessLog.Warnf("SMF Event Exposure Notification Failed[%s]", err.Error())
			}
			return
		} else if httpResponse == nil {
			logger.PduSessLog.Warnln("SMF Event Exposure Notification Failed[HTTP Response is nil]")
			return
		}
		if httpResponse.StatusCode != http.StatusOK && httpResponse.StatusCode != http.StatusNoContent {
			logger.PduSessLog.Warnf("SMF Event Exposure Notification Failed")
		} else {
			logger.PduSessLog.Tracef("SMF Event Exposure Notification Success")
		}
	}
}

func handleSessionRule(smContext *smf_context.SMContext, id string, sessionRuleModel *models.SessionRule) {
	if sessionRuleModel == nil {
		logger.PduSessLog.Debugf("Delete SessionRule[%s]", id)
		delete(smContext.SessionRules, id)
	} else {
		sessRule := smf_context.NewSessionRuleFromModel(sessionRuleModel)
		// Session rule installation
		if oldSessRule, exist := smContext.SessionRules[id]; !exist {
			logger.PduSessLog.Debugf("Install SessionRule[%s]", id)
			smContext.SessionRules[id] = sessRule
		} else { // Session rule modification
			logger.PduSessLog.Debugf("Modify SessionRule[%s]", oldSessRule.SessionRuleID)
			smContext.SessionRules[id] = sessRule
		}
	}
}

func ApplySmPolicyFromDecision(smContext *smf_context.SMContext, decision *models.SmPolicyDecision) error {
	selectedSessionRule := smContext.SelectedSessionRule()
	if selectedSessionRule == nil { //No active session rule
		//Update session rules from decision
		for id, sessRuleModel := range decision.SessRules {
			handleSessionRule(smContext, id, &sessRuleModel)
		}
		for id := range smContext.SessionRules {
			// Randomly choose a session rule to activate
			smf_context.SetSessionRuleActivateState(smContext.SessionRules[id], true)
			break
		}
	} else {
		selectedSessionRuleID := selectedSessionRule.SessionRuleID
		//Update session rules from decision
		for id, sessRuleModel := range decision.SessRules {
			handleSessionRule(smContext, id, &sessRuleModel)
		}
		if _, exist := smContext.SessionRules[selectedSessionRuleID]; !exist {
			//Original active session rule is deleted; choose again
			for id := range smContext.SessionRules {
				// Randomly choose a session rule to activate
				smf_context.SetSessionRuleActivateState(smContext.SessionRules[id], true)
				break
			}
		} else {
			//Activate original active session rule
			smf_context.SetSessionRuleActivateState(smContext.SessionRules[selectedSessionRuleID], true)
		}
	}

	for id, pccRuleModel := range decision.PccRules {
		pccRule, exist := smContext.PCCRules[id]
		//TODO: Change PccRules map[string]PccRule to map[string]*PccRule
		if &pccRuleModel == nil {
			logger.PduSessLog.Debugf("Remove PCCRule[%s]", id)
			if !exist {
				logger.PduSessLog.Errorf("pcc rule [%s] not exist", id)
				continue
			}
			refTcData := pccRule.RefTrafficControlData
			delete(refTcData.RefedPCCRule, pccRule.PCCRuleID)
			delete(smContext.PCCRules, id)
		} else {
			if exist {
				logger.PduSessLog.Debugf("Modify PCCRule[%s]", id)
			} else {
				logger.PduSessLog.Debugf("Install PCCRule[%s]", id)
			}

			newPccRule := smf_context.NewPCCRuleFromModel(&pccRuleModel)

			updatePccRule, updateTcData, trChanged := false, false, false
			var sourceTraRouting, targetTraRouting models.RouteToLocation
			var tcModel models.TrafficControlData
			//Set reference to traffic control data
			if len(pccRuleModel.RefTcData) != 0 && pccRuleModel.RefTcData[0] != "" {
				refTcID := pccRuleModel.RefTcData[0]
				tcModel = decision.TraffContDecs[refTcID]
				newTcData := smf_context.NewTrafficControlDataFromModel(&tcModel)
				newPccRule.RefTrafficControlData = newTcData

				//TODO: Fix always choosing the first RouteToLocs as targetTraRouting
				targetTraRouting = newTcData.RouteToLocs[0]

				sourceTcData, exist := smContext.TrafficControlPool[refTcID]
				if exist {
					//TODO: Fix always choosing the first RouteToLocs as sourceTraRouting
					sourceTraRouting = sourceTcData.RouteToLocs[0]
					if reflect.DeepEqual(sourceTraRouting, targetTraRouting) {
						trChanged, updateTcData, updatePccRule = true, true, true
					} else if reflect.DeepEqual(sourceTcData, newTcData) {
						updateTcData, updatePccRule = true, true
					}
				} else { //No sourceTcData, get related info from SMContext
					//TODO: Get the source DNAI
					sourceTraRouting.Dnai = ""
					sourceTraRouting.RouteInfo.Ipv4Addr = smContext.PDUAddress.String()
					//TODO: Get the port from API
					sourceTraRouting.RouteInfo.PortNumber = 2152
					trChanged, updateTcData, updatePccRule = true, true, true
				}

				if updateTcData {
					newTcData.RefedPCCRule[id] = newPccRule
					smContext.TrafficControlPool[refTcID] = newTcData
				}
			}
			if updatePccRule == false && reflect.DeepEqual(pccRule, newPccRule) {
				updatePccRule = true
			}
			if trChanged {
				//Send Notification to NEF/AF if UP path change type contains "EARLY"
				SendUpPathChgEventExposureNotification(tcModel.UpPathChgEvent, "EARLY", &sourceTraRouting, &targetTraRouting)
			}
			if updatePccRule {
				smContext.PCCRules[id] = newPccRule
				//TODO: Update to UPF
			}
			if trChanged {
				//Send Notification to NEF/AF if UP path change type contains "LATE"
				SendUpPathChgEventExposureNotification(tcModel.UpPathChgEvent, "LATE", &sourceTraRouting, &targetTraRouting)
			}
		}
	}
	return nil
}
