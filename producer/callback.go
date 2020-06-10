package producer

import (
	"free5gc/lib/http_wrapper"
	"free5gc/lib/openapi/models"
	smf_message "free5gc/src/smf/handler/message"
	"net/http"
)

func HandleSMPolicyUpdateNotify(rspChan chan smf_message.HandlerResponseMessage, request models.SmPolicyNotification) {

	rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &http_wrapper.Response{
		Status: http.StatusNoContent,
		Body:   nil,
	}}
}
