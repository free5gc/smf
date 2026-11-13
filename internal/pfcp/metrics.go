package pfcp

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	business_metrics "github.com/free5gc/smf/internal/metrics/business"
)

// pfcpRequestTypes pins the message_type label to a fixed set instead of go-pfcp's
// MessageTypeName(), so the label values do not change if the library renames a message.
var pfcpRequestTypes = map[uint8]string{
	message.MsgTypeHeartbeatRequest:            business_metrics.PFCP_MSG_HEARTBEAT,
	message.MsgTypeAssociationSetupRequest:     business_metrics.PFCP_MSG_ASSOCIATION_SETUP,
	message.MsgTypeAssociationReleaseRequest:   business_metrics.PFCP_MSG_ASSOCIATION_RELEASE,
	message.MsgTypeSessionEstablishmentRequest: business_metrics.PFCP_MSG_SESSION_ESTABLISHMENT,
	message.MsgTypeSessionModificationRequest:  business_metrics.PFCP_MSG_SESSION_MODIFICATION,
	message.MsgTypeSessionDeletionRequest:      business_metrics.PFCP_MSG_SESSION_DELETION,
}

// sendRequest is the single path for every PFCP request the SMF sends to a UPF. When PFCP metrics
// are enabled it records the result and round-trip time of the request.
func (s *PfcpServer) sendRequest(
	ctx context.Context,
	request message.Message,
	addr *net.UDPAddr,
) (message.Message, error) {
	if !business_metrics.IsPfcpMetricsEnabled() {
		return s.sendRequestAndWait(ctx, request, addr)
	}

	start := time.Now()
	response, err := s.sendRequestAndWait(ctx, request, addr)
	business_metrics.ObservePfcpRequest(
		pfcpMessageTypeLabel(request), pfcpUpfLabel(addr), pfcpResultLabel(request, response, err), time.Since(start),
	)
	return response, err
}

func pfcpMessageTypeLabel(request message.Message) string {
	if request == nil {
		return business_metrics.PFCP_MSG_OTHER
	}
	if label, ok := pfcpRequestTypes[request.MessageType()]; ok {
		return label
	}
	return business_metrics.PFCP_MSG_OTHER
}

// pfcpUpfLabel returns the UPF node ID the request was sent to. The SMF only sends requests to the
// UPFs configured in smfcfg.yaml, so the label has one value per configured UPF.
func pfcpUpfLabel(addr *net.UDPAddr) string {
	if addr == nil || addr.IP == nil {
		return ""
	}
	return addr.IP.String()
}

func pfcpResultLabel(request message.Message, response message.Message, err error) string {
	if errors.Is(err, errRequestTimedOut) {
		return business_metrics.PFCP_RESULT_TIMEOUT
	}
	if err != nil || request == nil || response == nil {
		return business_metrics.PFCP_RESULT_ERROR
	}
	// Every PFCP response type is the request type plus one (TS 29.244 Table 7.3-1).
	if response.MessageType() != request.MessageType()+1 {
		return business_metrics.PFCP_RESULT_ERROR
	}

	causeIE, hasCause := responseCause(response)
	if !hasCause {
		// Heartbeat Response carries no Cause: receiving it is the success.
		return business_metrics.PFCP_RESULT_SUCCESS
	}
	if causeIE == nil {
		return business_metrics.PFCP_RESULT_ERROR
	}
	cause, err := causeIE.Cause()
	if err != nil {
		return business_metrics.PFCP_RESULT_ERROR
	}
	if cause != ie.CauseRequestAccepted {
		return business_metrics.PFCP_RESULT_REJECTED
	}
	return business_metrics.PFCP_RESULT_SUCCESS
}

// responseCause returns the Cause IE of a response type that must carry one. hasCause is false for
// response types without a Cause IE.
func responseCause(response message.Message) (causeIE *ie.IE, hasCause bool) {
	switch rsp := response.(type) {
	case *message.SessionEstablishmentResponse:
		return rsp.Cause, true
	case *message.SessionModificationResponse:
		return rsp.Cause, true
	case *message.SessionDeletionResponse:
		return rsp.Cause, true
	case *message.AssociationSetupResponse:
		return rsp.Cause, true
	case *message.AssociationReleaseResponse:
		return rsp.Cause, true
	default:
		return nil, false
	}
}
