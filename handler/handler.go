package handler

import (
	"free5gc/lib/http_wrapper"
	"free5gc/lib/openapi/models"
	"free5gc/src/smf/handler/message"
	"free5gc/src/smf/pfcp"
	"free5gc/src/smf/producer"
	"net/http"
	"time"
)

func Handle() {

	for {
		select {
		case msg, ok := <-message.SmfChannel:
			if ok {
				switch msg.Event {
				case message.PFCPMessage:
					pfcp.Dispatch(msg.PFCPRequest)
				case message.PDUSessionSMContextCreate:
					producer.HandlePDUSessionSMContextCreate(msg.ResponseChan, msg.HTTPRequest.Body.(models.PostSmContextsRequest))
				case message.PDUSessionSMContextUpdate:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					seqNum, ResBody := producer.HandlePDUSessionSMContextUpdate(
						msg.ResponseChan, smContextRef, msg.HTTPRequest.Body.(models.UpdateSmContextRequest))
					response := http_wrapper.Response{
						Status: http.StatusOK,
						Body:   ResBody,
					}
					message.RspQueue.PutItem(seqNum, msg.ResponseChan, response)
				case message.PDUSessionSMContextRelease:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					seqNum := producer.HandlePDUSessionSMContextRelease(
						msg.ResponseChan, smContextRef, msg.HTTPRequest.Body.(models.ReleaseSmContextRequest))
					response := http_wrapper.Response{
						Status: http.StatusNoContent,
						Body:   nil,
					}
					message.RspQueue.PutItem(seqNum, msg.ResponseChan, response)
				case message.OAMGetUEPDUSessionInfo:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					producer.HandleOAMGetUEPDUSessionInfo(msg.ResponseChan, smContextRef)
				case message.SMPolicyUpdateNotify:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					producer.HandleSMPolicyUpdateNotify(msg.ResponseChan, smContextRef, msg.HTTPRequest.Body.(models.SmPolicyNotification))
				}
			}
		case <-time.After(time.Second * 1):
		}
	}
}
