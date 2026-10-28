package processor

import (
	"context"
	"time"

	smf_context "github.com/free5gc/smf/internal/context"
)

// RemoveSMContextFromAllNF requires SMLock. This is the lifecycle boundary
// authorizing removal; a charging Release alone does not authorize it.
func (p *Processor) RemoveSMContextFromAllNF(smContext *smf_context.SMContext, sendNotification bool) {
	if smf_context.GetSMContextByRef(smContext.Ref) != smContext {
		return
	}
	work := p.queueSMContextRelease(smContext, sendNotification)
	if work.removalScheduled || time.Now().Before(work.retryAt) {
		return
	}
	if err := p.startChargingRelease(context.Background(), smContext); err != nil {
		smContext.SetState(smf_context.InActivePending)
		smContext.Log.Warnf("Retain SMContext and policy for unsettled charging: %v", err)
		return
	}
	p.completeSMContextRelease(context.Background(), work)
}

// queueSMContextRelease requires SMLock and performs no external I/O. The
// existing recovery worker settles charging before policy/context cleanup.
func (p *Processor) queueSMContextRelease(
	smContext *smf_context.SMContext, sendNotification bool,
) *chargingReleaseContinuation {
	work := p.rememberChargingRelease(smContext)
	work.cleanupRequested = true
	work.sendNotification = work.sendNotification || sendNotification
	return work
}

func (p *Processor) completeSMContextRelease(ctx context.Context, work *chargingReleaseContinuation) {
	smContext := work.sm
	if ctx.Err() != nil || work.removalScheduled {
		return
	}
	smContext.SetState(smf_context.InActive)

	// Notify BSF about PCF binding release if we have a binding ID
	if smContext.BSFBindingID != "" {
		p.Consumer().NotifyBSFBindingRelease(smContext)
	}

	// Delete policy-owned charging data only after CHF reports a successful release.
	if smContext.SMPolicyID != "" {
		if err := p.Consumer().SendSMPolicyAssociationTerminationContext(ctx, smContext); err != nil {
			smContext.Log.Errorf("SM Policy Termination failed: %s", err)
			p.deferSMContextRelease(work)
			return
		}
		smContext.SMPolicyID = ""
	}

	// Run SDM cleanup once for contexts counted at creation.
	// UnSubscribe decrements the count or deletes the last subscription.
	// Clear the marker only on success, preserving state if UDM deletion fails.
	if smContext.SdmCounted && smf_context.GetSelf().Ues.UeExists(smContext.Supi) {
		problemDetails, err := p.Consumer().UnSubscribeContext(ctx, smContext)
		if problemDetails != nil {
			smContext.Log.Errorf("SDM UnSubscription Failed Problem[%+v]", problemDetails)
			p.deferSMContextRelease(work)
			return
		}
		if err != nil {
			smContext.Log.Errorf("SDM UnSubscription Error[%+v]", err)
			p.deferSMContextRelease(work)
			return
		}
		smContext.SdmCounted = false
	}

	// Because the amfUE who called this SMF API is being locked until the API Handler returns,
	// sending SMContext Status Notification should run asynchronously
	// so that this function returns immediately.
	work.removalScheduled = true
	go p.sendSMContextStatusNotificationAndRemoveSMContext(work)
}

func (p *Processor) sendSMContextStatusNotificationAndRemoveSMContext(
	work *chargingReleaseContinuation,
) {
	smContext := work.sm
	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	defer p.chargingReleases.CompareAndDelete(smContext.Ref, work)
	if smf_context.GetSMContextByRef(smContext.Ref) != smContext {
		return
	}
	if work.sendNotification && len(smContext.SmStatusNotifyUri) != 0 {
		p.SendReleaseNotification(smContext)
	}

	smf_context.RemoveSMContext(smContext.Ref)
}

func (p *Processor) SendReleaseNotification(smContext *smf_context.SMContext) {
	// Use go routine to send Notification to prevent blocking the handling process
	problemDetails, err := p.Consumer().SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
	if problemDetails != nil || err != nil {
		if problemDetails != nil {
			smContext.Log.Warnf("Send SMContext Status Notification Problem[%+v]", problemDetails)
		}

		if err != nil {
			smContext.Log.Warnf("Send SMContext Status Notification Error[%v]", err)
		}
	} else {
		smContext.Log.Traceln("Send SMContext Status Notification successfully")
	}
}

// SMLock guards this continuation. Settlement may finish before the UE/AMF
// handshake allows NF cleanup, so cleanup is an explicit, separate intent.
type chargingReleaseContinuation struct {
	sm               *smf_context.SMContext
	cleanupRequested bool
	sendNotification bool
	removalScheduled bool
	retryAt          time.Time
	attempts         int
}

func (p *Processor) rememberChargingRelease(sm *smf_context.SMContext) *chargingReleaseContinuation {
	sm.RequestChargingRelease()
	value, _ := p.chargingReleases.LoadOrStore(sm.Ref, &chargingReleaseContinuation{sm: sm})
	return value.(*chargingReleaseContinuation)
}

func (p *Processor) deferSMContextRelease(work *chargingReleaseContinuation) {
	work.retryAt = time.Now().Add(chargingRetryDelay(work.attempts))
	work.attempts++
}

func (p *Processor) recoverChargingReleases(ctx context.Context, now time.Time) {
	p.chargingReleases.Range(func(_, value any) bool {
		if ctx.Err() != nil {
			return false
		}
		work := value.(*chargingReleaseContinuation)
		sm := work.sm
		if !sm.SMLock.TryLock() {
			return true
		}
		defer sm.SMLock.Unlock()
		current, exists := p.chargingReleases.Load(sm.Ref)
		if !exists || current != work {
			return true
		}
		if smf_context.GetSMContextByRef(sm.Ref) != sm {
			p.chargingReleases.CompareAndDelete(sm.Ref, work)
			return true
		}
		if work.removalScheduled || now.Before(work.retryAt) {
			return true
		}
		// The existing invocation owns its retries, including permanent or
		// unconfirmed outcomes. Never replace it with a new final request.
		if _, pending := p.chargingWork.Load(sm.Ref); pending {
			return true
		}
		if err := p.startChargingRelease(ctx, sm); err != nil {
			sm.Log.Warnf("Final charging settlement remains pending: %v", err)
			return true
		}
		if work.cleanupRequested {
			p.completeSMContextRelease(ctx, work)
		} else {
			// UE/AMF completion will explicitly request removal later.
			p.chargingReleases.CompareAndDelete(sm.Ref, work)
		}
		return true
	})
}
