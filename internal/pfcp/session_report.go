package pfcp

import (
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// SessionReportHandler owns the SMF procedure behind a PFCP Session Report.
// PfcpServer keeps wire/transaction concerns here while Processor performs
// SM-context, AMF and charging work without importing the PFCP server package.
type SessionReportHandler interface {
	HandleSessionReportRequest(*message.SessionReportRequest) (cause uint8, remoteSEID uint64)
}

func (s *PfcpServer) SetSessionReportHandler(handler SessionReportHandler) {
	s.sessionReportMu.Lock()
	s.sessionReportHandler = handler
	s.sessionReportMu.Unlock()
}

func (s *PfcpServer) handleSessionReportRequest(
	request *message.SessionReportRequest,
) *message.SessionReportResponse {
	cause := ie.CauseServiceNotSupported
	remoteSEID := uint64(0)

	s.sessionReportMu.RLock()
	handler := s.sessionReportHandler
	s.sessionReportMu.RUnlock()
	if handler == nil {
		s.log.Warn("reject PFCP Session Report Request: session report handler is not configured")
	} else {
		cause, remoteSEID = handler.HandleSessionReportRequest(request)
	}

	return message.NewSessionReportResponse(
		0, 0, remoteSEID, request.Sequence(), 0,
		ie.NewCause(cause),
	)
}
