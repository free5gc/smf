package producer

import (
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
)

func RemoveSMContextFromAllNF(smContext *smf_context.SMContext, sendNotification bool) {
	smContext.SetState(smf_context.InActive)
	// remove SM Policy Association
	if smContext.SMPolicyID != "" {
		if err := consumer.SendSMPolicyAssociationTermination(smContext); err != nil {
			smContext.Log.Errorf("SM Policy Termination failed: %s", err)
		} else {
			smContext.SMPolicyID = ""
		}
	}

	// Because the amfUE who called this SMF API is being locked until the API Handler returns,
	// sending SMContext Status Notification should run asynchronously
	// so that this function returns immediately.
	go sendSMContextStatusNotificationAndRemoveSMContext(smContext, sendNotification)
}

func sendSMContextStatusNotificationAndRemoveSMContext(smContext *smf_context.SMContext, sendNotification bool) {
	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	if sendNotification && len(smContext.SmStatusNotifyUri) != 0 {
		SendReleaseNotification(smContext)
	}

	smf_context.RemoveSMContext(smContext.Ref)
}

func SendReleaseNotification(smContext *smf_context.SMContext) {
	// Use go routine to send Notification to prevent blocking the handling process
	problemDetails, err := consumer.SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
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
