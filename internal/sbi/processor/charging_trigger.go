package processor

import (
	"time"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
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
	smContext *smf_context.SMContext, urrList []*smf_context.URR, trigger models.ChfConvergedChargingTrigger,
) {
	var multipleUnitUsage []models.ChfConvergedChargingMultipleUnitUsage

	for _, urr := range urrList {
		if chgInfo := smContext.ChargingInfo[urr.URRID]; chgInfo != nil {
			rg := chgInfo.RatingGroup
			logger.PduSessLog.Tracef("Receive Usage Report from URR[%d], correspopnding Rating Group[%d], ChargingMethod %v",
				urr.URRID, rg, chgInfo.ChargingMethod)
			triggerTime := time.Now()

			uu := models.ChfConvergedChargingUsedUnitContainer{
				QuotaManagementIndicator: chgInfo.ChargingMethod,
				Triggers:                 []models.ChfConvergedChargingTrigger{trigger},
				TriggerTimestamp:         &triggerTime,
			}

			muu := models.ChfConvergedChargingMultipleUnitUsage{
				RatingGroup:       rg,
				UPFID:             chgInfo.UpfId,
				UsedUnitContainer: []models.ChfConvergedChargingUsedUnitContainer{uu},
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

func (p *Processor) ReleaseChargingSession(smContext *smf_context.SMContext) {
	multipleUnitUsage := buildMultiUnitUsageFromUsageReport(smContext)

	_, problemDetails, err := p.Consumer().SendConvergedChargingRequest(smContext,
		smf_context.CHARGING_RELEASE, multipleUnitUsage)
	if problemDetails != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Termination] Failed Problem[%+v]", problemDetails)
	} else if err != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Termination] Error[%+v]", err)
	} else {
		logger.ChargingLog.Infof("Send Charging Data Request[Termination] successfully")
	}
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
		for _, urr := range smContext.UrrUpfMap {
			upfId := smContext.ChargingInfo[urr.URRID].UpfId

			if urr.State == smf_context.RULE_UPDATE {
				upfUrrMap[upfId] = append(upfUrrMap[upfId], urr)
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
					urr.URRID, urr.VolumeQuota, urr.ReportingTrigger.Volqu)
			}

			upf := smf_context.GetUpfById(upfId)
			if upf == nil {
				logger.PduSessLog.Warnf("Cound not find upf %s", upfId)
				continue
			}
			rcvMsg, err_ := pfcp_message.SendPfcpSessionModificationRequest(
				upf, smContext, nil, nil, nil, nil, urrList)
			if err_ != nil {
				logger.PduSessLog.Warnf("Sending PFCP Session Modification Request to AN UPF error: %+v", err_)
				pfcpResponseStatus = smf_context.SessionUpdateFailed
			} else {
				logger.PduSessLog.Infoln("Received PFCP Session Modification Response")
				pfcpResponseStatus = smf_context.SessionUpdateSuccess
			}

			rsp := rcvMsg.PfcpMessage.Body.(pfcp.PFCPSessionModificationResponse)
			if rsp.Cause == nil || rsp.Cause.CauseValue != pfcpType.CauseRequestAccepted {
				logger.PduSessLog.Warn("Received PFCP Session Modification Not Accepted Response from AN UPF")
				pfcpResponseStatus = smf_context.SessionUpdateFailed
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
) []models.ChfConvergedChargingMultipleUnitUsage {
	logger.ChargingLog.Infof("build MultiUnitUsageFromUsageReport")

	var ratingGroupUnitUsagesMap map[int32]models.ChfConvergedChargingMultipleUnitUsage
	var multipleUnitUsage []models.ChfConvergedChargingMultipleUnitUsage

	ratingGroupUnitUsagesMap = make(map[int32]models.ChfConvergedChargingMultipleUnitUsage)
	for _, ur := range smContext.UrrReports {
		logger.ChargingLog.Debugf("Processing Usage Report: URR ID=%d, ReportType=%s", ur.UrrId, ur.ReportTpye)
		if ur.ReportTpye != "" {
			var triggers []models.ChfConvergedChargingTrigger

			chgInfo := smContext.ChargingInfo[ur.UrrId]
			if chgInfo == nil {
				logger.PduSessLog.Warnf("URR %d is not in ChargingInfo map!", ur.UrrId)
				continue
			}

			if chgInfo.ChargingLevel == smf_context.FlowCharging &&
				ur.ReportTpye == models.ChfConvergedChargingTriggerType_VOLUME_LIMIT {
				triggers = []models.ChfConvergedChargingTrigger{
					{
						TriggerType:     ur.ReportTpye,
						TriggerCategory: models.TriggerCategory_DEFERRED_REPORT,
					},
				}
			} else {
				triggers = []models.ChfConvergedChargingTrigger{
					{
						TriggerType:     ur.ReportTpye,
						TriggerCategory: models.TriggerCategory_IMMEDIATE_REPORT,
					},
				}
			}

			rg := chgInfo.RatingGroup
			logger.ChargingLog.Debugf("Receive Usage Report from URR[%d], Rating Group[%d], UpfId=%s, ChargingMethod=%v",
				ur.UrrId, rg, chgInfo.UpfId, chgInfo.ChargingMethod)
			triggerTime := time.Now()

			uu := models.ChfConvergedChargingUsedUnitContainer{
				QuotaManagementIndicator: chgInfo.ChargingMethod,
				Triggers:                 triggers,
				TriggerTimestamp:         &triggerTime,
				DownlinkVolume:           int32(ur.DownlinkVolume),
				UplinkVolume:             int32(ur.UplinkVolume),
				TotalVolume:              int32(ur.TotalVolume),
			}
			if unitUsage, ok := ratingGroupUnitUsagesMap[rg]; !ok {
				requestUnit := &models.RequestedUnit{}

				// Only online charging should request unit
				// offline charging is only for recording usage
				if chgInfo.ChargingMethod == models.QuotaManagementIndicator_ONLINE_CHARGING {
					requestUnit = &models.RequestedUnit{
						TotalVolume:    smContext.RequestedUnit,
						DownlinkVolume: smContext.RequestedUnit,
						UplinkVolume:   smContext.RequestedUnit,
					}
				}

				ratingGroupUnitUsagesMap[rg] = models.ChfConvergedChargingMultipleUnitUsage{
					RatingGroup:       rg,
					UPFID:             ur.UpfId,
					UsedUnitContainer: []models.ChfConvergedChargingUsedUnitContainer{uu},
					RequestedUnit:     requestUnit,
				}
			} else {
				unitUsage.UsedUnitContainer = append(unitUsage.UsedUnitContainer, uu)
				ratingGroupUnitUsagesMap[rg] = unitUsage
			}
		} else {
			logger.PduSessLog.Tracef("Report for urr (%d) will not be charged", ur.UrrId)
		}
	}

	smContext.UrrReports = []smf_context.UsageReport{}

	for _, unitUsage := range ratingGroupUnitUsagesMap {
		multipleUnitUsage = append(multipleUnitUsage, unitUsage)
	}

	return multipleUnitUsage
}

func getUrrsByRg(smContext *smf_context.SMContext, upfId string, rg int32) []*smf_context.URR {
	var foundUrrs []*smf_context.URR

	for _, urr := range smContext.UrrUpfMap {
		if smContext.ChargingInfo[urr.URRID] != nil &&
			smContext.ChargingInfo[urr.URRID].RatingGroup == rg &&
			smContext.ChargingInfo[urr.URRID].UpfId == upfId {
			foundUrrs = append(foundUrrs, urr)
			logger.ChargingLog.Debugf("Found URR[%d] for RatingGroup[%d], UpfId=%s", urr.URRID, rg, upfId)
		}
	}

	if len(foundUrrs) > 1 {
		logger.ChargingLog.Debugf("Multiple URRs (%d) found for RatingGroup[%d], UpfId=%s - Will update ALL of them",
			len(foundUrrs), rg, upfId)
	} else if len(foundUrrs) == 0 {
		logger.ChargingLog.Errorf("No URR found for RatingGroup[%d], UpfId=%s", rg, upfId)
	}

	return foundUrrs
}

// Update the urr by the charging information renewed by chf
func (p *Processor) updateGrantedQuota(
	smContext *smf_context.SMContext, multipleUnitInformation []models.MultipleUnitInformation,
) {
	logger.ChargingLog.Debugf("updateGrantedQuota: Received %d MultipleUnitInformation from CHF",
		len(multipleUnitInformation))

	for _, ui := range multipleUnitInformation {
		rg := ui.RatingGroup
		upfId := ui.UPFID
		logger.ChargingLog.Debugf("Processing CHF response: RatingGroup=%d, UpfId=%s", rg, upfId)

		urrs := getUrrsByRg(smContext, upfId, rg)
		if len(urrs) == 0 {
			logger.ChargingLog.Errorf("Cannot find URR for RatingGroup[%d], UpfId=%s - Quota will NOT be updated", rg, upfId)
			continue
		}

		// Update ALL URRs with the same Rating Group
		for _, urr := range urrs {
			logger.ChargingLog.Debugf("Will update URR[%d] with quota from RatingGroup[%d]", urr.URRID, rg)
			trigger := pfcpType.ReportingTriggers{}
			urr.State = smf_context.RULE_UPDATE
			chgInfo := smContext.ChargingInfo[urr.URRID]

			for _, t := range ui.Triggers {
				switch t.TriggerType {
				case models.ChfConvergedChargingTriggerType_VOLUME_LIMIT:
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
						if t.TriggerCategory == models.TriggerCategory_IMMEDIATE_REPORT {
							smContext.Log.Infof("Add Volume Limit Expiry Timer for Pdu session, it's rating group is [%d]", rg)

							if chgInfo.VolumeLimitExpiryTimer != nil {
								chgInfo.VolumeLimitExpiryTimer.Stop()
								chgInfo.VolumeLimitExpiryTimer = nil
							}

							chgInfo.VolumeLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.VolumeLimit)*time.Second, 1,
								func(expireTimes int32) {
									smContext.SMLock.Lock()
									defer smContext.SMLock.Unlock()
									urrList := []*smf_context.URR{urr}
									upf := smf_context.GetUpfById(ui.UPFID)
									if upf != nil {
										QueryReport(smContext, upf, urrList, models.ChfConvergedChargingTriggerType_VOLUME_LIMIT)
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
						if t.TriggerCategory == models.TriggerCategory_DEFERRED_REPORT {
							smContext.Log.Infof("Add Volume Limit Expiry Timer for rating group [%d] ", rg)

							if chgInfo.VolumeLimitExpiryTimer != nil {
								chgInfo.VolumeLimitExpiryTimer.Stop()
								chgInfo.VolumeLimitExpiryTimer = nil
							}

							chgInfo.VolumeLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.VolumeLimit)*time.Second, 1,
								func(expireTimes int32) {
									smContext.SMLock.Lock()
									defer smContext.SMLock.Unlock()
									urrList := []*smf_context.URR{urr}
									upf := smf_context.GetUpfById(ui.UPFID)
									if upf != nil {
										QueryReport(smContext, upf, urrList, models.ChfConvergedChargingTriggerType_VOLUME_LIMIT)
									}
								},
								func() {
									smContext.Log.Tracef("Volume Limit Expiry for rating group [%d]", rg)
									chgInfo.VolumeLimitExpiryTimer.Stop()
									chgInfo.VolumeLimitExpiryTimer = nil
								})
						}
					}
				case models.ChfConvergedChargingTriggerType_MAX_NUMBER_OF_CHANGES_IN_CHARGING_CONDITIONS:
					switch chgInfo.ChargingLevel {
					case smf_context.PduSessionCharging:
						chgInfo.EventLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.EventLimit)*time.Second, 1,
							func(expireTimes int32) {
								smContext.SMLock.Lock()
								defer smContext.SMLock.Unlock()
								urrList := []*smf_context.URR{urr}
								upf := smf_context.GetUpfById(ui.UPFID)
								if upf != nil {
									QueryReport(smContext, upf, urrList, models.ChfConvergedChargingTriggerType_VOLUME_LIMIT)
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
				case models.ChfConvergedChargingTriggerType_QUOTA_THRESHOLD:
					if ui.VolumeQuotaThreshold != 0 {
						trigger.Volth = true
						urr.VolumeThreshold = uint64(ui.VolumeQuotaThreshold)
					}
				// The difference between the quota validity time and the volume limit is
				// that the validity time is counted by the UPF, the volume limit is counted by the SMF
				case models.ChfConvergedChargingTriggerType_VALIDITY_TIME:
					if ui.ValidityTime != 0 {
						urr.ReportingTrigger.Quvti = true
						urr.QuotaValidityTime = time.Now().Add(time.Second * time.Duration(ui.ValidityTime))
					}
				case models.ChfConvergedChargingTriggerType_QUOTA_EXHAUSTED:
					if chgInfo.ChargingMethod == models.QuotaManagementIndicator_ONLINE_CHARGING {
						if ui.GrantedUnit != nil {
							trigger.Volqu = true
							urr.VolumeQuota = uint64(ui.GrantedUnit.TotalVolume)
							logger.ChargingLog.Debugf("QUOTA_EXHAUSTED: Setting VolumeQuota=%d", urr.VolumeQuota)
						} else {
							// No granted quota, so set the urr.VolumeQuota to 0, upf should stop send traffic
							logger.ChargingLog.Warnf("No granted quota, setting VolumeQuota=0")
							trigger.Volqu = true
							urr.VolumeQuota = 0
						}
					}
				}
			}

			urr.ReportingTrigger = trigger
		} // end for each urr
	}
}
