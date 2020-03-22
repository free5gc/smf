package smf_producer

import (
	"gofree5gc/lib/http_wrapper"
	"gofree5gc/lib/openapi/models"
	"gofree5gc/src/smf/smf_context"
	"gofree5gc/src/smf/smf_handler/smf_message"
	"net"
	"net/http"
)

type PDUSessionInfo struct {
	Supi         string
	PDUSessionID int32
	LocalSEID    uint64
	RemoteSEID   uint64
	Dnn          string
	Snssai       *models.Snssai
	AnType       models.AccessType
	PDUAddress   net.IP
	QosRules     smf_context.QoSRules
	UpCnxState   models.UpCnxState
	Tunnel       *smf_context.UPTunnel
}

func HandleOAMGetUEPDUSessionInfo(rspChan chan smf_message.HandlerResponseMessage, smContextRef string) {
	smContext := smf_context.GetSMContext(smContextRef)
	if smContext == nil {
		rspChan <- smf_message.HandlerResponseMessage{
			HTTPResponse: &http_wrapper.Response{
				Header: nil,
				Status: http.StatusNotFound,
				Body:   nil,
			},
		}
		return
	}

	rspChan <- smf_message.HandlerResponseMessage{
		HTTPResponse: &http_wrapper.Response{
			Header: nil,
			Status: http.StatusOK,
			Body: PDUSessionInfo{
				Supi:         smContext.Supi,
				PDUSessionID: smContext.PDUSessionID,
				LocalSEID:    smContext.LocalSEID,
				RemoteSEID:   smContext.RemoteSEID,
				Dnn:          smContext.Dnn,
				Snssai:       smContext.Snssai,
				AnType:       smContext.AnType,
				PDUAddress:   smContext.PDUAddress,
				QosRules:     smContext.QoSRules,
				UpCnxState:   smContext.UpCnxState,
				Tunnel:       smContext.Tunnel,
			},
		},
	}
}
