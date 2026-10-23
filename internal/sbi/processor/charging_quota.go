package processor

import (
	"time"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

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

		p.updateGrantedQuotaForRules(smContext, ui, urrs)
	}
}

func (p *Processor) updateGrantedQuotaForRules(
	smContext *smf_context.SMContext, ui models.Chf_ConvCharging_MultipleUnitInformation, urrs []*smf_context.URR,
) {
	rg := ui.RatingGroup
	upfId := ui.UPFID

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
