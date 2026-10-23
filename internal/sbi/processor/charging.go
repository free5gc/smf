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
