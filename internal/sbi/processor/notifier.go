package processor

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/smf/EvtExpos"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/util/metrics/sbi"
)

func (p *Processor) HandleChargingNotification(
	c *gin.Context,
	chargingNotifyRequest models.Chf_ConvCharging_ChargingNotifyRequest,
	smContextRef string,
) {
	logger.ChargingLog.Info("Handle Charging Notification")

	problemDetails := p.chargingNotificationProcedure(chargingNotifyRequest, smContextRef)
	if problemDetails == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.Set(sbi.IN_PB_DETAILS_CTX_STR, problemDetails.Cause)
	c.JSON(int(problemDetails.Status), problemDetails)
}

// While receive Charging Notification from CHF, SMF will send Charging Information to CHF and update UPF
// The Charging Notification will be sent when CHF found the changes of the quota file.
func (p *Processor) chargingNotificationProcedure(
	req models.Chf_ConvCharging_ChargingNotifyRequest, smContextRef string,
) *models.ProblemDetails {
	if smContext := smf_context.GetSMContextByRef(smContextRef); smContext != nil {
		smContext.SMLock.Lock()
		defer smContext.SMLock.Unlock()
		upfUrrMap := make(map[string][]*smf_context.URR)
		for _, reauthorizeDetail := range req.ReauthorizationDetails {
			rg := reauthorizeDetail.RatingGroup
			logger.ChargingLog.Infof("Force update charging information for rating group %d", rg)
			for upfId, entries := range smContext.UrrTable {
				for urrID, entry := range entries {
					if entry == nil || entry.Rule == nil {
						logger.ChargingLog.Errorf("URR[%d] rule not found for rating group %d", urrID, rg)
						continue
					}
					chgInfo := smContext.ChargingInfo[urrID]
					if chgInfo == nil {
						if smf_context.IsChargingRelatedUrr(entry.Rule) {
							logger.ChargingLog.Errorf("Charging-related URR[%d] missing ChargingInfo", urrID)
						} else {
							logger.ChargingLog.Tracef("Skip non-charging URR[%d] with no ChargingInfo", urrID)
						}
						continue
					}
					if chgInfo.RatingGroup == rg ||
						chgInfo.ChargingLevel == smf_context.PduSessionCharging {
						logger.ChargingLog.Tracef("Query URR (%d) for Rating Group (%d)", urrID, rg)
						upfUrrMap[upfId] = append(upfUrrMap[upfId], entry.Rule)
					}
				}
			}
		}
		for upfId, urrList := range upfUrrMap {
			upf := smf_context.GetUpfById(upfId)
			if upf == nil {
				logger.ChargingLog.Warnf("Cound not find upf %s", upfId)
				continue
			}
			p.QueryReport(smContext, upf, urrList, models.Chf_ConvCharging_TriggerType_FORCED_REAUTHORISATION)
		}
		p.ReportUsageAndUpdateQuota(smContext)
	} else {
		detail := fmt.Sprintf("SM Context [%s] Not Found ", smContextRef)
		return openapi.ProblemDetailsDataNotFound(detail)
	}

	return nil
}

func (p *Processor) HandleSMPolicyUpdateNotify(
	c *gin.Context,
	request models.Pcf_SMPolCtrl_SmPolicyNotification,
	smContextRef string,
) {
	logger.PduSessLog.Infoln("In HandleSMPolicyUpdateNotify")
	decision := request.SmPolicyDecision
	smContext := smf_context.GetSMContextByRef(smContextRef)

	if smContext == nil {
		logger.PduSessLog.Errorf("SMContext[%s] not found", smContextRef)
		c.Status(http.StatusBadRequest)
		return
	}

	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	smContext.CheckState(smf_context.Active)
	// Wait till the state becomes Active again
	// TODO: implement waiting in concurrent architecture

	previousState := smContext.State()
	smContext.SetState(smf_context.ModificationPending)
	defer smContext.SetState(previousState)

	// TS 29.512 V17.11.0 clause 4.2.6.1 note 4: a 400 rejects the
	// whole notification, including session data. Stage only the wrappers
	// ApplySessionRules mutates; restore the originals if PCC validation fails.
	oldSessionRules, oldSelected := smContext.SessionRules, smContext.SelectedSessionRuleID
	smContext.SessionRules = make(map[string]*smf_context.SessionRule, len(oldSessionRules))
	for id, rule := range oldSessionRules {
		copyRule := *rule
		smContext.SessionRules[id] = &copyRule
	}
	accepted := false
	defer func() {
		if !accepted {
			smContext.SessionRules, smContext.SelectedSessionRuleID = oldSessionRules, oldSelected
		}
	}()

	// Update SessionRule from decision
	if err := smContext.ApplySessionRules(decision); err != nil {
		// TODO: Fill the error body
		smContext.Log.Errorf("SMPolicyUpdateNotify err: %v", err)
		c.Status(http.StatusBadRequest)
		return
	}

	// TS 29.512 V17.11.0 clauses 4.2.3.2 and 4.2.3.16:
	// reject all-invalid batches; continue installing valid siblings otherwise.
	var failures *smf_context.PCCRuleValidationError
	if err := smContext.ApplyPccRules(decision); err != nil {
		smContext.Log.Errorf("apply sm policy decision error: %+v", err)
		if !errors.As(err, &failures) {
			c.Status(http.StatusBadRequest)
			return
		}
		if failures.AllRulesFailed {
			c.JSON(http.StatusBadRequest, models.Pcf_SMPolCtrl_ErrorReport{
				Error:       &models.ProblemDetails{Status: http.StatusBadRequest, Cause: "PCC_RULE_EVENT"},
				RuleReports: failures.RuleReports,
			})
			return
		}
	}
	accepted = true

	smContext.SendUpPathChgNotification("EARLY", SendUpPathChgEventExposureNotification)

	p.ActivateUPFSession(smContext, nil)

	smContext.SendUpPathChgNotification("LATE", SendUpPathChgEventExposureNotification)

	smContext.PostRemoveDataPath()

	if failures != nil {
		// Table 5.5.2.2-2 specifies an array, even for one failure cause.
		c.JSON(http.StatusOK, []models.Pcf_SMPolCtrl_PartialSuccessReport{{
			FailureCause: models.Pcf_SMPolCtrl_FailureCause_PCC_RULE_EVENT,
			RuleReports:  failures.RuleReports,
		}})
		return
	}
	c.Status(http.StatusNoContent)
}

func SendUpPathChgEventExposureNotification(
	uri string, notification *models.Smf_EvtExpos_NsmfEventExposureNotification,
) {
	configuration := EvtExpos.NewConfiguration()
	client := EvtExpos.NewAPIClient(configuration)
	request := &EvtExpos.CreateIndividualSubcriptionMyNotificationRequest{
		RequestBody: notification,
	}

	// Nsmf_EventExposure_Notify (TS 29.508 4.2.2) is a Notify operation;
	// TS 29.500 6.7.3 excludes OAuth access tokens.
	_, err := client.SubscriptionsCollectionApi.
		CreateIndividualSubcriptionMyNotification(context.Background(), uri, request)

	switch err := err.(type) {
	case openapi.GenericOpenAPIError:
		logger.PduSessLog.Warnf("SMF Event Exposure Notification Error[%s]", err.Error())
	case error:
		logger.PduSessLog.Warnf("SMF Event Exposure Notification Failed[%s]", err.Error())
	case nil:
		logger.PduSessLog.Tracef("SMF Event Exposure Notification Success")
	default:
		logger.PduSessLog.Warnf("SMF Event Exposure Notification Unknown Error: %+v", err)
	}
}
