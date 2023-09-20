package producer

import (
	"context"
	"fmt"
	"net/http"

	"github.com/free5gc/openapi/Nsmf_EventExposure"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/util/httpwrapper"
)

func HandleChargingNotification(chargingNotifyRequest models.ChargingNotifyRequest,
	smContextRef string,
) *httpwrapper.Response {
	logger.ChargingLog.Info("Handle Charging Notification")

	problemDetails := chargingNotificationProcedure(chargingNotifyRequest, smContextRef)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	} else {
		return httpwrapper.NewResponse(http.StatusNoContent, nil, nil)
	}
}

// While receive Charging Notification from CHF, SMF will send Charging Information to CHF and update UPF
// The Charging Notification will be sent when CHF found the changes of the quota file.
func chargingNotificationProcedure(req models.ChargingNotifyRequest, smContextRef string) *models.ProblemDetails {
	if smContext := smf_context.GetSMContextByRef(smContextRef); smContext != nil {
		smContext.SMLock.Lock()
		defer smContext.SMLock.Unlock()
		upfUrrMap := make(map[string][]*smf_context.URR)
		for _, reauthorizeDetail := range req.ReauthorizationDetails {
			rg := reauthorizeDetail.RatingGroup
			logger.ChargingLog.Infof("Force update charging information for rating group %d", rg)
			for _, urr := range smContext.UrrUpfMap {
				chgInfo := smContext.ChargingInfo[urr.URRID]
				if chgInfo.RatingGroup == rg ||
					chgInfo.ChargingLevel == smf_context.PduSessionCharging {
					logger.ChargingLog.Tracef("Query URR (%d) for Rating Group (%d)", urr.URRID, rg)
					upfId := smContext.ChargingInfo[urr.URRID].UpfId
					upfUrrMap[upfId] = append(upfUrrMap[upfId], urr)
				}
			}
		}
		for upfId, urrList := range upfUrrMap {
			upf := smf_context.GetUpfById(upfId)
			if upf == nil {
				logger.ChargingLog.Warnf("Cound not find upf %s", upfId)
				continue
			}
			QueryReport(smContext, upf, urrList, models.TriggerType_FORCED_REAUTHORISATION)
		}
		ReportUsageAndUpdateQuota(smContext)
	} else {
		problemDetails := &models.ProblemDetails{
			Status: http.StatusNotFound,
			Cause:  "CONTEXT_NOT_FOUND",
			Detail: fmt.Sprintf("SM Context [%s] Not Found ", smContextRef),
		}
		return problemDetails
	}

	return nil
}

func HandleSMPolicyUpdateNotify(smContextRef string, request models.SmPolicyNotification) *httpwrapper.Response {
	logger.PduSessLog.Infoln("In HandleSMPolicyUpdateNotify")
	decision := request.SmPolicyDecision
	smContext := smf_context.GetSMContextByRef(smContextRef)

	if smContext == nil {
		logger.PduSessLog.Errorf("SMContext[%s] not found", smContextRef)
		httpResponse := httpwrapper.NewResponse(http.StatusBadRequest, nil, nil)
		return httpResponse
	}

	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	smContext.CheckState(smf_context.Active)
	// Wait till the state becomes Active again
	// TODO: implement waiting in concurrent architecture

	smContext.SetState(smf_context.ModificationPending)

	// Update SessionRule from decision
	if err := smContext.ApplySessionRules(decision); err != nil {
		// TODO: Fill the error body
		smContext.Log.Errorf("SMPolicyUpdateNotify err: %v", err)
		return httpwrapper.NewResponse(http.StatusBadRequest, nil, nil)
	}

	//TODO: Response data type -
	//[200 OK] UeCampingRep
	//[200 OK] array(PartialSuccessReport)
	//[400 Bad Request] ErrorReport
	if err := smContext.ApplyPccRules(decision); err != nil {
		smContext.Log.Errorf("apply sm policy decision error: %+v", err)
		// TODO: Fill the error body
		return httpwrapper.NewResponse(http.StatusBadRequest, nil, nil)
	}

	smContext.SendUpPathChgNotification("EARLY", SendUpPathChgEventExposureNotification)

	ActivateUPFSession(smContext, nil)

	smContext.SendUpPathChgNotification("LATE", SendUpPathChgEventExposureNotification)

	smContext.PostRemoveDataPath()

	return httpwrapper.NewResponse(http.StatusNoContent, nil, nil)
}

func SendUpPathChgEventExposureNotification(
	uri string, notification *models.NsmfEventExposureNotification,
) {
	configuration := Nsmf_EventExposure.NewConfiguration()
	client := Nsmf_EventExposure.NewAPIClient(configuration)
	_, httpResponse, err := client.
		DefaultCallbackApi.
		SmfEventExposureNotification(context.Background(), uri, *notification)
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
	defer func() {
		if rspCloseErr := httpResponse.Body.Close(); rspCloseErr != nil {
			logger.PduSessLog.Errorf("SmfEventExposureNotification response body cannot close: %+v", rspCloseErr)
		}
	}()
	if httpResponse.StatusCode != http.StatusOK && httpResponse.StatusCode != http.StatusNoContent {
		logger.PduSessLog.Warnf("SMF Event Exposure Notification Failed")
	} else {
		logger.PduSessLog.Tracef("SMF Event Exposure Notification Success")
	}
}
