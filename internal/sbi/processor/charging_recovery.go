package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"time"

	"github.com/wmnsk/go-pfcp/ie"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
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
	consumed      bool
	quotaDelivery bool
	response      []byte
	owners        map[chargingOwner][]*smf_context.URR
	bindings      map[string]*chargingQuotaTarget
	prepared      bool
	retryable     bool
	retryAt       time.Time
	attempts      int
}

type chargingQuotaTarget struct {
	upfID                 string
	peerIP                string
	upf                   *smf_context.UPF
	generation            uint64
	localSEID, remoteSEID uint64
	rules                 []*smf_context.URR
	values                []smf_context.URR
	sent, done            bool
	uncertain             error
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
		} else if work.response != nil {
			sm.ChargingQuotaPending(err)
		} else {
			sm.FinishChargingUpdate(err)
		}
		work.retryAt = time.Now().Add(chargingRetryDelay(work.attempts))
	}()
	if work.response == nil {
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
		work.response, err = json.Marshal(rsp)
		if err != nil {
			return nil, err
		}
	}
	// Removal is tied to the captured batch, never the queue length at retry time.
	if !work.consumed {
		count := len(work.reports)
		if len(sm.UrrReports) < count || (count > 0 && !slices.Equal(sm.UrrReports[:count], work.reports)) {
			work.retryable = false
			return nil, fmt.Errorf("pending charging report batch changed before acknowledgement")
		}
		sm.UrrReports = sm.UrrReports[count:]
		work.consumed = true
	}
	if work.kind == smf_context.CHARGING_RELEASE {
		sm.FinishChargingRelease(nil)
		return nil, nil
	}
	if err = json.Unmarshal(work.response, &response); err != nil {
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

func chargingRuleLive(sm *smf_context.SMContext, upfID string, rule *smf_context.URR) bool {
	entry := sm.UrrTable[upfID][rule.URRID]
	return entry != nil && entry.Rule == rule && entry.RefCount > 0 && entry.State != smf_context.RULE_REMOVE
}

func (p *Processor) deliverChargingQuota(
	ctx context.Context, work *chargingWork, response *models.Chf_ConvCharging_ChargingDataResponse,
) error {
	sm := work.sm
	ref, _, err := sm.PendingChargingRequest(smf_context.CHARGING_UPDATE)
	if err != nil {
		return err
	}
	if ref != sm.ChargingDataRef {
		work.retryable = false
		return fmt.Errorf("charging resource changed before quota delivery")
	}
	if !work.prepared {
		// Validate every live original binding before changing any rule or timer.
		for _, info := range response.MultipleUnitInformation {
			owner := chargingOwner{rg: info.RatingGroup, upf: info.UPFID}
			for _, rule := range work.owners[owner] {
				if chargingRuleLive(sm, owner.upf, rule) {
					if err = validateChargingBinding(sm, work.bindings[owner.upf]); err != nil {
						return err
					}
					break
				}
			}
		}
		for _, info := range response.MultipleUnitInformation {
			owner := chargingOwner{rg: info.RatingGroup, upf: info.UPFID}
			var live []*smf_context.URR
			for _, rule := range work.owners[owner] {
				if chargingRuleLive(sm, info.UPFID, rule) {
					live = append(live, rule)
				}
			}
			if len(live) == 0 {
				continue
			}
			p.updateGrantedQuotaForRules(sm, info, live)
			target := work.bindings[info.UPFID]
			for _, rule := range live {
				found := false
				for _, existing := range target.rules {
					if existing == rule {
						found = true
						break
					}
				}
				if !found {
					target.rules = append(target.rules, rule)
				}
			}
		}
		for _, target := range work.bindings {
			for _, rule := range target.rules {
				target.values = append(target.values, *rule)
			}
		}
		work.prepared = true
	}
	keys := make([]string, 0, len(work.bindings))
	for key := range work.bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var failures []error
	for _, key := range keys {
		target := work.bindings[key]
		if target.done || len(target.rules) == 0 {
			continue
		}
		if err = p.deliverChargingTarget(ctx, sm, target); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (p *Processor) deliverChargingTarget(
	ctx context.Context, sm *smf_context.SMContext, target *chargingQuotaTarget,
) error {
	var live []*smf_context.URR
	for i, rule := range target.rules {
		if !chargingRuleLive(sm, target.upfID, rule) {
			continue
		}
		if *rule != target.values[i] {
			return fmt.Errorf("quota rule changed for UPF %s", target.upfID)
		}
		live = append(live, rule)
	}
	if len(live) == 0 {
		target.done = true
		return nil
	}
	// The transaction layer already retransmits the same packet/sequence. A new
	// transaction after an unknown outcome could reset quota already consumed.
	if target.sent {
		return target.uncertain
	}
	if err := validateChargingBinding(sm, target); err != nil {
		return err
	}
	association, finish, err := target.upf.BeginSessionWork()
	if err != nil {
		return err
	}
	defer finish()
	if err = validateChargingBinding(sm, target); err != nil {
		return err
	}
	ip := net.ParseIP(target.peerIP)

	client, ok := p.getActivePFCPClient().(SessionModificationPFCPClient)
	if !ok {
		return fmt.Errorf("PFCP quota client is not configured")
	}
	// Build only this reply's rules. Unrelated RULE_UPDATE entries are excluded.
	for _, rule := range live {
		sm.SetUrrState(target.upfID, rule.URRID, smf_context.RULE_UPDATE)
	}
	request, err := pfcp_message.BuildSessionModificationRequest(ip.String(), target.upfID, sm, nil, nil, nil, nil, live)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithCancel(association)
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	if err = ctx.Err(); err != nil {
		return err
	}
	previousState := sm.State()
	sm.SetState(smf_context.PFCPModification)
	defer sm.SetState(previousState)
	target.sent = true
	target.uncertain = fmt.Errorf("quota delivery outcome for UPF %s requires confirmation", target.upfID)
	reply, err := client.SendSessionModificationRequest(
		requestCtx, request, &net.UDPAddr{IP: ip, Port: pfcpPeerPort}, target.localSEID)
	if err != nil {
		target.uncertain = fmt.Errorf("%w: %w", target.uncertain, err)
		return target.uncertain
	}
	if reply == nil || reply.Cause == nil || reply.SEID() != target.localSEID || reply.Sequence() != request.Sequence() {
		return target.uncertain
	}
	cause, causeErr := reply.Cause.Cause()
	if causeErr != nil || cause != ie.CauseRequestAccepted {
		target.uncertain = fmt.Errorf("quota UPF %s returned cause=%d err=%v; confirmation required",
			target.upfID, cause, causeErr)
		return target.uncertain
	}
	target.done = true
	target.uncertain = nil
	if len(reply.UsageReport) != 0 {
		if err = sm.HandleReports(reply.UsageReport, target.upf.NodeID, ""); err != nil {
			logger.ChargingLog.Warnf("Decode quota modification usage reports: %v", err)
		}
	}
	return nil
}

func validateChargingBinding(sm *smf_context.SMContext, target *chargingQuotaTarget) error {
	if target == nil || target.upf == nil {
		return fmt.Errorf("original quota UPF is unavailable")
	}
	if smf_context.GetUpfById(target.upfID) != target.upf {
		return fmt.Errorf("original quota UPF %s was replaced", target.upfID)
	}
	state, generation := target.upf.AssociationStateAndGeneration()
	if state != smf_context.AssociationEstablished || generation != target.generation {
		return fmt.Errorf("quota UPF %s association changed", target.upfID)
	}
	if target.upf.NodeID.ResolveNodeIdToIp().String() != target.peerIP {
		return fmt.Errorf("quota UPF %s address changed", target.upfID)
	}
	session := sm.PFCPContext[target.peerIP]
	if session == nil || session.LocalSEID != target.localSEID || session.RemoteSEID != target.remoteSEID {
		return fmt.Errorf("quota PFCP session changed for UPF %s", target.upfID)
	}
	return nil
}
