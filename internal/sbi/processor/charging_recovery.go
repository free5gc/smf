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

// RunChargingRecovery examines only retained work, never the full session pool.
// Ordinary callers and recovery serialize through the original session's SMLock.
func (p *Processor) RunChargingRecovery(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		p.recoverPendingCharging(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Processor) recoverPendingCharging(ctx context.Context, now time.Time) {
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
		if !work.retryable || now.Before(work.retryAt) {
			return true
		}
		if smf_context.GetSMContextByRef(sm.Ref) != sm {
			work.retryable = false
			logger.ChargingLog.Errorf("Charging recovery retained for removed session %s", sm.Ref)
			return true
		}
		if !sm.BeginChargingRetry(work.kind) {
			return true
		}
		work.attempts++
		if _, err := p.runChargingWork(ctx, work, nil); err != nil {
			logger.ChargingLog.Warnf("Charging recovery for %s deferred: %v", sm.Ref, err)
		}
		return true
	})
	p.recoverChargingReleases(ctx, now)
}

func chargingRetryDelay(attempt int) time.Duration {
	delay := 2 * time.Second
	for i := 0; i < attempt && delay < 5*time.Minute; i++ {
		delay *= 2
	}
	return min(delay, 5*time.Minute)
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
	if !work.quotaDelivery {
		sm.FinishChargingUpdate(nil)
		return response, nil
	}
	sm.ChargingQuotaPending(nil)
	work.retryable = true
	if err = p.deliverChargingQuota(ctx, work, response); err != nil {
		return response, err
	}
	sm.FinishChargingUpdate(nil)
	return response, nil
}
