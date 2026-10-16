package pfcp

import (
	"net"
	"runtime/debug"
	"sync"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// SetDispatch replaces the request callback used by the bounded dispatcher
// workers. NewPfcpServer installs PfcpServer.Dispatch by default; tests and
// embedders may replace it before Run.
func (s *PfcpServer) SetDispatch(dispatch func(message.Message, *net.UDPAddr)) {
	s.dispatchMu.Lock()
	s.dispatch = dispatch
	s.dispatchMu.Unlock()
}

// Dispatch routes concrete go-pfcp request messages to SMF handlers.
func (s *PfcpServer) Dispatch(msg message.Message, addr *net.UDPAddr) {
	var response message.Message
	var afterResponse func()
	switch request := msg.(type) {
	case *message.HeartbeatRequest:
		response = s.handleHeartbeatRequest(request)
	case *message.AssociationSetupRequest:
		response, afterResponse = s.handleAssociationSetupRequest(request)
	case *message.AssociationReleaseRequest:
		response = s.handleAssociationReleaseRequest(request)
	case *message.AssociationUpdateRequest:
		response, afterResponse = s.handleAssociationUpdateRequest(request)
	case *message.NodeReportRequest:
		// TODO(go-pfcp migration): parse Node Report Type and the report-specific
		// grouped IEs. Until then, reject explicitly so the peer and RxTransaction
		// both complete instead of waiting for a response that will never arrive.
		s.log.Warnf("PFCP Node Report Request handling is not implemented (sequence=%#x from=%v)",
			request.Sequence(), addr)
		response = message.NewNodeReportResponse(
			request.Sequence(), s.localNodeIDIE(), ie.NewCause(ie.CauseServiceNotSupported), nil,
		)
	case *message.SessionSetDeletionRequest:
		// TODO(go-pfcp migration): identify the targeted session set and coordinate
		// SM-context cleanup. Return an explicit rejection while unsupported so the
		// request transaction can complete and cache a replayable response.
		s.log.Warnf("PFCP Session Set Deletion Request handling is not implemented (sequence=%#x from=%v)",
			request.Sequence(), addr)
		response = message.NewSessionSetDeletionResponse(
			request.Sequence(), s.localNodeIDIE(), ie.NewCause(ie.CauseServiceNotSupported), nil,
		)
	case *message.SessionReportRequest:
		response = s.handleSessionReportRequest(request)
	default:
		s.log.Warnf("unsupported PFCP request %T from %v", msg, addr)
		return
	}

	if response != nil {
		if err := s.SendPfcpResponse(response, addr); err != nil {
			s.log.Errorf("send %s to %v: %v", response.MessageTypeName(), addr, err)
			return
		}
	}
	if afterResponse != nil {
		afterResponse()
	}
}

// dispatcher is one fixed worker in the bounded PFCP request pool.
func (s *PfcpServer) dispatcher(wg *sync.WaitGroup) {
	defer func() {
		s.log.Infoln("PFCP dispatcher stopped")
		wg.Done()
	}()

	for {
		if s.dispatchIteration() {
			return
		}
	}
}

// dispatchIteration gives infrastructure panics an iteration-sized failure
// boundary. The fixed worker remains available for the next request instead of
// terminating or taking the whole SMF down.
func (s *PfcpServer) dispatchIteration() (stopped bool) {
	var received *RcvPfcpMsg
	defer func() {
		if recovered := recover(); recovered != nil {
			if received != nil && received.Rx != nil {
				received.Rx.abortUnansweredAfterDispatchPanic()
			}
			s.log.Errorf("panic in PFCP dispatcher infrastructure: %v\n%s", recovered, debug.Stack())
			stopped = false
		}
	}()

	select {
	case <-s.stopCh:
		return true
	case queued, ok := <-s.dispCh:
		if !ok {
			return true
		}
		received = &queued
		// select does not prioritize stopCh when both cases are ready. Do not
		// begin another handler after shutdown has already started.
		if isServerStopped(s.stopCh) {
			if queued.Rx != nil {
				queued.Rx.stop()
			}
			return true
		}
		s.dispatchReceived(queued)
		return false
	}
}

func (s *PfcpServer) dispatchReceived(received RcvPfcpMsg) {
	if received.Msg == nil {
		if received.Rx != nil {
			received.Rx.stop()
		}
		return
	}

	s.dispatchMu.RLock()
	dispatch := s.dispatch
	s.dispatchMu.RUnlock()
	if dispatch == nil {
		if received.Rx != nil {
			received.Rx.stop()
		}
		return
	}
	if received.Rx == nil || !received.Rx.beginHandling() {
		s.log.Debugf("dropping stale queued PFCP request %s sequence %#x from %v",
			received.Msg.MessageTypeName(), received.Msg.Sequence(), &received.RemoteAddr)
		return
	}

	// Register lifecycle cleanup first so the later panic-recovery defer runs
	// before it. Recovery can abort an unanswered transaction; finishHandling
	// then observes done and does not accidentally restart its timer.
	defer received.Rx.finishHandling()
	defer func() {
		if recovered := recover(); recovered != nil {
			aborted := received.Rx.abortUnansweredAfterDispatchPanic()
			s.log.Errorf(
				"panic handling PFCP %s sequence %#x from %v (unanswered transaction aborted=%t): %v\n%s",
				received.Msg.MessageTypeName(), received.Msg.Sequence(), &received.RemoteAddr,
				aborted, recovered, debug.Stack(),
			)
		}
	}()

	// Synchronous execution is intentional: spawning here would turn blocked
	// handlers into an unbounded goroutine backlog.
	dispatch(received.Msg, &received.RemoteAddr)
}
