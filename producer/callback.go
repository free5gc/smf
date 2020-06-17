package producer

import (
	"free5gc/lib/http_wrapper"
	"free5gc/lib/openapi/models"
	"free5gc/src/smf/context"
	smf_message "free5gc/src/smf/handler/message"
	"free5gc/src/smf/logger"
	"net/http"
)

func HandleSMPolicyUpdateNotify(rspChan chan smf_message.HandlerResponseMessage, smContextRef string, request models.SmPolicyNotification) {

	// policyDecision := request.SmPolicyDecision

	smContext := context.GetSMContext(smContextRef)

	if smContext == nil {
		logger.PduSessLog.Errorf("SMContext[%s] not found", smContextRef)
		rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &http_wrapper.Response{
			Status: http.StatusBadRequest,
			Body:   nil,
		}}
		return
	}

	rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &http_wrapper.Response{
		Status: http.StatusNoContent,
		Body:   nil,
	}}
}
