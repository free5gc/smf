package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

// Initial delivery is followed by at most three retries of the frozen request.
const chargingMaxRetries = 3

type chargingOwner struct {
	rg  int32
	upf string
}

// SMLock guards this in-memory continuation, including its immutable response
// and original report batch. It does not survive a process restart.
type chargingWork struct {
	sm            *smf_context.SMContext
	kind          smf_context.RequestType
	reports       []smf_context.UsageReport
	usageConsumed bool
	quotaDelivery bool
	chfResponse   []byte
	owners        map[chargingOwner][]*smf_context.URR
	bindings      map[string]*chargingQuotaTarget
	prepared      bool
	retryable     bool
	retryAt       time.Time
	attempts      int
}

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
// failures within the retry limit and consumes only the acknowledged report batch.
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
		case smf_context.ChargingReleaseAbandoned:
			p.dropChargingReports(smContext, lastErr)
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
	state, lastErr := smContext.ChargingReleaseResult()
	if state == smf_context.ChargingReleaseAbandoned && lastErr != nil {
		return nil // Local cleanup is permitted; settlement remains explicitly abandoned.
	}
	return err
}

func (p *Processor) ReportUsageAndUpdateQuota(smContext *smf_context.SMContext) {
	if state, lastErr := smContext.ChargingReleaseResult(); state == smf_context.ChargingReleaseAbandoned {
		p.dropChargingReports(smContext, lastErr)
		return
	}
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

func (p *Processor) newChargingWork(
	sm *smf_context.SMContext, kind smf_context.RequestType,
	usage []models.Chf_ConvCharging_MultipleUnitUsage, reportCount int,
) *chargingWork {
	work := &chargingWork{
		quotaDelivery: kind == smf_context.CHARGING_UPDATE && reportCount > 0,
		sm:            sm, kind: kind, reports: append([]smf_context.UsageReport(nil), sm.UrrReports[:reportCount]...),
		owners: make(map[chargingOwner][]*smf_context.URR), bindings: make(map[string]*chargingQuotaTarget),
	}
	for _, entry := range usage {
		key := chargingOwner{rg: entry.RatingGroup, upf: entry.UPFID}
		work.owners[key] = getUrrsByRg(sm, entry.UPFID, entry.RatingGroup)
		if _, exists := work.bindings[entry.UPFID]; exists {
			continue
		}
		target := &chargingQuotaTarget{upfID: entry.UPFID, upf: smf_context.GetUpfById(entry.UPFID)}
		if target.upf != nil {
			_, target.generation = target.upf.AssociationStateAndGeneration()
			target.peerIP = target.upf.NodeID.ResolveNodeIdToIp().String()
			if session := sm.PFCPContext[target.peerIP]; session != nil {
				target.localSEID, target.remoteSEID = session.LocalSEID, session.RemoteSEID
			}
		}
		work.bindings[entry.UPFID] = target
	}
	p.chargingWork.Store(sm.Ref, work)
	return work
}

func (p *Processor) runChargingWork(
	ctx context.Context, work *chargingWork, usage []models.Chf_ConvCharging_MultipleUnitUsage,
) (response *models.Chf_ConvCharging_ChargingDataResponse, err error) {
	sm := work.sm
	defer func() {
		if err == nil {
			p.chargingWork.CompareAndDelete(sm.Ref, work)
			return
		}
		if !work.retryable {
			p.dropChargingWork(work, fmt.Errorf("permanent charging failure: %w", err))
			return
		}
		if work.attempts >= chargingMaxRetries {
			p.dropChargingWork(work, fmt.Errorf("charging retry limit (%d) reached: %w", chargingMaxRetries, err))
			return
		}
		if work.kind == smf_context.CHARGING_RELEASE {
			sm.FinishChargingRelease(err)
		} else if work.chfResponse != nil {
			sm.ChargingQuotaPending(err)
		} else {
			sm.FinishChargingUpdate(err)
		}
		work.retryAt = time.Now().Add(chargingRetryDelay(work.attempts))
	}()
	if work.chfResponse == nil {
		rsp, problem, sendErr := p.Consumer().SendConvergedChargingRequestContext(ctx, sm, work.kind, usage)
		work.retryable = true
		if problem != nil {
			work.retryable = problem.Status == 408 || problem.Status == 429 || (problem.Status >= 500 && problem.Status <= 599)
			return nil, fmt.Errorf("charging request failed: status=%d cause=%s detail=%s",
				problem.Status, problem.Cause, problem.Detail)
		}
		if sendErr != nil {
			work.retryable = !errors.Is(sendErr, smf_context.ErrChargingResponseSequence)
			return nil, sendErr
		}
		if work.kind == smf_context.CHARGING_UPDATE {
			if rsp == nil {
				return nil, fmt.Errorf("charging update returned no response body")
			}
			for _, info := range rsp.MultipleUnitInformation {
				if _, exists := work.owners[chargingOwner{rg: info.RatingGroup, upf: info.UPFID}]; !exists ||
					(info.GrantedUnit != nil && info.GrantedUnit.TotalVolume < 0) {
					work.retryable = false
					return nil, fmt.Errorf("CHF response has an unknown quota owner or invalid grant")
				}
			}
		}
		work.chfResponse, err = json.Marshal(rsp)
		if err != nil {
			return nil, err
		}
	}
	// Removal is tied to the captured batch, never the queue length at retry time.
	if !work.usageConsumed {
		count := len(work.reports)
		if len(sm.UrrReports) < count || (count > 0 && !slices.Equal(sm.UrrReports[:count], work.reports)) {
			work.retryable = false
			return nil, fmt.Errorf("pending charging report batch changed before acknowledgement")
		}
		sm.UrrReports = sm.UrrReports[count:]
		work.usageConsumed = true
	}
	if work.kind == smf_context.CHARGING_RELEASE {
		sm.FinishChargingRelease(nil)
		return nil, nil
	}
	if err = json.Unmarshal(work.chfResponse, &response); err != nil {
		work.retryable = false
		return nil, err
	}
	// Trigger-only ULCL notifications precede PFCP establishment and have no
	// usage batch/quota continuation. Preserve their notification behavior.
	if work.quotaDelivery {
		sm.ChargingQuotaPending(nil)
		work.retryable = true
		if err = p.deliverChargingQuota(ctx, work, response); err != nil {
			return response, err
		}
	}
	sm.FinishChargingUpdate(nil)
	return response, nil
}

// dropChargingWork requires SMLock. Discard the resource rather than reissue an
// ambiguous debit under a fresh invocation; retain the failed outcome in the session.
func (p *Processor) dropChargingWork(work *chargingWork, reason error) {
	sm := work.sm
	ref, request, snapshotErr := sm.PendingChargingRequest(work.kind)
	body, encodeErr := json.Marshal(request)
	reason = errors.Join(reason, snapshotErr, encodeErr)
	logger.ChargingLog.Errorf(
		"Drop charging work: supi=%s session=%s ref=%s type=%d retries=%d acknowledged=%t reason=%v request=%s reports=%+v",
		sm.Supi, sm.Ref, ref, work.kind, work.attempts, work.chfResponse != nil, reason, body, work.reports)
	p.dropChargingReports(sm, reason)
	for _, info := range sm.ChargingInfo {
		if info == nil {
			continue
		}
		if info.VolumeLimitExpiryTimer != nil {
			info.VolumeLimitExpiryTimer.Stop()
			info.VolumeLimitExpiryTimer = nil
		}
		if info.EventLimitExpiryTimer != nil {
			info.EventLimitExpiryTimer.Stop()
			info.EventLimitExpiryTimer = nil
		}
	}
	sm.AbandonCharging(reason)
	p.chargingWork.CompareAndDelete(sm.Ref, work)
}

func (p *Processor) dropChargingReports(sm *smf_context.SMContext, reason error) {
	if len(sm.UrrReports) == 0 {
		return
	}
	logger.ChargingLog.Errorf("Drop charging reports: supi=%s session=%s ref=%s reason=%v reports=%+v",
		sm.Supi, sm.Ref, sm.ChargingDataRef, reason, sm.UrrReports)
	sm.UrrReports = nil
}

// RunChargingRetries examines only work retained in this process, with a bounded retry budget.
// Pending requests are not persisted or restored after restart.
// Ordinary callers and retries serialize through the original session's SMLock.
func (p *Processor) RunChargingRetries(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		p.retryPendingCharging(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Processor) retryPendingCharging(ctx context.Context, now time.Time) {
	p.chargingWork.Range(func(_, value any) bool {
		if ctx.Err() != nil {
			return false
		}
		work := value.(*chargingWork)
		sm := work.sm
		if !sm.SMLock.TryLock() {
			return true
		}
		defer sm.SMLock.Unlock()
		current, exists := p.chargingWork.Load(sm.Ref)
		if !exists || current != work {
			return true
		}
		if smf_context.GetSMContextByRef(sm.Ref) != sm {
			p.dropChargingWork(work, fmt.Errorf("session context was removed"))
			return true
		}
		if !work.retryable || work.attempts >= chargingMaxRetries {
			p.dropChargingWork(work, fmt.Errorf("charging work cannot be retried"))
			return true
		}
		if now.Before(work.retryAt) {
			return true
		}
		if !sm.BeginChargingRetry(work.kind) {
			p.dropChargingWork(work, fmt.Errorf("pending charging invocation is unavailable"))
			return true
		}
		work.attempts++
		if _, err := p.runChargingWork(ctx, work, nil); err != nil {
			if _, pending := p.chargingWork.Load(sm.Ref); pending {
				logger.ChargingLog.Warnf("Charging retry for %s deferred: %v", sm.Ref, err)
			}
		}
		return true
	})
	p.retryChargingReleases(ctx, now)
}

func chargingRetryDelay(attempt int) time.Duration {
	delay := 2 * time.Second
	for i := 0; i < attempt && delay < 5*time.Minute; i++ {
		delay *= 2
	}
	return min(delay, 5*time.Minute)
}
