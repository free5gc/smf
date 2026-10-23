package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
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

	if _, err := p.sendChargingUpdate(smContext, multipleUnitUsage, 0); err != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Update] failed: %v", err)
	} else {
		logger.ChargingLog.Infof("Send Charging Data Request[Update] successfully")
	}
}

// sendChargingUpdate requires SMLock. Its pending work survives CHF and PFCP
// failures and consumes only the original report batch after acknowledgement.
func (p *Processor) sendChargingUpdate(
	smContext *smf_context.SMContext, multipleUnitUsage []models.Chf_ConvCharging_MultipleUnitUsage, reportCount int,
) (*models.Chf_ConvCharging_ChargingDataResponse, error) {
	if smContext.ChargingDataRef == "" {
		return nil, fmt.Errorf("charging session has no resource reference")
	}
	if reportCount < 0 || reportCount > len(smContext.UrrReports) {
		return nil, fmt.Errorf("invalid charging report count")
	}
	if err := smContext.BeginChargingUpdate(); err != nil {
		return nil, err
	}
	work := p.newChargingWork(smContext, smf_context.CHARGING_UPDATE, multipleUnitUsage, reportCount)
	return p.runChargingWork(context.Background(), work, multipleUnitUsage)
}

// ReleaseChargingSession requires SMLock. Remember termination even when an
// unresolved Update prevents sending Release yet. NF cleanup is requested
// separately, when the owning UE/AMF release path permits context removal.
func (p *Processor) ReleaseChargingSession(smContext *smf_context.SMContext) error {
	if smContext.ChargingDataRef == "" {
		return nil
	}
	p.rememberChargingRelease(smContext)
	return p.startChargingRelease(context.Background(), smContext)
}

func (p *Processor) startChargingRelease(ctx context.Context, smContext *smf_context.SMContext) error {
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
			return fmt.Errorf("charging release blocked by an unsettled update")
		}
	}
	reports := smContext.UrrReports
	usage := buildMultiUnitUsage(smContext, reports)
	work := p.newChargingWork(smContext, smf_context.CHARGING_RELEASE, usage, len(reports))
	_, err := p.runChargingWork(ctx, work, usage)
	return err
}

func (p *Processor) ReportUsageAndUpdateQuota(smContext *smf_context.SMContext) {
	reports := smContext.UrrReports
	usage := buildMultiUnitUsage(smContext, reports)
	if len(usage) == 0 {
		return
	}
	if _, err := p.sendChargingUpdate(smContext, usage, len(reports)); err != nil {
		logger.ChargingLog.Errorf("Send Charging Data Request[Update] failed: %v", err)
	}
}

func buildMultiUnitUsage(
	smContext *smf_context.SMContext, reports []smf_context.UsageReport,
) []models.Chf_ConvCharging_MultipleUnitUsage {
	logger.ChargingLog.Infof("build MultiUnitUsage")

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
