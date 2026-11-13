package pfcp

import (
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// Parse errors bypass procedure callbacks but use normal Rx caching, bounded
// dispatch and response sending. SEID=0 for a protocol rejection is permitted
// by TS 29.244 7.2.2.4.2; no session lookup or mutation is needed here.
func (s *PfcpServer) parseErrorResponse(request message.Message, failure *packetParseError) message.Message {
	ies := []*ie.IE{ie.NewCause(failure.cause)}
	if failure.offendingKnown {
		ies = append(ies, ie.NewOffendingIE(failure.offending))
	}
	nodeIEs := ies
	if nodeID := s.localNodeIDIE(); nodeID != nil {
		nodeIEs = append([]*ie.IE{nodeID}, ies...)
	}
	switch request.MessageType() {
	case message.MsgTypeAssociationSetupRequest:
		return message.NewAssociationSetupResponse(request.Sequence(), nodeIEs...)
	case message.MsgTypeAssociationUpdateRequest:
		return message.NewAssociationUpdateResponse(request.Sequence(), nodeIEs...)
	case message.MsgTypeAssociationReleaseRequest:
		return message.NewAssociationReleaseResponse(request.Sequence(), s.localNodeIDIE(), ies[0], ies[1:]...)
	case message.MsgTypeNodeReportRequest:
		return message.NewNodeReportResponse(request.Sequence(), s.localNodeIDIE(), ies[0], nil, ies[1:]...)
	case message.MsgTypeSessionSetDeletionRequest:
		return message.NewSessionSetDeletionResponse(request.Sequence(), s.localNodeIDIE(), ies[0], nil, ies[1:]...)
	case message.MsgTypeSessionReportRequest:
		return message.NewSessionReportResponse(0, 0, 0, request.Sequence(), 0, ies...)
	default:
		return nil
	}
}
