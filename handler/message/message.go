package message

import (
	"free5gc/lib/http_wrapper"
	"free5gc/lib/pfcp/pfcpUdp"
	"net/http"
)

type HandlerMessage struct {
	Event        Event
	HTTPRequest  *http_wrapper.Request
	PFCPRequest  *pfcpUdp.Message
	ResponseChan chan HandlerResponseMessage
}

type HandlerResponseMessage struct {
	HTTPResponse *http_wrapper.Response
	PFCPResponse *pfcpUdp.Message
}

type ResponseQueueItem struct {
	RspChan  chan HandlerResponseMessage
	Response http_wrapper.Response
}

func NewPfcpMessage(pfcpRequest *pfcpUdp.Message) (msg HandlerMessage) {
	msg = HandlerMessage{}
	msg.Event = PFCPMessage
	msg.ResponseChan = make(chan HandlerResponseMessage)
	msg.PFCPRequest = pfcpRequest
	return
}

func NewHandlerMessage(event Event, httpRequest *http_wrapper.Request) (msg HandlerMessage) {
	msg = HandlerMessage{}
	msg.Event = event
	msg.ResponseChan = make(chan HandlerResponseMessage)
	if httpRequest != nil {
		msg.HTTPRequest = httpRequest
	}
	return
}

/* Send HTTP Response to HTTP handler thread through HTTP channel, args[0] is response payload and args[1:] is Additional Value*/
func SendHttpResponseMessage(channel chan HandlerResponseMessage, header http.Header, status int, body interface{}) {
	responseMsg := HandlerResponseMessage{}
	responseMsg.HTTPResponse = http_wrapper.NewResponse(status, header, body)
	channel <- responseMsg
}
