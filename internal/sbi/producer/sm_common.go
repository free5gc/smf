package producer

import (
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/sbi/consumer"
)

func RemoveSMContextFromAllNF(smContext *smf_context.SMContext, sendNotification bool) {
	smContext.SMContextState = smf_context.InActive
	logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
	// remove SM Policy Association
	if smContext.SMPolicyID != "" {
		if err := consumer.SendSMPolicyAssociationTermination(smContext); err != nil {
			logger.PduSessLog.Errorf("SM Policy Termination failed: %s", err)
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
		problemDetails, err := consumer.SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
		if problemDetails != nil || err != nil {
			if problemDetails != nil {
				logger.PduSessLog.Warnf("Send SMContext Status Notification Problem[%+v]", problemDetails)
			}

			if err != nil {
				logger.PduSessLog.Warnf("Send SMContext Status Notification Error[%v]", err)
			}
		} else {
			logger.PduSessLog.Traceln("Send SMContext Status Notification successfully")
		}
	}

	smf_context.RemoveSMContext(smContext.Ref)
}
