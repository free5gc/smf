package processor

import (
	smf_context "github.com/free5gc/smf/internal/context"
)

func (p *Processor) RemoveSMContextFromAllNF(smContext *smf_context.SMContext, sendNotification bool) {
	// A failed or in-flight settlement must also block later cleanup callers.
	// ReleaseChargingSession reuses the recorded result without sending again.
	if err := p.ReleaseChargingSession(smContext); err != nil {
		smContext.SetState(smf_context.InActivePending)
		smContext.Log.Warnf("Retain SMContext and policy for unsettled charging: %v", err)
		return
	}
	smContext.SetState(smf_context.InActive)

	// Notify BSF about PCF binding release if we have a binding ID
	if smContext.BSFBindingID != "" {
		p.Consumer().NotifyBSFBindingRelease(smContext)
	}

	// Delete policy-owned charging data only after CHF reports a successful release.
	if smContext.SMPolicyID != "" {
		if err := p.Consumer().SendSMPolicyAssociationTermination(smContext); err != nil {
			smContext.Log.Errorf("SM Policy Termination failed: %s", err)
		} else {
			smContext.SMPolicyID = ""
		}
	}

	// Run SDM cleanup once for contexts counted at creation.
	// UnSubscribe decrements the count or deletes the last subscription.
	// Clear the marker only on success, preserving state if UDM deletion fails.
	if smContext.SdmCounted && smf_context.GetSelf().Ues.UeExists(smContext.Supi) {
		problemDetails, err := p.Consumer().UnSubscribe(smContext)
		if problemDetails != nil {
			smContext.Log.Errorf("SDM UnSubscription Failed Problem[%+v]", problemDetails)
		} else if err != nil {
			smContext.Log.Errorf("SDM UnSubscription Error[%+v]", err)
		} else {
			smContext.SdmCounted = false
		}
	}

	// Because the amfUE who called this SMF API is being locked until the API Handler returns,
	// sending SMContext Status Notification should run asynchronously
	// so that this function returns immediately.
	go p.sendSMContextStatusNotificationAndRemoveSMContext(smContext, sendNotification)
}

func (p *Processor) sendSMContextStatusNotificationAndRemoveSMContext(
	smContext *smf_context.SMContext, sendNotification bool,
) {
	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	if sendNotification && len(smContext.SmStatusNotifyUri) != 0 {
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
