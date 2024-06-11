package producer

import (
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
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
	if sendNotification {
		go sendSMContextStatusNotificationAndRemoveSMContext(smContext.SmStatusNotifyUri)
	}
	smf_context.GetSelf().RemoveSMContext(smContext)
}

func sendSMContextStatusNotificationAndRemoveSMContext(uri string) {
	if len(uri) != 0 {
		SendReleaseNotification(uri)
	}
}

func SendReleaseNotification(uri string) {
	// Use go routine to send Notification to prevent blocking the handling process
	problemDetails, err := consumer.SendSMContextStatusNotification(uri)
	if problemDetails != nil || err != nil {
		if problemDetails != nil {
			logger.CtxLog.Warnf("Send SMContext Status Notification Problem[%+v]", problemDetails)
		}

		if err != nil {
			logger.CtxLog.Warnf("Send SMContext Status Notification Error[%v]", err)
		}
	} else {
		logger.CtxLog.Traceln("Send SMContext Status Notification successful")
	}
}
