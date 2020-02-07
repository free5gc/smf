package smf_handler

import (
	"gofree5gc/lib/openapi/models"
	"gofree5gc/src/smf/smf_handler/smf_message"
	"gofree5gc/src/smf/smf_pfcp"
	"gofree5gc/src/smf/smf_producer"
	"time"
)

func Handle() {

	ResponseQueue := smf_message.NewQueue()
	for {
		select {
		case msg, ok := <-smf_message.SmfChannel:
			if ok {
				switch msg.Event {
				case smf_message.PFCPMessage:
					smf_pfcp.Dispatch(msg.PFCPRequest, ResponseQueue)
				case smf_message.PDUSessionSMContextCreate:
					smf_producer.HandlePDUSessionSMContextCreate(msg.ResponseChan, msg.HTTPRequest.Body.(models.PostSmContextsRequest))
				case smf_message.PDUSessionSMContextUpdate:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					seqNum, ResBody := smf_producer.HandlePDUSessionSMContextUpdate(
						msg.ResponseChan, smContextRef, msg.HTTPRequest.Body.(models.UpdateSmContextRequest))

					ResponseQueue.PutItem(
						seqNum, msg.ResponseChan, ResBody)
				case smf_message.PDUSessionSMContextRelease:
					smContextRef := msg.HTTPRequest.Params["smContextRef"]
					smf_producer.HandlePDUSessionSMContextRelease(
						msg.ResponseChan, smContextRef, msg.HTTPRequest.Body.(models.ReleaseSmContextRequest))
				}
			}
		case <-time.After(time.Second * 1):
		}
	}
}
