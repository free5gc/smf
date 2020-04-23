package handler

import (
	"free5gc/lib/http_wrapper"
	"free5gc/lib/openapi/models"
	"free5gc/src/smf/handler/message"
	"free5gc/src/smf/smf_pfcp"
	"free5gc/src/smf/smf_producer"
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
					smf_pfcp.Dispatch(msg.PFCPRequest)
				case message.PDUSessionSMContextCreate:
					smf_producer.HandlePDUSessionSMContextCreate(msg.ResponseChan, msg.HTTPRequest.Body.(models.PostSmContextsRequest))
				case message.PDUSessionSMContextUpdate:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					seqNum, ResBody := smf_producer.HandlePDUSessionSMContextUpdate(
						msg.ResponseChan, smContextRef, msg.HTTPRequest.Body.(models.UpdateSmContextRequest))
					response := http_wrapper.Response{
						Status: http.StatusOK,
						Body:   ResBody,
					}
					message.RspQueue.PutItem(seqNum, msg.ResponseChan, response)
				case message.PDUSessionSMContextRelease:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					seqNum := smf_producer.HandlePDUSessionSMContextRelease(
						msg.ResponseChan, smContextRef, msg.HTTPRequest.Body.(models.ReleaseSmContextRequest))
					response := http_wrapper.Response{
						Status: http.StatusNoContent,
						Body:   nil,
					}
					message.RspQueue.PutItem(seqNum, msg.ResponseChan, response)
				case message.OAMGetUEPDUSessionInfo:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					smf_producer.HandleOAMGetUEPDUSessionInfo(msg.ResponseChan, smContextRef)
				}
			}
		case <-time.After(time.Second * 1):
		}
	}
}
