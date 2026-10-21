package processor

import (
	"fmt"
	"time"

	"github.com/wmnsk/go-pfcp/ie"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func (p *Processor) CreateChargingSession(smContext *smf_context.SMContext) {
	_, problemDetails, err := p.Consumer().SendConvergedChargingRequest(smContext, smf_context.CHARGING_INIT, nil)
	if problemDetails != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Init] Failed Problem[%+v]", problemDetails)
	} else if err != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Init] Error[%+v]", err)
	} else {
		logger.ChargingLog.Infof("Send Charging Data Request[Init] successfully")
	}
}

func (p *Processor) UpdateChargingSession(
	smContext *smf_context.SMContext, urrList []*smf_context.URR, trigger models.Chf_ConvCharging_Trigger,
) {
	var multipleUnitUsage []models.Chf_ConvCharging_MultipleUnitUsage

	for _, urr := range urrList {
		if chgInfo := smContext.ChargingInfo[urr.URRID]; chgInfo != nil {
			rg := chgInfo.RatingGroup
			logger.PduSessLog.Tracef("Receive Usage Report from URR[%d], correspopnding Rating Group[%d], ChargingMethod %v",
				urr.URRID, rg, chgInfo.ChargingMethod)
			triggerTime := time.Now()

			uu := models.Chf_ConvCharging_UsedUnitContainer{
				QuotaManagementIndicator: chgInfo.ChargingMethod,
				Triggers:                 []models.Chf_ConvCharging_Trigger{trigger},
				TriggerTimestamp:         &triggerTime,
			}

			muu := models.Chf_ConvCharging_MultipleUnitUsage{
				RatingGroup:       rg,
				UPFID:             chgInfo.UpfId,
				UsedUnitContainer: []models.Chf_ConvCharging_UsedUnitContainer{uu},
			}

			multipleUnitUsage = append(multipleUnitUsage, muu)
		}
	}

	_, problemDetails, err := p.Consumer().SendConvergedChargingRequest(smContext,
		smf_context.CHARGING_UPDATE, multipleUnitUsage)
	if problemDetails != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Init] Failed Problem[%+v]", problemDetails)
	} else if err != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Init] Error[%+v]", err)
	} else {
		logger.ChargingLog.Infof("Send Charging Data Request[Init] successfully")
	}
}

func (p *Processor) ReleaseChargingSession(smContext *smf_context.SMContext) error {
	if smContext.ChargingDataRef == "" {
		return nil
	}
	if !smContext.BeginChargingRelease() {
		state, lastErr := smContext.ChargingReleaseResult()
		switch state {
		case smf_context.ChargingReleaseSucceeded:
			return nil
		case smf_context.ChargingReleaseFailed:
			return lastErr
		case smf_context.ChargingReleaseInProgress:
			return fmt.Errorf("charging release is in progress")
		default:
			return fmt.Errorf("unexpected charging release state: %d", state)
		}
	}

	// Final usage stays in the context until CHF reports success. The consumer
	// freezes the complete request before attempting any network operation.
	reports := smContext.UrrReports
	multipleUnitUsage := buildMultiUnitUsage(smContext, reports)

	_, problemDetails, err := p.Consumer().SendConvergedChargingRequest(smContext,
		smf_context.CHARGING_RELEASE, multipleUnitUsage)
	if problemDetails != nil {
		err = fmt.Errorf("charging release failed: status=%d cause=%s detail=%s",
			problemDetails.Status, problemDetails.Cause, problemDetails.Detail)
	} else if err != nil {
		err = fmt.Errorf("charging release failed: %w", err)
	}
	if err != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Termination] failed: %v", err)
	} else {
		smContext.UrrReports = smContext.UrrReports[len(reports):]
		logger.ChargingLog.Infof("Send Charging Data Request[Termination] successfully")
	}
	smContext.FinishChargingRelease(err)
	return err
}

// Report usage report to the CHF and update the URR with the charging information in the charging response
func (p *Processor) ReportUsageAndUpdateQuota(smContext *smf_context.SMContext) {
	multipleUnitUsage := buildMultiUnitUsageFromUsageReport(smContext)

	if len(multipleUnitUsage) == 0 {
		logger.ChargingLog.Infof("No report need to be charged")
		return
	}

	rsp, problemDetails, errSendConvergedChargingRequest := p.Consumer().SendConvergedChargingRequest(smContext,
		smf_context.CHARGING_UPDATE, multipleUnitUsage)

	if problemDetails != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Update] Failed Problem[%+v]", problemDetails)
	} else if errSendConvergedChargingRequest != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Update] Error[%+v]", errSendConvergedChargingRequest)
	} else {
		var pfcpResponseStatus smf_context.PFCPSessionResponseStatus

		upfUrrMap := make(map[string][]*smf_context.URR)

		logger.ChargingLog.Infof("Send Charging Data Request[Update] successfully")
		smContext.SetState(smf_context.PFCPModification)

		p.updateGrantedQuota(smContext, rsp.MultipleUnitInformation)
		// Usually only the anchor UPF need	to be updated
		for upfId, entries := range smContext.UrrTable {
			for _, entry := range entries {
				if entry.State == smf_context.RULE_UPDATE {
					upfUrrMap[upfId] = append(upfUrrMap[upfId], entry.Rule)
				}
			}
		}

		if len(upfUrrMap) == 0 {
			logger.ChargingLog.Infof("Do not have urr that need to update charging information")
			return
		}

		for upfId, urrList := range upfUrrMap {
			logger.ChargingLog.Debugf("Sending PFCP Session Modification to UpfId=%s with %d URRs", upfId, len(urrList))
			for _, urr := range urrList {
				logger.ChargingLog.Debugf("URR[%d]: VolumeQuota=%d, Trigger.Volqu=%v",
					urr.URRID, urr.VolumeQuota, urr.ReportingTrigger.HasVOLQU())
			}

			upf := smf_context.GetUpfById(upfId)
			if upf == nil {
				logger.PduSessLog.Warnf("Cound not find upf %s", upfId)
				continue
			}
			response, requestErr := p.sendSessionModificationRequest(smContext, &PFCPState{
				upf: upf, urrList: urrList,
			})
			if requestErr != nil {
				logger.PduSessLog.Warnf("Sending PFCP Session Modification Request to AN UPF error: %+v", requestErr)
				pfcpResponseStatus = smf_context.SessionUpdateFailed
			} else {
				cause, causeErr := response.Cause.Cause()
				if causeErr != nil || cause != ie.CauseRequestAccepted {
					logger.PduSessLog.Warnf(
						"Received PFCP Session Modification Not Accepted Response from AN UPF: cause=%d err=%v",
						cause, causeErr,
					)
					pfcpResponseStatus = smf_context.SessionUpdateFailed
				} else {
					logger.PduSessLog.Infoln("Received PFCP Session Modification Accepted Response")
					pfcpResponseStatus = smf_context.SessionUpdateSuccess
				}
			}

			switch pfcpResponseStatus {
			case smf_context.SessionUpdateSuccess:
				logger.PfcpLog.Traceln("In case SessionUpdateSuccess")
				smContext.SetState(smf_context.Active)
			case smf_context.SessionUpdateFailed:
				logger.PfcpLog.Traceln("In case SessionUpdateFailed")
				smContext.SetState(smf_context.Active)
			}
		}
	}
}

func buildMultiUnitUsageFromUsageReport(
	smContext *smf_context.SMContext,
) []models.Chf_ConvCharging_MultipleUnitUsage {
	multipleUnitUsage := buildMultiUnitUsage(smContext, smContext.UrrReports)
	smContext.UrrReports = []smf_context.UsageReport{}
	return multipleUnitUsage
}

func buildMultiUnitUsage(
	smContext *smf_context.SMContext, reports []smf_context.UsageReport,
) []models.Chf_ConvCharging_MultipleUnitUsage {
	logger.ChargingLog.Infof("build MultiUnitUsageFromUsageReport")

	type usageKey struct {
		ratingGroup int32
		upfID       string
	}
	var ratingGroupUnitUsagesMap map[usageKey]models.Chf_ConvCharging_MultipleUnitUsage
	var multipleUnitUsage []models.Chf_ConvCharging_MultipleUnitUsage

	ratingGroupUnitUsagesMap = make(map[usageKey]models.Chf_ConvCharging_MultipleUnitUsage)
	for _, ur := range reports {
		logger.ChargingLog.Debugf("Processing Usage Report: URR ID=%d, ReportType=%s", ur.UrrId, ur.ReportTpye)
		if ur.ReportTpye != "" {
			var triggers []models.Chf_ConvCharging_Trigger

			chgInfo := smContext.ChargingInfo[ur.UrrId]
			if chgInfo == nil {
				var urrRule *smf_context.URR
				if ur.UpfId != "" {
					if entries, ok := smContext.UrrTable[ur.UpfId]; ok {
						if entry := entries[ur.UrrId]; entry != nil {
							urrRule = entry.Rule
						}
					}
				}
				if urrRule == nil {
					for _, entries := range smContext.UrrTable {
						if entry := entries[ur.UrrId]; entry != nil {
							urrRule = entry.Rule
							break
						}
					}
				}

				if urrRule == nil {
					logger.PduSessLog.Errorf("URR[%d] rule not found", ur.UrrId)
				} else if smf_context.IsChargingRelatedUrr(urrRule) {
					logger.PduSessLog.Errorf("Charging-related URR[%d] missing ChargingInfo", ur.UrrId)
				} else {
					logger.PduSessLog.Tracef("Skip non-charging URR[%d] with no ChargingInfo", ur.UrrId)
				}
				continue
			}

			if ur.UpfId != "" && chgInfo.UpfId != "" && ur.UpfId != chgInfo.UpfId {
				logger.ChargingLog.Warnf("Usage report UPF mismatch for URR[%d] RG[%d]: "+
					"report.UpfId=%s chargingInfo.UpfId=%s reportType=%s",
					ur.UrrId, chgInfo.RatingGroup, ur.UpfId, chgInfo.UpfId, ur.ReportTpye)
			}

			if chgInfo.ChargingLevel == smf_context.FlowCharging &&
				ur.ReportTpye == models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT {
				triggers = []models.Chf_ConvCharging_Trigger{
					{
						TriggerType:     ur.ReportTpye,
						TriggerCategory: models.Chf_ConvCharging_TriggerCategory_DEFERRED_REPORT,
					},
				}
			} else {
				triggers = []models.Chf_ConvCharging_Trigger{
					{
						TriggerType:     ur.ReportTpye,
						TriggerCategory: models.Chf_ConvCharging_TriggerCategory_IMMEDIATE_REPORT,
					},
				}
			}

			rg := chgInfo.RatingGroup
			logger.ChargingLog.Debugf("Receive Usage Report from URR[%d], Rating Group[%d], ReportUpfId=%s, "+
				"ChargingInfoUpfId=%s, ChargingMethod=%v",
				ur.UrrId, rg, ur.UpfId, chgInfo.UpfId, chgInfo.ChargingMethod)
			triggerTime := time.Now()

			uu := models.Chf_ConvCharging_UsedUnitContainer{
				QuotaManagementIndicator: chgInfo.ChargingMethod,
				Triggers:                 triggers,
				TriggerTimestamp:         &triggerTime,
				DownlinkVolume:           int32(ur.DownlinkVolume),
				UplinkVolume:             int32(ur.UplinkVolume),
				TotalVolume:              int32(ur.TotalVolume),
			}
			upfID := ur.UpfId
			if upfID == "" {
				upfID = chgInfo.UpfId
			}
			key := usageKey{ratingGroup: rg, upfID: upfID}
			unitUsage, exists := ratingGroupUnitUsagesMap[key]
			if !exists {
				unitUsage.RatingGroup, unitUsage.UPFID = rg, upfID
			}
			unitUsage.UsedUnitContainer = append(unitUsage.UsedUnitContainer, uu)
			// Preserve final usage, but request credit only for a live service.
			// A later live report can share a group with an earlier final report.
			if chgInfo.ChargingMethod == models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING &&
				ur.ReportTpye != models.Chf_ConvCharging_TriggerType_FINAL &&
				len(getUrrsByRg(smContext, upfID, rg)) != 0 {
				unitUsage.RequestedUnit = &models.Chf_ConvCharging_RequestedUnit{
					TotalVolume: smContext.RequestedUnit, DownlinkVolume: smContext.RequestedUnit,
					UplinkVolume: smContext.RequestedUnit,
				}
			}
			ratingGroupUnitUsagesMap[key] = unitUsage
		} else {
			logger.PduSessLog.Tracef("Report for urr (%d) will not be charged", ur.UrrId)
		}
	}

	for _, unitUsage := range ratingGroupUnitUsagesMap {
		multipleUnitUsage = append(multipleUnitUsage, unitUsage)
	}

	return multipleUnitUsage
}

func getUrrsByRg(smContext *smf_context.SMContext, upfId string, rg int32) []*smf_context.URR {
	var foundUrrs []*smf_context.URR

	if entries, ok := smContext.UrrTable[upfId]; ok {
		for urrID, entry := range entries {
			if entry.State != smf_context.RULE_REMOVE && entry.RefCount > 0 &&
				smContext.ChargingInfo[urrID] != nil && smContext.ChargingInfo[urrID].RatingGroup == rg {
				foundUrrs = append(foundUrrs, entry.Rule)
				logger.ChargingLog.Debugf("Found URR[%d] for RatingGroup[%d], UpfId=%s", urrID, rg, upfId)
			}
		}
	}

	if len(foundUrrs) > 1 {
		logger.ChargingLog.Debugf("Multiple URRs (%d) found for RatingGroup[%d], UpfId=%s - Will update ALL of them",
			len(foundUrrs), rg, upfId)
	}

	return foundUrrs
}

// Update the urr by the charging information renewed by chf
func (p *Processor) updateGrantedQuota(
	smContext *smf_context.SMContext, multipleUnitInformation []models.Chf_ConvCharging_MultipleUnitInformation,
) {
	logger.ChargingLog.Debugf("updateGrantedQuota: Received %d MultipleUnitInformation from CHF",
		len(multipleUnitInformation))

	for _, ui := range multipleUnitInformation {
		rg := ui.RatingGroup
		upfId := ui.UPFID
		logger.ChargingLog.Debugf("Processing CHF response: RatingGroup=%d, UpfId=%s", rg, upfId)

		urrs := getUrrsByRg(smContext, upfId, rg)
		if len(urrs) == 0 {
			retired := false
			for _, info := range smContext.ChargingInfo {
				if info.UpfId == upfId && info.RatingGroup == rg {
					retired = true
					break
				}
			}
			if retired {
				logger.ChargingLog.Debugf("Ignore quota for retired RatingGroup[%d], UpfId=%s", rg, upfId)
				continue
			}
			logger.ChargingLog.Errorf("Cannot find URR for RatingGroup[%d], UpfId=%s - Quota will NOT be updated", rg, upfId)
			continue
		}

		// Update ALL URRs with the same Rating Group
		for _, urr := range urrs {
			logger.ChargingLog.Debugf("Will update URR[%d] with quota from RatingGroup[%d]", urr.URRID, rg)
			trigger := pfcptype.ReportingTrigger{}
			smContext.SetUrrState(upfId, urr.URRID, smf_context.RULE_UPDATE)
			chgInfo := smContext.ChargingInfo[urr.URRID]

			for _, t := range ui.Triggers {
				switch t.TriggerType {
				case models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT:
					// According to 32.255, the for the trigger "Expirt of datavolume limit" have two reporting level
					// In the Pdu sesion level, the report should be "Immediate report",
					// that is this report should send to CHF immediately
					// In the Rating Group level, the report should be "Defferd report", that is this report should send to CHF
					// when the in the next charging request triggereed
					// by charging trigger that belongs to the type of immediate report

					// TODO: Currently CHF cannot identify the report level since it only knows the rating group,
					// so the both charging level of "Expirt of datavolume limit"
					// will be included in the report, and the report type will be determined by the SMF
					switch chgInfo.ChargingLevel {
					case smf_context.PduSessionCharging:
						if t.TriggerCategory == models.Chf_ConvCharging_TriggerCategory_IMMEDIATE_REPORT {
							smContext.Log.Infof("Add Volume Limit Expiry Timer for Pdu session, it's rating group is [%d]", rg)

							if chgInfo.VolumeLimitExpiryTimer != nil {
								chgInfo.VolumeLimitExpiryTimer.Stop()
								chgInfo.VolumeLimitExpiryTimer = nil
							}

							chgInfo.VolumeLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.VolumeLimit)*time.Second, 1,
								func(expireTimes int32) {
									smContext.SMLock.Lock()
									defer smContext.SMLock.Unlock()
									entry := smContext.UrrTable[ui.UPFID][urr.URRID]
									if entry == nil || entry.RefCount == 0 || entry.State == smf_context.RULE_REMOVE {
										return
									}
									urrList := []*smf_context.URR{urr}
									upf := smf_context.GetUpfById(ui.UPFID)
									if upf != nil {
										p.QueryReport(smContext, upf, urrList, models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT)
										p.ReportUsageAndUpdateQuota(smContext)
									}
								},
								func() {
									smContext.Log.Tracef("Volume Limit Expiry for Pdu session, it's rating group is [%d]", rg)
									chgInfo.VolumeLimitExpiryTimer.Stop()
									chgInfo.VolumeLimitExpiryTimer = nil
								})
						}
					case smf_context.FlowCharging:
						if t.TriggerCategory == models.Chf_ConvCharging_TriggerCategory_DEFERRED_REPORT {
							smContext.Log.Infof("Add Volume Limit Expiry Timer for rating group [%d] ", rg)

							if chgInfo.VolumeLimitExpiryTimer != nil {
								chgInfo.VolumeLimitExpiryTimer.Stop()
								chgInfo.VolumeLimitExpiryTimer = nil
							}

							chgInfo.VolumeLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.VolumeLimit)*time.Second, 1,
								func(expireTimes int32) {
									smContext.SMLock.Lock()
									defer smContext.SMLock.Unlock()
									entry := smContext.UrrTable[ui.UPFID][urr.URRID]
									if entry == nil || entry.RefCount == 0 || entry.State == smf_context.RULE_REMOVE {
										return
									}
									urrList := []*smf_context.URR{urr}
									upf := smf_context.GetUpfById(ui.UPFID)
									if upf != nil {
										p.QueryReport(smContext, upf, urrList, models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT)
									}
								},
								func() {
									smContext.Log.Tracef("Volume Limit Expiry for rating group [%d]", rg)
									chgInfo.VolumeLimitExpiryTimer.Stop()
									chgInfo.VolumeLimitExpiryTimer = nil
								})
						}
					}
				case models.Chf_ConvCharging_TriggerType_MAX_NUMBER_OF_CHANGES_IN_CHARGING_CONDITIONS:
					switch chgInfo.ChargingLevel {
					case smf_context.PduSessionCharging:
						chgInfo.EventLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.EventLimit)*time.Second, 1,
							func(expireTimes int32) {
								smContext.SMLock.Lock()
								defer smContext.SMLock.Unlock()
								entry := smContext.UrrTable[ui.UPFID][urr.URRID]
								if entry == nil || entry.RefCount == 0 || entry.State == smf_context.RULE_REMOVE {
									return
								}
								urrList := []*smf_context.URR{urr}
								upf := smf_context.GetUpfById(ui.UPFID)
								if upf != nil {
									p.QueryReport(smContext, upf, urrList, models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT)
									p.ReportUsageAndUpdateQuota(smContext)
								}
							},
							func() {
								smContext.Log.Tracef("Event Limit Expiry Timer is triggered")
								chgInfo.EventLimitExpiryTimer = nil
							})
					default:
						smContext.Log.Tracef("MAX_NUMBER_OF_CHANGES_IN_CHARGING_CONDITIONS" +
							"should only be applied to PDU session level charging")
					}
				case models.Chf_ConvCharging_TriggerType_QUOTA_THRESHOLD:
					if ui.VolumeQuotaThreshold != 0 {
						trigger.SetVOLTH()
						urr.VolumeThreshold = uint64(ui.VolumeQuotaThreshold)
					}
				// The difference between the quota validity time and the volume limit is
				// that the validity time is counted by the UPF, the volume limit is counted by the SMF
				case models.Chf_ConvCharging_TriggerType_VALIDITY_TIME:
					if ui.ValidityTime != 0 {
						urr.ReportingTrigger.SetQUVTI()
						urr.QuotaValidityTime = time.Now().Add(time.Second * time.Duration(ui.ValidityTime))
					}
				case models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED:
					if chgInfo.ChargingMethod == models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING {
						if ui.GrantedUnit != nil {
							trigger.SetVOLQU()
							urr.VolumeQuota = uint64(ui.GrantedUnit.TotalVolume)
							logger.ChargingLog.Debugf("QUOTA_EXHAUSTED: Setting VolumeQuota=%d", urr.VolumeQuota)
						} else {
							// No granted quota, so set the urr.VolumeQuota to 0, upf should stop send traffic
							logger.ChargingLog.Warnf("No granted quota, setting VolumeQuota=0")
							trigger.SetVOLQU()
							urr.VolumeQuota = 0
						}
					}
				}
			}

			urr.ReportingTrigger = trigger
		} // end for each urr
	}
}
