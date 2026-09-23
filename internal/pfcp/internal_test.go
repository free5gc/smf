package pfcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/internal/sbi/processor"
	"github.com/free5gc/smf/pkg/factory"
)

// Tests consolidated from association_internal_test.go.
type fakeAssociationState struct {
	mu           sync.Mutex
	setupCause   uint8
	updateCause  uint8
	releaseCause uint8
	setupPeer    pfcptype.NodeID
	updatePeer   pfcptype.NodeID
	releasePeer  pfcptype.NodeID
	update       *message.AssociationUpdateRequest
	updateCalled bool
	recoveryTime time.Time
	after        func()
}

func (f *fakeAssociationState) SetupAssociation(
	peer pfcptype.NodeID,
	recoveryTime time.Time,
) (uint8, func()) {
	f.mu.Lock()
	f.setupPeer = peer
	f.recoveryTime = recoveryTime
	f.mu.Unlock()
	return f.setupCause, f.after
}

func (f *fakeAssociationState) UpdateAssociation(
	peer pfcptype.NodeID,
	request *message.AssociationUpdateRequest,
) (uint8, func()) {
	f.mu.Lock()
	f.updatePeer = peer
	f.update = request
	f.updateCalled = true
	f.mu.Unlock()
	return f.updateCause, f.after
}

func (f *fakeAssociationState) ReleaseAssociation(peer pfcptype.NodeID) uint8 {
	f.mu.Lock()
	f.releasePeer = peer
	f.mu.Unlock()
	return f.releaseCause
}

func TestHandleAssociationSetupRequestMissingMandatoryIE(t *testing.T) {
	s := NewPfcpServer(nil, "127.0.0.1")
	response, afterResponse := s.handleAssociationSetupRequest(message.NewAssociationSetupRequest(7))
	if afterResponse != nil {
		t.Fatal("afterResponse is non-nil for an invalid request")
	}
	assertCause(t, response.Cause, ie.CauseMandatoryIEMissing)
	if response.Sequence() != 7 {
		t.Fatalf("response sequence = %d, want 7", response.Sequence())
	}
}

func TestHandleAssociationSetupRequestWithoutLocalNodeIDDoesNotPanic(t *testing.T) {
	s := NewPfcpServer(nil, "")
	request := message.NewAssociationSetupRequest(
		8,
		ie.NewNodeIDHeuristic("192.0.2.10"),
		ie.NewRecoveryTimeStamp(time.Now()),
	)
	response, _ := s.handleAssociationSetupRequest(request)
	if response == nil {
		t.Fatal("handler returned nil response")
	}
	if response.NodeID != nil {
		t.Fatal("response unexpectedly contains a local Node ID")
	}
}

func TestHandleAssociationSetupRequestUsesStateManager(t *testing.T) {
	recoveryTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	afterCalled := false
	state := &fakeAssociationState{
		setupCause: ie.CauseRequestAccepted,
		after:      func() { afterCalled = true },
	}
	s := NewPfcpServer(nil, "127.0.0.1")
	s.SetAssociationStateManager(state)
	request := message.NewAssociationSetupRequest(
		9,
		ie.NewNodeIDHeuristic("192.0.2.10"),
		ie.NewRecoveryTimeStamp(recoveryTime),
	)

	response, afterResponse := s.handleAssociationSetupRequest(request)
	assertCause(t, response.Cause, ie.CauseRequestAccepted)
	if afterResponse == nil {
		t.Fatal("accepted setup did not return afterResponse")
	}
	if afterCalled {
		t.Fatal("afterResponse ran before the caller invoked it")
	}
	if got := state.setupPeer.String(); got != "192.0.2.10" {
		t.Fatalf("setup peer = %q, want 192.0.2.10", got)
	}
	if got, want := state.recoveryTime.Unix(), recoveryTime.Unix(); got != want {
		t.Fatalf("recovery time = %d, want %d", got, want)
	}
	afterResponse()
	if !afterCalled {
		t.Fatal("afterResponse did not run")
	}
}

func TestAssociationSetupDispatchRunsAfterResponse(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	afterCalled := make(chan struct{}, 1)
	s.SetAssociationStateManager(&fakeAssociationState{
		setupCause: ie.CauseRequestAccepted,
		after:      func() { afterCalled <- struct{}{} },
	})

	peer, err := net.DialUDP("udp", nil, s.LocalAddr())
	if err != nil {
		t.Fatalf("DialUDP() error: %v", err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	}()
	request := message.NewAssociationSetupRequest(
		11,
		ie.NewNodeIDHeuristic("127.0.0.2"),
		ie.NewRecoveryTimeStamp(time.Now()),
	)
	packet, err := request.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	if _, err = peer.Write(packet); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if err = peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	responsePacket := make([]byte, 1500)
	n, err := peer.Read(responsePacket)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	parsed, err := message.Parse(responsePacket[:n])
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	response, ok := parsed.(*message.AssociationSetupResponse)
	if !ok {
		t.Fatalf("response = %T, want *message.AssociationSetupResponse", parsed)
	}
	assertCause(t, response.Cause, ie.CauseRequestAccepted)
	select {
	case <-afterCalled:
	case <-time.After(time.Second):
		t.Fatal("afterResponse was not run after response send")
	}
}

func TestHandleAssociationUpdateRequestMissingNodeID(t *testing.T) {
	s := NewPfcpServer(nil, "127.0.0.1")
	response, afterResponse := s.handleAssociationUpdateRequest(
		message.NewAssociationUpdateRequest(12),
	)
	if afterResponse != nil {
		t.Fatal("afterResponse is non-nil for an invalid request")
	}
	assertCause(t, response.Cause, ie.CauseMandatoryIEMissing)
	if response.Sequence() != 12 {
		t.Fatalf("response sequence = %d, want 12", response.Sequence())
	}
}

func TestHandleAssociationUpdateRequestUsesStateManager(t *testing.T) {
	period := 30 * time.Second
	afterCalled := false
	state := &fakeAssociationState{
		updateCause: ie.CauseRequestAccepted,
		after:       func() { afterCalled = true },
	}
	s := NewPfcpServer(nil, "127.0.0.1")
	s.SetAssociationStateManager(state)
	request := message.NewAssociationUpdateRequest(
		14,
		ie.NewNodeIDHeuristic("192.0.2.20"),
		ie.NewUPFunctionFeatures(0x11, 0x22),
		ie.NewPFCPAssociationReleaseRequest(1, 1),
		ie.NewGracefulReleasePeriod(period),
		ie.NewPFCPAUReqFlags(0x01),
	)

	response, afterResponse := s.handleAssociationUpdateRequest(request)
	assertCause(t, response.Cause, ie.CauseRequestAccepted)
	if response.NodeID == nil || response.CPFunctionFeatures == nil {
		t.Fatal("accepted response is missing local Node ID or CP Function Features")
	}
	if afterResponse == nil {
		t.Fatal("accepted release update did not return afterResponse")
	}
	if afterCalled {
		t.Fatal("afterResponse ran before the caller invoked it")
	}
	if got := state.updatePeer.String(); got != "192.0.2.20" {
		t.Fatalf("update peer = %q, want 192.0.2.20", got)
	}
	if state.update != request {
		t.Fatal("state manager did not receive the original go-pfcp Association Update Request")
	}
	features, err := state.update.UPFunctionFeatures.UPFunctionFeatures()
	if err != nil || len(features) != 2 || features[0] != 0x11 || features[1] != 0x22 {
		t.Fatalf("UP Function Features = %v, err=%v; want [0x11 0x22]", features, err)
	}
	if !state.update.PFCPAssociationReleaseRequest.HasSARR() ||
		!state.update.PFCPAssociationReleaseRequest.HasURSS() ||
		!state.update.PFCPAUReqFlags.HasPARPS() {
		t.Fatal("state manager request is missing SARR, URSS, or PARPS")
	}
	gotPeriod, err := state.update.GracefulReleasePeriod.GracefulReleasePeriod()
	if err != nil || gotPeriod != period {
		t.Fatalf("Graceful Release Period = %v, err=%v; want %s", gotPeriod, err, period)
	}
	afterResponse()
	if !afterCalled {
		t.Fatal("afterResponse did not run")
	}
}

func TestHandleAssociationUpdateRequestRejectsGracePeriodWithoutReleaseIE(t *testing.T) {
	state := &fakeAssociationState{updateCause: ie.CauseRequestAccepted}
	s := NewPfcpServer(nil, "127.0.0.1")
	s.SetAssociationStateManager(state)
	response, afterResponse := s.handleAssociationUpdateRequest(
		message.NewAssociationUpdateRequest(
			15,
			ie.NewNodeIDHeuristic("192.0.2.21"),
			ie.NewGracefulReleasePeriod(time.Minute),
		),
	)
	if afterResponse != nil {
		t.Fatal("afterResponse is non-nil for an invalid request")
	}
	assertCause(t, response.Cause, ie.CauseConditionalIEMissing)
	if state.updateCalled {
		t.Fatal("state manager was called for invalid request")
	}
}

func TestAssociationUpdateDispatchRunsAfterResponse(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	afterCalled := make(chan struct{}, 1)
	s.SetAssociationStateManager(&fakeAssociationState{
		updateCause: ie.CauseRequestAccepted,
		after:       func() { afterCalled <- struct{}{} },
	})

	peer, err := net.DialUDP("udp", nil, s.LocalAddr())
	if err != nil {
		t.Fatalf("DialUDP() error: %v", err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	}()
	request := message.NewAssociationUpdateRequest(
		16,
		ie.NewNodeIDHeuristic("127.0.0.2"),
		ie.NewPFCPAssociationReleaseRequest(1, 0),
	)
	packet, err := request.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	if _, err = peer.Write(packet); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if err = peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	responsePacket := make([]byte, 1500)
	n, err := peer.Read(responsePacket)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	parsed, err := message.Parse(responsePacket[:n])
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	response, ok := parsed.(*message.AssociationUpdateResponse)
	if !ok {
		t.Fatalf("response = %T, want *message.AssociationUpdateResponse", parsed)
	}
	assertCause(t, response.Cause, ie.CauseRequestAccepted)
	select {
	case <-afterCalled:
	case <-time.After(time.Second):
		t.Fatal("afterResponse was not run after response send")
	}
}

func TestHandleAssociationReleaseRequest(t *testing.T) {
	state := &fakeAssociationState{releaseCause: ie.CauseRequestAccepted}
	s := NewPfcpServer(nil, "127.0.0.1")
	s.SetAssociationStateManager(state)
	response := s.handleAssociationReleaseRequest(message.NewAssociationReleaseRequest(
		13,
		ie.NewNodeIDHeuristic("upf.example.net"),
	))
	assertCause(t, response.Cause, ie.CauseRequestAccepted)
	if got := state.releasePeer.String(); got != "upf.example.net" {
		t.Fatalf("release peer = %q, want upf.example.net", got)
	}
}

func TestSendAssociationSetupRequest(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	upfRecoveryTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		request, ok := received.(*message.AssociationSetupRequest)
		if !ok {
			return nil, fmt.Errorf("received %T, want *message.AssociationSetupRequest", received)
		}
		if request.NodeID == nil || request.RecoveryTimeStamp == nil {
			return nil, fmt.Errorf("Association Setup Request is missing mandatory IE(s)")
		}
		return message.NewAssociationSetupResponse(
			request.Sequence(),
			ie.NewNodeIDHeuristic("127.0.0.2"),
			ie.NewCause(ie.CauseRequestAccepted),
			ie.NewRecoveryTimeStamp(upfRecoveryTime),
		), nil
	})

	response, err := s.SendAssociationSetupRequest(context.Background(), peerAddr)
	if err != nil {
		t.Fatalf("SendAssociationSetupRequest() error: %v", err)
	}
	if err = <-peerDone; err != nil {
		t.Fatalf("UPF peer error: %v", err)
	}
	recoveryTime, err := response.RecoveryTimeStamp.RecoveryTimeStamp()
	if err != nil {
		t.Fatalf("decode Recovery Time Stamp: %v", err)
	}
	if got, want := recoveryTime.Unix(), upfRecoveryTime.Unix(); got != want {
		t.Fatalf("UPF recovery time = %d, want %d", got, want)
	}
}

func TestSendAssociationSetupRequestRejectsMissingMandatoryIE(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		return message.NewAssociationSetupResponse(
			received.Sequence(),
			ie.NewNodeIDHeuristic("127.0.0.2"),
			ie.NewCause(ie.CauseRequestAccepted),
		), nil
	})
	_, err := s.SendAssociationSetupRequest(context.Background(), peerAddr)
	if err == nil || !strings.Contains(err.Error(), "missing mandatory") {
		t.Fatalf("SendAssociationSetupRequest() error = %v, want mandatory IE error", err)
	}
	if peerErr := <-peerDone; peerErr != nil {
		t.Fatalf("UPF peer error: %v", peerErr)
	}
}

func TestSendAssociationReleaseRequest(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		request, ok := received.(*message.AssociationReleaseRequest)
		if !ok {
			return nil, fmt.Errorf("received %T, want *message.AssociationReleaseRequest", received)
		}
		return message.NewAssociationReleaseResponse(
			request.Sequence(),
			ie.NewNodeIDHeuristic("127.0.0.2"),
			ie.NewCause(ie.CauseRequestAccepted),
		), nil
	})
	if _, err := s.SendAssociationReleaseRequest(context.Background(), peerAddr); err != nil {
		t.Fatalf("SendAssociationReleaseRequest() error: %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("UPF peer error: %v", err)
	}
}

func TestNodeIDFromIE(t *testing.T) {
	tests := []struct {
		name string
		ie   *ie.IE
		want string
	}{
		{name: "IPv4", ie: ie.NewNodeIDHeuristic("192.0.2.1"), want: "192.0.2.1"},
		{name: "IPv6", ie: ie.NewNodeIDHeuristic("2001:db8::1"), want: "2001:db8::1"},
		{name: "FQDN", ie: ie.NewNodeIDHeuristic("upf.example.net"), want: "upf.example.net"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nodeID, err := nodeIDFromIE(test.ie)
			if err != nil {
				t.Fatalf("nodeIDFromIE() error: %v", err)
			}
			if got := nodeID.String(); got != test.want {
				t.Fatalf("node ID = %q, want %q", got, test.want)
			}
		})
	}
}

func startAssociationPeer(
	t *testing.T,
	buildResponse func(message.Message) (message.Message, error),
) (*net.UDPAddr, <-chan error) {
	t.Helper()
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	})
	done := make(chan error, 1)
	go func() {
		packet := make([]byte, 1500)
		n, smfAddr, readErr := peer.ReadFromUDP(packet)
		if readErr != nil {
			done <- readErr
			return
		}
		request, parseErr := message.Parse(packet[:n])
		if parseErr != nil {
			done <- parseErr
			return
		}
		response, buildErr := buildResponse(request)
		if buildErr != nil {
			done <- buildErr
			return
		}
		responsePacket := make([]byte, response.MarshalLen())
		if marshalErr := response.MarshalTo(responsePacket); marshalErr != nil {
			done <- marshalErr
			return
		}
		_, writeErr := peer.WriteToUDP(responsePacket, smfAddr)
		done <- writeErr
	}()
	return peer.LocalAddr().(*net.UDPAddr), done
}

func assertCause(t *testing.T, causeIE *ie.IE, want uint8) {
	t.Helper()
	if causeIE == nil {
		t.Fatal("Cause IE is nil")
	}
	got, err := causeIE.Cause()
	if err != nil {
		t.Fatalf("Cause() error: %v", err)
	}
	if got != want {
		t.Fatalf("Cause = %d, want %d", got, want)
	}
}

// Tests consolidated from dispatcher_recovery_internal_test.go.
func startDispatcherRecoveryServer(t *testing.T, workers int) *PfcpServer {
	t.Helper()
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.port = 0
	s.dispatchWorkers = workers
	s.retransTimeout = 5 * time.Second
	s.maxRetrans = 0

	var wg sync.WaitGroup
	if err := s.Run(&wg); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	t.Cleanup(func() {
		s.Stop()
		wg.Wait()
	})
	return s
}

func TestDispatcherRequestPanicKeepsWorkerAvailable(t *testing.T) {
	s := startDispatcherRecoveryServer(t, 1)
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.61"), Port: PfcpPort}
	secondHandled := make(chan struct{}, 1)

	s.SetDispatch(func(msg message.Message, _ *net.UDPAddr) {
		if msg.Sequence() == 1 {
			panic("injected request handler failure")
		}
		if msg.Sequence() == 2 {
			secondHandled <- struct{}{}
		}
	})

	s.transactionHandler(message.NewHeartbeatRequest(
		1, ie.NewRecoveryTimeStamp(time.Now()), nil,
	), peer)
	firstID := TransactionID(peer, 1)
	deadline := time.Now().Add(time.Second)
	for {
		if _, found := s.loadRxTransaction(firstID); !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("panicking request transaction was not aborted")
		}
		time.Sleep(time.Millisecond)
	}

	s.transactionHandler(message.NewHeartbeatRequest(
		2, ie.NewRecoveryTimeStamp(time.Now()), nil,
	), peer)
	select {
	case <-secondHandled:
	case <-time.After(time.Second):
		t.Fatal("the fixed dispatcher worker did not process a request after recovering from panic")
	}
}

func TestDispatcherPanicAfterResponseKeepsCacheAndWorker(t *testing.T) {
	s := startDispatcherRecoveryServer(t, 1)
	var dispatchCount atomic.Int32
	sendErrors := make(chan error, 2)
	s.SetDispatch(func(msg message.Message, addr *net.UDPAddr) {
		dispatchCount.Add(1)
		err := s.SendPfcpResponse(message.NewHeartbeatResponse(
			msg.Sequence(), ie.NewRecoveryTimeStamp(s.RecoveryTime()),
		), addr)
		if err != nil {
			sendErrors <- err
			return
		}
		if msg.Sequence() == 71 {
			panic("injected after-response failure")
		}
	})

	peer, err := net.DialUDP("udp", nil, s.LocalAddr())
	if err != nil {
		t.Fatalf("DialUDP() error: %v", err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	}()

	firstPacket := marshalDispatcherHeartbeatRequest(t, 71)
	sendAndReadDispatcherHeartbeatResponse(t, peer, firstPacket, 71)

	// The panic happened after rx.send cached the response. A duplicate request
	// must reuse that cache instead of executing the handler again.
	sendAndReadDispatcherHeartbeatResponse(t, peer, firstPacket, 71)
	if got := dispatchCount.Load(); got != 1 {
		t.Fatalf("dispatch count after duplicate = %d, want 1", got)
	}

	// With exactly one worker this proves that the same fixed worker survived.
	sendAndReadDispatcherHeartbeatResponse(t, peer, marshalDispatcherHeartbeatRequest(t, 72), 72)
	if got := dispatchCount.Load(); got != 2 {
		t.Fatalf("dispatch count after next request = %d, want 2", got)
	}
	select {
	case err = <-sendErrors:
		t.Fatalf("SendPfcpResponse() error: %v", err)
	default:
	}
}

func TestDispatchIterationDoesNotStartWorkAfterStop(t *testing.T) {
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	peer := net.UDPAddr{IP: net.ParseIP("192.0.2.62"), Port: PfcpPort}
	rx := s.newRxTransaction(&peer, 1)
	t.Cleanup(rx.stop)
	var dispatched atomic.Bool
	s.SetDispatch(func(message.Message, *net.UDPAddr) { dispatched.Store(true) })
	s.dispCh <- RcvPfcpMsg{
		RemoteAddr: peer,
		Msg: message.NewHeartbeatRequest(
			1, ie.NewRecoveryTimeStamp(time.Now()), nil,
		),
		Rx: rx,
	}

	s.Stop()
	if stopped := s.dispatchIteration(); !stopped {
		t.Fatal("dispatch iteration did not stop after stopCh was closed")
	}
	if dispatched.Load() {
		t.Fatal("dispatcher started a handler after stopCh was closed")
	}
}

func marshalDispatcherHeartbeatRequest(t *testing.T, sequence uint32) []byte {
	t.Helper()
	packet, err := message.NewHeartbeatRequest(
		sequence, ie.NewRecoveryTimeStamp(time.Now()), nil,
	).Marshal()
	if err != nil {
		t.Fatalf("marshal Heartbeat Request: %v", err)
	}
	return packet
}

func sendAndReadDispatcherHeartbeatResponse(
	t *testing.T,
	peer *net.UDPConn,
	packet []byte,
	sequence uint32,
) {
	t.Helper()
	if _, err := peer.Write(packet); err != nil {
		t.Fatalf("Write() sequence %d: %v", sequence, err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() sequence %d: %v", sequence, err)
	}
	buffer := make([]byte, 1500)
	n, err := peer.Read(buffer)
	if err != nil {
		t.Fatalf("Read() sequence %d: %v", sequence, err)
	}
	parsed, err := message.Parse(buffer[:n])
	if err != nil {
		t.Fatalf("parse response sequence %d: %v", sequence, err)
	}
	response, ok := parsed.(*message.HeartbeatResponse)
	if !ok {
		t.Fatalf("response sequence %d = %T, want *message.HeartbeatResponse", sequence, parsed)
	}
	if response.Sequence() != sequence {
		t.Fatalf("response sequence = %d, want %d", response.Sequence(), sequence)
	}
}

// Tests consolidated from heartbeat_internal_test.go.
func TestSendHeartbeatRequest(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	upfRecoveryTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	peerAddr, peerDone := startHeartbeatResponsePeer(t, func(req *message.HeartbeatRequest) (message.Message, error) {
		if req.RecoveryTimeStamp == nil {
			return nil, fmt.Errorf("Heartbeat Request is missing Recovery Time Stamp")
		}
		smfRecoveryTime, err := req.RecoveryTimeStamp.RecoveryTimeStamp()
		if err != nil {
			return nil, fmt.Errorf("decode request Recovery Time Stamp: %w", err)
		}
		if got, want := smfRecoveryTime.Unix(), s.RecoveryTime().Unix(); got != want {
			return nil, fmt.Errorf("request Recovery Time Stamp = %d, want %d", got, want)
		}
		return message.NewHeartbeatResponse(
			req.Sequence(),
			ie.NewRecoveryTimeStamp(upfRecoveryTime),
		), nil
	})

	response, err := s.SendHeartbeatRequest(context.Background(), peerAddr)
	if err != nil {
		t.Fatalf("SendHeartbeatRequest() error: %v", err)
	}
	if err = <-peerDone; err != nil {
		t.Fatalf("UPF peer error: %v", err)
	}
	recoveryTime, err := response.RecoveryTimeStamp.RecoveryTimeStamp()
	if err != nil {
		t.Fatalf("decode response Recovery Time Stamp: %v", err)
	}
	if got, want := recoveryTime.Unix(), upfRecoveryTime.Unix(); got != want {
		t.Fatalf("response Recovery Time Stamp = %d, want %d", got, want)
	}
}

func TestSendHeartbeatRequestRejectsMissingRecoveryTimeStamp(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	peerAddr, peerDone := startHeartbeatResponsePeer(t, func(req *message.HeartbeatRequest) (message.Message, error) {
		return message.NewHeartbeatResponse(req.Sequence(), nil), nil
	})

	_, err := s.SendHeartbeatRequest(context.Background(), peerAddr)
	if err == nil || !strings.Contains(err.Error(), "missing Recovery Time Stamp") {
		t.Fatalf("SendHeartbeatRequest() error = %v, want missing Recovery Time Stamp", err)
	}
	if peerErr := <-peerDone; peerErr != nil {
		t.Fatalf("UPF peer error: %v", peerErr)
	}
}

func TestSendHeartbeatRequestRejectsUnexpectedResponse(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	peerAddr, peerDone := startHeartbeatResponsePeer(t, func(req *message.HeartbeatRequest) (message.Message, error) {
		return message.NewAssociationUpdateResponse(req.Sequence()), nil
	})

	_, err := s.SendHeartbeatRequest(context.Background(), peerAddr)
	if err == nil || !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("SendHeartbeatRequest() error = %v, want unexpected response", err)
	}
	if peerErr := <-peerDone; peerErr != nil {
		t.Fatalf("UPF peer error: %v", peerErr)
	}
}

func TestSendHeartbeatRequestTimeout(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	blackHole := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}

	_, err := s.SendHeartbeatRequest(context.Background(), blackHole)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("SendHeartbeatRequest() error = %v, want timeout", err)
	}
}

func TestSendHeartbeatRequestRejectsStoppedServer(t *testing.T) {
	s, wg := startTestPfcpServer(t)
	s.Stop()
	wg.Wait()

	peer := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: PfcpPort}
	_, err := s.SendHeartbeatRequest(context.Background(), peer)
	if err == nil || !strings.Contains(err.Error(), "server is stopped") {
		t.Fatalf("SendHeartbeatRequest() error = %v, want server stopped", err)
	}
}

func TestSendHeartbeatRequestRejectsUnspecifiedDestination(t *testing.T) {
	s := NewPfcpServer(nil, "127.0.0.1")
	for _, addr := range []*net.UDPAddr{nil, {IP: net.IPv4zero, Port: PfcpPort}} {
		if _, err := s.SendHeartbeatRequest(context.Background(), addr); err == nil {
			t.Fatalf("SendHeartbeatRequest(%v) succeeded, want destination error", addr)
		}
	}
}

func startHeartbeatResponsePeer(
	t *testing.T,
	buildResponse func(*message.HeartbeatRequest) (message.Message, error),
) (*net.UDPAddr, <-chan error) {
	t.Helper()
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	})

	done := make(chan error, 1)
	go func() {
		packet := make([]byte, 1500)
		n, smfAddr, readErr := peer.ReadFromUDP(packet)
		if readErr != nil {
			done <- readErr
			return
		}
		parsed, parseErr := message.Parse(packet[:n])
		if parseErr != nil {
			done <- parseErr
			return
		}
		request, ok := parsed.(*message.HeartbeatRequest)
		if !ok {
			done <- fmt.Errorf("received %T, want *message.HeartbeatRequest", parsed)
			return
		}
		response, buildErr := buildResponse(request)
		if buildErr != nil {
			done <- buildErr
			return
		}
		responsePacket := make([]byte, response.MarshalLen())
		if marshalErr := response.MarshalTo(responsePacket); marshalErr != nil {
			done <- marshalErr
			return
		}
		_, writeErr := peer.WriteToUDP(responsePacket, smfAddr)
		done <- writeErr
	}()
	return peer.LocalAddr().(*net.UDPAddr), done
}

// Tests consolidated from server_internal_test.go.
type fakeSmf struct {
	cfg *factory.Config
}

func (f *fakeSmf) Config() *factory.Config { return f.cfg }

func startTestPfcpServer(t *testing.T) (*PfcpServer, *sync.WaitGroup) {
	t.Helper()

	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.port = 0
	s.dispatchWorkers = 2
	s.retransTimeout = 20 * time.Millisecond
	s.maxRetrans = 1

	var wg sync.WaitGroup
	if err := s.Run(&wg); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	t.Cleanup(func() {
		s.Stop()
		wg.Wait()
	})
	return s, &wg
}

func TestPfcpServerHeartbeatRoundTrip(t *testing.T) {
	s, _ := startTestPfcpServer(t)

	peer, err := net.DialUDP("udp", nil, s.LocalAddr())
	if err != nil {
		t.Fatalf("DialUDP() error: %v", err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	}()

	req := message.NewHeartbeatRequest(1, ie.NewRecoveryTimeStamp(time.Now()), nil)
	b, err := req.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	if _, err = peer.Write(b); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	if err = peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error: %v", err)
	}
	rbuf := make([]byte, 1500)
	n, err := peer.Read(rbuf)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	rsp, err := message.Parse(rbuf[:n])
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	heartbeatResponse, ok := rsp.(*message.HeartbeatResponse)
	if !ok {
		t.Fatalf("response = %T, want *message.HeartbeatResponse", rsp)
	}
	if heartbeatResponse.Sequence() != req.Sequence() {
		t.Fatalf("response sequence = %d, want %d", heartbeatResponse.Sequence(), req.Sequence())
	}
	if heartbeatResponse.RecoveryTimeStamp == nil {
		t.Fatal("Heartbeat Response is missing Recovery Time Stamp")
	}
	recoveryTime, err := heartbeatResponse.RecoveryTimeStamp.RecoveryTimeStamp()
	if err != nil {
		t.Fatalf("decode Recovery Time Stamp: %v", err)
	}
	if got, want := recoveryTime.Unix(), s.RecoveryTime().Unix(); got != want {
		t.Fatalf("Recovery Time Stamp = %d, want %d", got, want)
	}
}

func TestPfcpServerMatchesOutboundResponse(t *testing.T) {
	s, _ := startTestPfcpServer(t)

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error: %v", err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	}()

	peerDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1500)
		n, addr, readErr := peer.ReadFromUDP(buf)
		if readErr != nil {
			peerDone <- readErr
			return
		}
		req, parseErr := message.Parse(buf[:n])
		if parseErr != nil {
			peerDone <- parseErr
			return
		}
		rsp := message.NewHeartbeatResponse(req.Sequence(), ie.NewRecoveryTimeStamp(time.Now()))
		b, marshalErr := rsp.Marshal()
		if marshalErr != nil {
			peerDone <- marshalErr
			return
		}
		_, writeErr := peer.WriteToUDP(b, addr)
		peerDone <- writeErr
	}()

	req := message.NewHeartbeatRequest(0, ie.NewRecoveryTimeStamp(time.Now()), nil)
	got, err := s.sendRequest(context.Background(), req, peer.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("sendRequest() error: %v", err)
	}
	if _, ok := got.(*message.HeartbeatResponse); !ok {
		t.Fatalf("response = %T, want *message.HeartbeatResponse", got)
	}
	if err = <-peerDone; err != nil {
		t.Fatalf("peer error: %v", err)
	}
}

func TestPfcpServerTxTimeout(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	blackHole := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}
	req := message.NewHeartbeatRequest(0, ie.NewRecoveryTimeStamp(time.Now()), nil)

	_, err := s.sendRequest(context.Background(), req, blackHole)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("sendRequest() error = %v, want timeout", err)
	}
}

func TestPfcpServerStopUnblocksPendingSenders(t *testing.T) {
	s, wg := startTestPfcpServer(t)
	blackHole := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}
	req := message.NewHeartbeatRequest(0, ie.NewRecoveryTimeStamp(time.Now()), nil)
	sendDone := make(chan error, 1)
	go func() {
		_, err := s.sendRequest(context.Background(), req, blackHole)
		sendDone <- err
	}()

	// Wait until the main loop has created the transaction before stopping.
	deadline := time.Now().Add(time.Second)
	for transactionCount(&s.txTrans) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if transactionCount(&s.txTrans) == 0 {
		t.Fatal("request transaction was not created")
	}

	s.Stop()
	wg.Wait()

	select {
	case err := <-sendDone:
		if err == nil || !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("sendRequest() error after Stop = %v, want stopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not unblock the pending sender")
	}

	// Stop is intentionally idempotent because app shutdown paths may converge.
	s.Stop()
}

func TestPfcpServerDuplicateRequestUsesCachedResponse(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	var dispatchCount atomic.Int32
	s.SetDispatch(func(msg message.Message, addr *net.UDPAddr) {
		dispatchCount.Add(1)
		if err := s.SendPfcpResponse(message.NewHeartbeatResponse(
			msg.Sequence(),
			ie.NewRecoveryTimeStamp(s.RecoveryTime()),
		), addr); err != nil {
			t.Errorf("SendPfcpResponse() error: %v", err)
		}
	})

	peer, err := net.DialUDP("udp", nil, s.LocalAddr())
	if err != nil {
		t.Fatalf("DialUDP() error: %v", err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	}()

	req := message.NewHeartbeatRequest(7, ie.NewRecoveryTimeStamp(time.Now()), nil)
	b, err := req.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	buf := make([]byte, 1500)
	for i := 0; i < 2; i++ {
		if _, err = peer.Write(b); err != nil {
			t.Fatalf("Write() #%d error: %v", i+1, err)
		}
		if err = peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err = peer.Read(buf); err != nil {
			t.Fatalf("Read() #%d error: %v", i+1, err)
		}
	}

	if got := dispatchCount.Load(); got != 1 {
		t.Fatalf("dispatch count = %d, want 1", got)
	}
}

func TestNewPfcpServerUsesConfiguredDispatchWorkers(t *testing.T) {
	cfg := &factory.Config{Configuration: &factory.Configuration{PFCP: &factory.PFCP{
		DispatchWorkerCount: 3,
	}}}
	s := NewPfcpServer(&fakeSmf{cfg: cfg}, "127.0.0.1")
	if got := s.dispatchWorkers; got != 3 {
		t.Fatalf("dispatch workers = %d, want 3", got)
	}
}

func TestPfcpServerDispatchConcurrencyIsBounded(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	// This test intentionally blocks all workers; keep queued jobs alive long
	// enough to verify the worker bound rather than the stale-request policy.
	s.retransTimeout = 5 * time.Second
	s.maxRetrans = 0
	workers := s.dispatchWorkers
	jobs := workers * 4
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	started := make(chan struct{}, jobs)
	var active atomic.Int32
	var maxActive atomic.Int32
	var completed atomic.Int32
	s.SetDispatch(func(message.Message, *net.UDPAddr) {
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		completed.Add(1)
	})

	peer := net.UDPAddr{IP: net.ParseIP("192.0.2.20"), Port: PfcpPort}
	for sequence := 1; sequence <= jobs; sequence++ {
		rx := s.newRxTransaction(&peer, uint32(sequence))
		s.dispCh <- RcvPfcpMsg{
			RemoteAddr: peer,
			Msg: message.NewHeartbeatRequest(
				uint32(sequence),
				ie.NewRecoveryTimeStamp(time.Now()),
				nil,
			),
			Rx: rx,
		}
	}

	for worker := 0; worker < workers; worker++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("only %d/%d dispatch workers started", worker, workers)
		}
	}
	select {
	case <-started:
		t.Fatalf("more than %d handlers ran concurrently", workers)
	case <-time.After(50 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })
	deadline := time.Now().Add(time.Second)
	for int(completed.Load()) != jobs && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := int(completed.Load()); got != jobs {
		t.Fatalf("completed handlers = %d, want %d", got, jobs)
	}
	if got := int(maxActive.Load()); got > workers {
		t.Fatalf("maximum concurrent handlers = %d, worker limit = %d", got, workers)
	}
}

func TestPfcpServerDropsStaleQueuedRequest(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.21"), Port: PfcpPort}
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	started := make(chan struct{}, s.dispatchWorkers)
	var dispatched atomic.Int32
	s.SetDispatch(func(message.Message, *net.UDPAddr) {
		dispatched.Add(1)
		started <- struct{}{}
		<-release
	})

	// Occupy every worker with claimed transactions. Claimed requests must not
	// expire even though their handler runs beyond the queue timeout.
	for sequence := uint32(1); sequence <= uint32(s.dispatchWorkers); sequence++ {
		s.transactionHandler(message.NewHeartbeatRequest(
			sequence, ie.NewRecoveryTimeStamp(time.Now()), nil,
		), peer)
	}
	for worker := 0; worker < s.dispatchWorkers; worker++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("only %d/%d dispatch workers started", worker, s.dispatchWorkers)
		}
	}

	staleSequence := uint32(s.dispatchWorkers + 1)
	s.transactionHandler(message.NewHeartbeatRequest(
		staleSequence, ie.NewRecoveryTimeStamp(time.Now()), nil,
	), peer)
	staleID := TransactionID(peer, staleSequence)
	deadline := time.Now().Add(time.Second)
	for {
		if _, found := s.loadRxTransaction(staleID); !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued request did not expire")
		}
		time.Sleep(time.Millisecond)
	}

	// The transactions already claimed by workers remain live while handlers
	// are running, instead of expiring under their feet.
	for sequence := uint32(1); sequence <= uint32(s.dispatchWorkers); sequence++ {
		if _, found := s.loadRxTransaction(TransactionID(peer, sequence)); !found {
			t.Fatalf("claimed request sequence %d expired during handling", sequence)
		}
	}

	releaseOnce.Do(func() { close(release) })
	deadline = time.Now().Add(time.Second)
	for len(s.dispCh) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.dispCh) != 0 {
		t.Fatal("stale request was not removed from the dispatch queue")
	}
	// Give the worker that dequeued the stale item time to perform the claim.
	time.Sleep(10 * time.Millisecond)
	if got, want := dispatched.Load(), int32(s.dispatchWorkers); got != want {
		t.Fatalf("dispatched handlers = %d, want %d; stale request executed", got, want)
	}
}

func TestPfcpServerDispatchOverflowReleasesRxTransaction(t *testing.T) {
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.dispCh = make(chan RcvPfcpMsg, 1)
	s.dispCh <- RcvPfcpMsg{
		Msg: message.NewHeartbeatRequest(1, ie.NewRecoveryTimeStamp(time.Now()), nil),
	}

	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.30"), Port: PfcpPort}
	req := message.NewHeartbeatRequest(2, ie.NewRecoveryTimeStamp(time.Now()), nil)
	s.transactionHandler(req, peer)

	if got := transactionCount(&s.rxTrans); got != 0 {
		t.Fatalf("Rx transactions after dispatch overflow = %d, want 0", got)
	}
}

func transactionCount(transactions *sync.Map) int {
	count := 0
	transactions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// Tests consolidated from session_internal_test.go.
func TestSendSessionEstablishmentRequest(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	const localSEID uint64 = 0x0102030405060708
	const remoteSEID uint64 = 0x1112131415161718
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		request, ok := received.(*message.SessionEstablishmentRequest)
		if !ok {
			return nil, fmt.Errorf("received %T, want *message.SessionEstablishmentRequest", received)
		}
		if request.NodeID == nil || request.CPFSEID == nil {
			return nil, fmt.Errorf("Session Establishment Request is missing mandatory IE(s)")
		}
		return message.NewSessionEstablishmentResponse(
			0, 0, localSEID, request.Sequence(), 0,
			ie.NewNodeIDHeuristic("127.0.0.2"),
			ie.NewCause(ie.CauseRequestAccepted),
			ie.NewFSEID(remoteSEID, net.ParseIP("127.0.0.2").To4(), nil),
		), nil
	})

	request := message.NewSessionEstablishmentRequest(
		0, 0, 0, 0, 0,
		ie.NewNodeIDHeuristic("127.0.0.1"),
		ie.NewFSEID(localSEID, net.ParseIP("127.0.0.1").To4(), nil),
	)
	response, err := s.SendSessionEstablishmentRequest(context.Background(), request, peerAddr, localSEID)
	if err != nil {
		t.Fatalf("SendSessionEstablishmentRequest() error: %v", err)
	}
	if err = <-peerDone; err != nil {
		t.Fatalf("UPF peer error: %v", err)
	}
	fields, err := response.UPFSEID.FSEID()
	if err != nil {
		t.Fatalf("decode UP F-SEID: %v", err)
	}
	if fields.SEID != remoteSEID {
		t.Fatalf("remote SEID = %d, want %d", fields.SEID, remoteSEID)
	}
}

func TestSendSessionEstablishmentRequestReturnsRejectedResponse(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	const localSEID uint64 = 41
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		return message.NewSessionEstablishmentResponse(
			0, 0, localSEID, received.Sequence(), 0,
			ie.NewNodeIDHeuristic("127.0.0.2"),
			ie.NewCause(ie.CauseNoResourcesAvailable),
		), nil
	})

	response, err := s.SendSessionEstablishmentRequest(
		context.Background(),
		message.NewSessionEstablishmentRequest(0, 0, 0, 0, 0),
		peerAddr,
		localSEID,
	)
	if err != nil {
		t.Fatalf("rejected response returned transport error: %v", err)
	}
	if err = <-peerDone; err != nil {
		t.Fatalf("UPF peer error: %v", err)
	}
	assertCause(t, response.Cause, ie.CauseNoResourcesAvailable)
}

func TestSendSessionEstablishmentRequestRejectsWrongSEID(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		return message.NewSessionEstablishmentResponse(
			0, 0, 99, received.Sequence(), 0,
			ie.NewNodeIDHeuristic("127.0.0.2"),
			ie.NewCause(ie.CauseRequestAccepted),
			ie.NewFSEID(77, net.ParseIP("127.0.0.2").To4(), nil),
		), nil
	})

	_, err := s.SendSessionEstablishmentRequest(
		context.Background(),
		message.NewSessionEstablishmentRequest(0, 0, 0, 0, 0),
		peerAddr,
		42,
	)
	if err == nil || !strings.Contains(err.Error(), "has SEID 99, want 42") {
		t.Fatalf("SendSessionEstablishmentRequest() error = %v, want SEID mismatch", err)
	}
	if peerErr := <-peerDone; peerErr != nil {
		t.Fatalf("UPF peer error: %v", peerErr)
	}
}

func TestSendSessionEstablishmentRequestRequiresUPFSEIDWhenAccepted(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	const localSEID uint64 = 42
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		return message.NewSessionEstablishmentResponse(
			0, 0, localSEID, received.Sequence(), 0,
			ie.NewNodeIDHeuristic("127.0.0.2"),
			ie.NewCause(ie.CauseRequestAccepted),
		), nil
	})

	_, err := s.SendSessionEstablishmentRequest(
		context.Background(),
		message.NewSessionEstablishmentRequest(0, 0, 0, 0, 0),
		peerAddr,
		localSEID,
	)
	if err == nil || !strings.Contains(err.Error(), "missing UP F-SEID") {
		t.Fatalf("SendSessionEstablishmentRequest() error = %v, want missing UP F-SEID", err)
	}
	if peerErr := <-peerDone; peerErr != nil {
		t.Fatalf("UPF peer error: %v", peerErr)
	}
}

func TestSendSessionDeletionRequest(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	const localSEID uint64 = 0x0102030405060708
	const remoteSEID uint64 = 0x1112131415161718
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		request, ok := received.(*message.SessionDeletionRequest)
		if !ok {
			return nil, fmt.Errorf("received %T, want *message.SessionDeletionRequest", received)
		}
		if request.SEID() != remoteSEID {
			return nil, fmt.Errorf("request SEID = %d, want %d", request.SEID(), remoteSEID)
		}
		return message.NewSessionDeletionResponse(
			0, 0, localSEID, request.Sequence(), 0,
			ie.NewCause(ie.CauseRequestAccepted),
		), nil
	})

	response, err := s.SendSessionDeletionRequest(
		context.Background(),
		message.NewSessionDeletionRequest(0, 0, remoteSEID, 0, 0),
		peerAddr,
		localSEID,
	)
	if err != nil {
		t.Fatalf("SendSessionDeletionRequest() error: %v", err)
	}
	if err = <-peerDone; err != nil {
		t.Fatalf("UPF peer error: %v", err)
	}
	assertCause(t, response.Cause, ie.CauseRequestAccepted)
}

func TestSendSessionDeletionRequestRequiresCause(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	const localSEID uint64 = 42
	peerAddr, peerDone := startAssociationPeer(t, func(received message.Message) (message.Message, error) {
		return message.NewSessionDeletionResponse(
			0, 0, localSEID, received.Sequence(), 0,
		), nil
	})

	_, err := s.SendSessionDeletionRequest(
		context.Background(),
		message.NewSessionDeletionRequest(0, 0, 77, 0, 0),
		peerAddr,
		localSEID,
	)
	if err == nil || !strings.Contains(err.Error(), "missing Cause") {
		t.Fatalf("SendSessionDeletionRequest() error = %v, want missing Cause", err)
	}
	if peerErr := <-peerDone; peerErr != nil {
		t.Fatalf("UPF peer error: %v", peerErr)
	}
}

func TestSendSessionDeletionRequestCancelsTransaction(t *testing.T) {
	s, _ := startTestPfcpServer(t)
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error: %v", err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	}()
	received := make(chan struct{}, 1)
	go func() {
		packet := make([]byte, 1500)
		if _, _, readErr := peer.ReadFromUDP(packet); readErr == nil {
			received <- struct{}{}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err = s.SendSessionDeletionRequest(
		ctx,
		message.NewSessionDeletionRequest(0, 0, 901, 0, 0),
		peer.LocalAddr().(*net.UDPAddr),
		902,
	)
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("SendSessionDeletionRequest() error = %v, want deadline exceeded", err)
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("UPF peer did not receive Session Deletion Request")
	}
	deadline := time.Now().Add(time.Second)
	for {
		transactions := 0
		s.txTrans.Range(func(_, _ any) bool {
			transactions++
			return true
		})
		if transactions == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d transaction(s) remained after context cancellation", transactions)
		}
		time.Sleep(time.Millisecond)
	}
}

// Tests consolidated from session_report_internal_test.go.
type fakeSessionReportHandler struct {
	cause      uint8
	remoteSEID uint64
	request    *message.SessionReportRequest
}

func (f *fakeSessionReportHandler) HandleSessionReportRequest(
	request *message.SessionReportRequest,
) (uint8, uint64) {
	f.request = request
	return f.cause, f.remoteSEID
}

func TestHandleSessionReportRequestUsesProcedureHandler(t *testing.T) {
	handler := &fakeSessionReportHandler{
		cause:      ie.CauseRequestAccepted,
		remoteSEID: 0x0102030405060708,
	}
	server := NewPfcpServer(nil, "127.0.0.1")
	server.SetSessionReportHandler(handler)
	request := message.NewSessionReportRequest(
		0, 0, 99, 0x123456, 0,
		ie.NewReportType(0, 0, 1, 0),
	)

	response := server.handleSessionReportRequest(request)
	if handler.request != request {
		t.Fatal("Session Report procedure did not receive the original concrete request")
	}
	assertCause(t, response.Cause, ie.CauseRequestAccepted)
	if got, want := response.SEID(), handler.remoteSEID; got != want {
		t.Fatalf("response SEID = %d, want %d", got, want)
	}
	if got, want := response.Sequence(), request.Sequence(); got != want {
		t.Fatalf("response sequence = %#x, want %#x", got, want)
	}
}

func TestHandleSessionReportRequestWithoutHandler(t *testing.T) {
	server := NewPfcpServer(nil, "127.0.0.1")
	request := message.NewSessionReportRequest(0, 0, 99, 7, 0, ie.NewReportType(0, 0, 1, 0))

	response := server.handleSessionReportRequest(request)
	assertCause(t, response.Cause, ie.CauseServiceNotSupported)
	if response.SEID() != 0 {
		t.Fatalf("response SEID = %d, want zero when no SMF procedure handled the request", response.SEID())
	}
}

// Tests consolidated from transaction_rx_internal_test.go.
func TestRxTimerPanicAbortsOnlyCurrentTransaction(t *testing.T) {
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.retransTimeout = time.Hour
	s.maxRetrans = 0
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.41"), Port: PfcpPort}

	failed := s.newRxTransaction(peer, 1)
	unrelated := s.newRxTransaction(peer, 2)
	t.Cleanup(unrelated.stop)
	generation := rxGeneration(failed)

	panicRxTimer(failed, generation)

	if _, found := s.loadRxTransaction(failed.id); found {
		t.Fatal("panicking Rx timer transaction was not removed")
	}
	failed.mu.Lock()
	failedDone, failedTimer := failed.done, failed.timer
	failed.mu.Unlock()
	if !failedDone || failedTimer != nil {
		t.Fatalf("failed transaction state: done=%t timer=%v, want done with no timer", failedDone, failedTimer)
	}
	if got, found := s.loadRxTransaction(unrelated.id); !found || got != unrelated {
		t.Fatal("unrelated Rx transaction was removed by timer panic recovery")
	}
}

func TestStaleRxTimerPanicDoesNotAbortResetGeneration(t *testing.T) {
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.retransTimeout = time.Hour
	s.maxRetrans = 0
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.42"), Port: PfcpPort}
	rx := s.newRxTransaction(peer, 1)
	t.Cleanup(rx.stop)
	staleGeneration := rxGeneration(rx)

	rx.mu.Lock()
	rx.resetTimerLocked()
	currentGeneration := rx.timerGeneration
	rx.mu.Unlock()
	if currentGeneration == staleGeneration {
		t.Fatal("reset did not advance timer generation")
	}

	panicRxTimer(rx, staleGeneration)

	if got, found := s.loadRxTransaction(rx.id); !found || got != rx {
		t.Fatal("stale timer panic removed the current Rx transaction generation")
	}
	rx.mu.Lock()
	done := rx.done
	rx.mu.Unlock()
	if done {
		t.Fatal("stale timer panic marked the current Rx transaction done")
	}
}

func TestOldRxTimerPanicDoesNotDeleteReplacementTransaction(t *testing.T) {
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.retransTimeout = time.Hour
	s.maxRetrans = 0
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.43"), Port: PfcpPort}
	old := s.newRxTransaction(peer, 1)
	generation := rxGeneration(old)

	replacement := &RxTransaction{server: s, destAddr: peer, seq: old.seq, id: old.id}
	s.rxTrans.Store(old.id, replacement)
	t.Cleanup(func() { s.rxTrans.CompareAndDelete(replacement.id, replacement) })

	panicRxTimer(old, generation)

	if got, found := s.loadRxTransaction(old.id); !found || got != replacement {
		t.Fatal("old timer panic deleted the replacement Rx transaction")
	}
}

func rxGeneration(rx *RxTransaction) uint64 {
	rx.mu.Lock()
	defer rx.mu.Unlock()
	return rx.timerGeneration
}

func panicRxTimer(rx *RxTransaction, generation uint64) {
	defer rx.recoverTimerPanic(generation)
	panic("injected Rx timer failure")
}

// Tests consolidated from transaction_tx_internal_test.go.
func TestTxTimerPanicAbortsOnlyCurrentTransaction(t *testing.T) {
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.retransTimeout = time.Hour
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.51"), Port: PfcpPort}

	response := make(chan RcvPfcpMsg, 1)
	failed, err := s.newTxTransaction(peer, response)
	if err != nil {
		t.Fatalf("newTxTransaction() error: %v", err)
	}
	startTxTimer(failed)
	unrelated, err := s.newTxTransaction(peer, nil)
	if err != nil {
		t.Fatalf("new unrelated TxTransaction: %v", err)
	}
	t.Cleanup(func() { unrelated.complete(RcvPfcpMsg{Msg: nil}) })
	generation := txGeneration(failed)

	panicTxTimer(failed, generation)

	if _, found := s.loadTxTransaction(failed.id); found {
		t.Fatal("panicking Tx timer transaction was not removed")
	}
	if txSequenceInUse(s, failed.seq) {
		t.Fatal("panicking Tx timer transaction did not release its sequence")
	}
	select {
	case notification, ok := <-response:
		if !ok {
			t.Fatal("response channel closed without a timeout notification")
		}
		if notification.Msg != nil {
			t.Fatalf("panic notification Msg = %T, want nil", notification.Msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Tx timer panic did not unblock the response waiter")
	}
	if got, found := s.loadTxTransaction(unrelated.id); !found || got != unrelated {
		t.Fatal("unrelated Tx transaction was removed by timer panic recovery")
	}
}

func TestStaleTxTimerPanicDoesNotAbortResetGeneration(t *testing.T) {
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.retransTimeout = time.Hour
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.52"), Port: PfcpPort}
	response := make(chan RcvPfcpMsg, 1)
	tx, err := s.newTxTransaction(peer, response)
	if err != nil {
		t.Fatalf("newTxTransaction() error: %v", err)
	}
	startTxTimer(tx)
	t.Cleanup(func() { tx.complete(RcvPfcpMsg{Msg: nil}) })
	staleGeneration := txGeneration(tx)

	tx.mu.Lock()
	tx.resetTimerLocked()
	currentGeneration := tx.timerGeneration
	tx.mu.Unlock()
	if currentGeneration == staleGeneration {
		t.Fatal("reset did not advance Tx timer generation")
	}

	panicTxTimer(tx, staleGeneration)

	if got, found := s.loadTxTransaction(tx.id); !found || got != tx {
		t.Fatal("stale Tx timer panic removed the current transaction generation")
	}
	tx.mu.Lock()
	done := tx.done
	tx.mu.Unlock()
	if done {
		t.Fatal("stale Tx timer panic marked the current transaction done")
	}
	select {
	case <-response:
		t.Fatal("stale Tx timer panic notified the response waiter")
	default:
	}
}

func TestOldTxTimerPanicDoesNotDeleteReplacementTransaction(t *testing.T) {
	s := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	s.retransTimeout = time.Hour
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.53"), Port: PfcpPort}
	old, err := s.newTxTransaction(peer, make(chan RcvPfcpMsg, 1))
	if err != nil {
		t.Fatalf("newTxTransaction() error: %v", err)
	}
	startTxTimer(old)
	generation := txGeneration(old)

	replacement := &TxTransaction{server: s, destAddr: peer, seq: old.seq, id: old.id}
	s.txTrans.Store(old.id, replacement)
	t.Cleanup(func() { replacement.complete(RcvPfcpMsg{Msg: nil}) })

	panicTxTimer(old, generation)

	if got, found := s.loadTxTransaction(old.id); !found || got != replacement {
		t.Fatal("old Tx timer panic deleted the replacement transaction")
	}
	if !txSequenceInUse(s, old.seq) {
		t.Fatal("old Tx timer panic freed the replacement transaction sequence")
	}
}

func startTxTimer(tx *TxTransaction) {
	tx.mu.Lock()
	tx.resetTimerLocked()
	tx.mu.Unlock()
}

func txGeneration(tx *TxTransaction) uint64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.timerGeneration
}

func txSequenceInUse(s *PfcpServer, sequence uint32) bool {
	s.seqAlloc.mu.Lock()
	defer s.seqAlloc.mu.Unlock()
	_, found := s.seqAlloc.inUse[sequence]
	return found
}

func panicTxTimer(tx *TxTransaction, generation uint64) {
	defer tx.recoverTimerPanic(generation)
	panic("injected Tx timer failure")
}

type transientReadErrorConn struct {
	packet    []byte
	peer      *net.UDPAddr
	closed    chan struct{}
	closeOnce sync.Once
	readCount atomic.Int32
}

func (c *transientReadErrorConn) ReadFromUDP(buffer []byte) (int, *net.UDPAddr, error) {
	switch c.readCount.Add(1) {
	case 1:
		return 0, nil, &net.OpError{Op: "read", Net: "udp", Err: syscall.ECONNREFUSED}
	case 2:
		return copy(buffer, c.packet), c.peer, nil
	default:
		<-c.closed
		return 0, nil, net.ErrClosed
	}
}

func (c *transientReadErrorConn) WriteToUDP(packet []byte, _ *net.UDPAddr) (int, error) {
	return len(packet), nil
}

func (c *transientReadErrorConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: PfcpPort}
}

func (c *transientReadErrorConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func TestReceiverContinuesAfterTransientUDPReadError(t *testing.T) {
	request := message.NewHeartbeatRequest(
		91,
		ie.NewRecoveryTimeStamp(time.Now()),
		nil,
	)
	packet, err := request.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.80"), Port: PfcpPort}
	connection := &transientReadErrorConn{
		packet: packet,
		peer:   peer,
		closed: make(chan struct{}),
	}
	server := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	server.conn = connection

	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go server.receiver(&waitGroup)
	t.Cleanup(func() {
		server.Stop()
		waitGroup.Wait()
	})

	select {
	case received := <-server.rcvCh:
		if received.Msg == nil || received.Msg.Sequence() != request.Sequence() {
			t.Fatalf("received message = %#v, want sequence %#x", received.Msg, request.Sequence())
		}
		if !received.RemoteAddr.IP.Equal(peer.IP) {
			t.Fatalf("received peer = %v, want %v", received.RemoteAddr.IP, peer.IP)
		}
	case <-time.After(time.Second):
		t.Fatal("receiver did not continue after a transient UDP read error")
	}

	if isServerStopped(server.stopCh) {
		t.Fatal("transient UDP read error stopped the PFCP server")
	}
	if got := connection.readCount.Load(); got < 2 {
		t.Fatalf("ReadFromUDP() calls = %d, want at least 2", got)
	}
}

type scriptedResponseConn struct {
	writes     chan error
	closed     chan struct{}
	closeOnce  sync.Once
	writeCount atomic.Int32
}

func (c *scriptedResponseConn) ReadFromUDP([]byte) (int, *net.UDPAddr, error) {
	<-c.closed
	return 0, nil, net.ErrClosed
}

func (c *scriptedResponseConn) WriteToUDP(packet []byte, _ *net.UDPAddr) (int, error) {
	if c.writeCount.Add(1) == 1 {
		err := errors.New("injected response write failure")
		c.writes <- err
		return 0, err
	}
	c.writes <- nil
	return len(packet), nil
}

func (c *scriptedResponseConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: PfcpPort}
}

func (c *scriptedResponseConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func waitForResponseWrite(t *testing.T, writes <-chan error) error {
	t.Helper()
	select {
	case err := <-writes:
		return err
	case <-time.After(time.Second):
		t.Fatal("PFCP response write did not occur")
		return nil
	}
}

func TestFailedResponseWriteDeletesRxTransaction(t *testing.T) {
	connection := &scriptedResponseConn{writes: make(chan error, 4), closed: make(chan struct{})}
	server := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	server.conn = connection
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.81"), Port: PfcpPort}
	rx := server.newRxTransaction(peer, 91)
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go server.main(&waitGroup)
	t.Cleanup(func() { server.Stop(); waitGroup.Wait() })

	err := server.SendPfcpResponse(message.NewHeartbeatResponse(
		91, ie.NewRecoveryTimeStamp(time.Now()),
	), peer)
	if err == nil || !strings.Contains(err.Error(), "injected response write failure") {
		t.Fatalf("SendPfcpResponse() error = %v, want write failure", err)
	}
	if writeErr := waitForResponseWrite(t, connection.writes); writeErr == nil {
		t.Fatal("first response write unexpectedly succeeded")
	}
	if _, found := server.loadRxTransaction(rx.id); found {
		t.Fatal("failed response retained its RxTransaction")
	}
	rx.mu.Lock()
	defer rx.mu.Unlock()
	if !rx.done || len(rx.msgBuf) != 0 || rx.timer != nil {
		t.Fatalf("failed response state: done=%t cache=%d timer=%v", rx.done, len(rx.msgBuf), rx.timer)
	}
}

func TestFailedAssociationUpdateResponseIsHandledAgainOnRetransmission(t *testing.T) {
	connection := &scriptedResponseConn{writes: make(chan error, 4), closed: make(chan struct{})}
	server := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	server.conn = connection
	manager := &fakeAssociationState{updateCause: ie.CauseRequestAccepted}
	var committed atomic.Int32
	committedCh := make(chan struct{}, 2)
	manager.after = func() {
		committed.Add(1)
		committedCh <- struct{}{}
	}
	server.SetAssociationStateManager(manager)
	var handled atomic.Int32
	server.SetDispatch(func(msg message.Message, addr *net.UDPAddr) {
		handled.Add(1)
		server.Dispatch(msg, addr)
	})
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go server.main(&waitGroup)
	go server.dispatcher(&waitGroup)
	t.Cleanup(func() { server.Stop(); waitGroup.Wait() })

	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.82"), Port: PfcpPort}
	request := message.NewAssociationUpdateRequest(92, ie.NewNodeIDHeuristic("192.0.2.82"))
	id := TransactionID(peer, request.Sequence())
	server.transactionHandler(request, peer)
	if err := waitForResponseWrite(t, connection.writes); err == nil {
		t.Fatal("first response write unexpectedly succeeded")
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, found := server.loadRxTransaction(id); !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed RxTransaction was not deleted")
		}
		time.Sleep(time.Millisecond)
	}
	if got := committed.Load(); got != 0 {
		t.Fatalf("callback count after failed response = %d, want 0", got)
	}

	server.transactionHandler(request, peer)
	if err := waitForResponseWrite(t, connection.writes); err != nil {
		t.Fatalf("retransmitted response write failed: %v", err)
	}
	select {
	case <-committedCh:
	case <-time.After(time.Second):
		t.Fatal("successful response did not commit afterResponse")
	}
	if got := handled.Load(); got != 2 {
		t.Fatalf("handler calls after retry = %d, want 2", got)
	}
	server.transactionHandler(request, peer)
	if err := waitForResponseWrite(t, connection.writes); err != nil {
		t.Fatalf("cached response replay failed: %v", err)
	}
	if got := handled.Load(); got != 2 {
		t.Fatalf("handler calls after cached replay = %d, want 2", got)
	}
	if got := committed.Load(); got != 1 {
		t.Fatalf("callback count after cached replay = %d, want 1", got)
	}
}

type associationReleaseTestClient struct {
	releaseCount atomic.Int32
	released     chan struct{}
}

func (*associationReleaseTestClient) SendAssociationSetupRequest(
	context.Context, *net.UDPAddr,
) (*message.AssociationSetupResponse, error) {
	return nil, nil
}

func (*associationReleaseTestClient) SendHeartbeatRequest(
	context.Context, *net.UDPAddr,
) (*message.HeartbeatResponse, error) {
	return nil, nil
}

func (c *associationReleaseTestClient) SendAssociationReleaseRequest(
	context.Context, *net.UDPAddr,
) (*message.AssociationReleaseResponse, error) {
	c.releaseCount.Add(1)
	c.released <- struct{}{}
	return message.NewAssociationReleaseResponse(
		1, ie.NewNodeIDHeuristic("192.0.2.85"), ie.NewCause(ie.CauseRequestAccepted),
	), nil
}

func TestAssociationUpdateFeaturesAndSARRCommitOnlyAfterSuccessfulResponse(t *testing.T) {
	connection := &scriptedResponseConn{writes: make(chan error, 4), closed: make(chan struct{})}
	server := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	server.conn = connection
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.85").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	upf.SetUPFunctionFeatures([]byte{0x01, 0x02})
	pfcpSelf := smf_context.GetSelf()
	previousContext := pfcpSelf.PfcpContext
	pfcpContext, cancelPFCP := context.WithCancel(context.Background())
	pfcpSelf.PfcpContext = pfcpContext
	t.Cleanup(func() {
		pfcpSelf.PfcpContext = previousContext
		cancelPFCP()
	})
	upf.EstablishAssociation(pfcpContext)
	t.Cleanup(upf.CancelAssociation)
	client := &associationReleaseTestClient{released: make(chan struct{}, 2)}
	manager := &processor.Processor{}
	manager.SetActivePFCPClient(client)
	server.SetAssociationStateManager(manager)
	var handled atomic.Int32
	server.SetDispatch(func(msg message.Message, addr *net.UDPAddr) {
		handled.Add(1)
		server.Dispatch(msg, addr)
	})
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go server.main(&waitGroup)
	go server.dispatcher(&waitGroup)
	t.Cleanup(func() { server.Stop(); waitGroup.Wait() })

	peer := &net.UDPAddr{IP: nodeID.IP, Port: PfcpPort}
	request := message.NewAssociationUpdateRequest(
		95,
		ie.NewNodeIDHeuristic(nodeID.IP.String()),
		ie.NewUPFunctionFeatures(0x21, 0x43),
		ie.NewPFCPAssociationReleaseRequest(1, 0),
	)
	id := TransactionID(peer, request.Sequence())
	server.transactionHandler(request, peer)
	if err := waitForResponseWrite(t, connection.writes); err == nil {
		t.Fatal("first response write unexpectedly succeeded")
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, found := server.loadRxTransaction(id); !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed response transaction was not removed")
		}
		time.Sleep(time.Millisecond)
	}
	if got := upf.UPFunctionFeatures(); len(got) != 2 || got[0] != 0x01 || got[1] != 0x02 {
		t.Fatalf("features changed after failed response: %v", got)
	}
	if err := upf.IsAssociated(); err != nil {
		t.Fatalf("SARR changed association after failed response: %v", err)
	}
	if got := client.releaseCount.Load(); got != 0 {
		t.Fatalf("Association Release requests after failed response = %d, want 0", got)
	}

	server.transactionHandler(request, peer)
	if err := waitForResponseWrite(t, connection.writes); err != nil {
		t.Fatalf("retried response write failed: %v", err)
	}
	select {
	case <-client.released:
	case <-time.After(time.Second):
		t.Fatal("successful response did not begin association release")
	}
	if got := upf.UPFunctionFeatures(); len(got) != 2 || got[0] != 0x21 || got[1] != 0x43 {
		t.Fatalf("features after successful response = %v, want [0x21 0x43]", got)
	}
	server.transactionHandler(request, peer)
	if err := waitForResponseWrite(t, connection.writes); err != nil {
		t.Fatalf("cached response replay failed: %v", err)
	}
	if got := handled.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
	if got := client.releaseCount.Load(); got != 1 {
		t.Fatalf("Association Release requests = %d, want 1", got)
	}
}

type blockedResponseConn struct {
	started   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *blockedResponseConn) ReadFromUDP([]byte) (int, *net.UDPAddr, error) {
	<-c.closed
	return 0, nil, net.ErrClosed
}

func (c *blockedResponseConn) WriteToUDP(packet []byte, _ *net.UDPAddr) (int, error) {
	close(c.started)
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	case <-c.release:
		return len(packet), nil
	}
}

func (c *blockedResponseConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: PfcpPort}
}

func (c *blockedResponseConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func TestResponseWriteRacesWithTimerAndOldTransactionCannotDeleteReplacement(t *testing.T) {
	connection := &blockedResponseConn{
		started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}),
	}
	server := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	server.conn = connection
	t.Cleanup(server.Stop)
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.83"), Port: PfcpPort}
	rx := server.newRxTransaction(peer, 93)
	rx.mu.Lock()
	staleGeneration := rx.timerGeneration
	rx.mu.Unlock()
	sendDone := make(chan error, 1)
	go func() {
		sendDone <- rx.send(message.NewHeartbeatResponse(
			93, ie.NewRecoveryTimeStamp(time.Now()),
		))
	}()
	select {
	case <-connection.started:
	case <-time.After(time.Second):
		t.Fatal("response write did not start")
	}
	expireDone := make(chan struct{})
	go func() { rx.expire(staleGeneration); close(expireDone) }()
	close(connection.release)
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("response write failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("response write deadlocked with timer")
	}
	select {
	case <-expireDone:
	case <-time.After(time.Second):
		t.Fatal("timer callback did not finish")
	}
	if current, found := server.loadRxTransaction(rx.id); !found || current != rx {
		t.Fatal("stale timer deleted a successfully cached response")
	}
	replacement := server.newRxTransaction(peer, 93)
	defer replacement.stop()
	rx.stop()
	if current, found := server.loadRxTransaction(rx.id); !found || current != replacement {
		t.Fatal("old transaction deleted its replacement")
	}
}

func TestResponseWriteUnblocksOnServerShutdown(t *testing.T) {
	connection := &blockedResponseConn{
		started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}),
	}
	server := NewPfcpServer(&fakeSmf{cfg: &factory.Config{}}, "127.0.0.1")
	server.conn = connection
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.84"), Port: PfcpPort}
	server.newRxTransaction(peer, 94)
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go server.main(&waitGroup)
	t.Cleanup(func() { server.Stop(); waitGroup.Wait() })
	sendDone := make(chan error, 1)
	go func() {
		sendDone <- server.SendPfcpResponse(message.NewHeartbeatResponse(
			94, ie.NewRecoveryTimeStamp(time.Now()),
		), peer)
	}()
	select {
	case <-connection.started:
	case <-time.After(time.Second):
		t.Fatal("response write did not start")
	}
	server.Stop()
	select {
	case err := <-sendDone:
		if err == nil {
			t.Fatal("response write succeeded after server shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("SendPfcpResponse deadlocked during shutdown")
	}
	finished := make(chan struct{})
	go func() { waitGroup.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("PFCP main loop did not stop after failed write")
	}
}

func TestUnsupportedNodeRequestsReturnCachedRejection(t *testing.T) {
	server := startDispatcherRecoveryServer(t, 1)
	var handled atomic.Int32
	server.SetDispatch(func(msg message.Message, addr *net.UDPAddr) {
		handled.Add(1)
		server.Dispatch(msg, addr)
	})

	peer, err := net.DialUDP("udp", nil, server.LocalAddr())
	if err != nil {
		t.Fatalf("DialUDP() error: %v", err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Errorf("Close() error: %v", closeErr)
		}
	}()

	tests := []struct {
		name     string
		request  message.Message
		response func(message.Message) *ie.IE
	}{
		{
			name: "NodeReport",
			request: message.NewNodeReportRequest(
				81, ie.NewNodeIDHeuristic("192.0.2.91"), ie.NewNodeReportType(0x01),
			),
			response: func(msg message.Message) *ie.IE {
				rsp, ok := msg.(*message.NodeReportResponse)
				if !ok {
					t.Fatalf("response type = %T, want *message.NodeReportResponse", msg)
				}
				return rsp.Cause
			},
		},
		{
			name: "SessionSetDeletion",
			request: message.NewSessionSetDeletionRequest(
				82, ie.NewNodeIDHeuristic("192.0.2.91"), nil,
			),
			response: func(msg message.Message) *ie.IE {
				rsp, ok := msg.(*message.SessionSetDeletionResponse)
				if !ok {
					t.Fatalf("response type = %T, want *message.SessionSetDeletionResponse", msg)
				}
				return rsp.Cause
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packet := make([]byte, test.request.MarshalLen())
			if marshalErr := test.request.MarshalTo(packet); marshalErr != nil {
				t.Fatalf("MarshalTo() error: %v", marshalErr)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if _, writeErr := peer.Write(packet); writeErr != nil {
					t.Fatalf("Write() error: %v", writeErr)
				}
				if deadlineErr := peer.SetReadDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
					t.Fatalf("SetReadDeadline() error: %v", deadlineErr)
				}
				responsePacket := make([]byte, 2048)
				n, readErr := peer.Read(responsePacket)
				if readErr != nil {
					t.Fatalf("Read() error: %v", readErr)
				}
				response, parseErr := message.Parse(responsePacket[:n])
				if parseErr != nil {
					t.Fatalf("Parse() error: %v", parseErr)
				}
				if got, want := response.Sequence(), test.request.Sequence(); got != want {
					t.Fatalf("response sequence = %#x, want %#x", got, want)
				}
				assertCause(t, test.response(response), ie.CauseServiceNotSupported)
			}
			if got, want := handled.Load(), int32(index+1); got != want {
				t.Fatalf("dispatch count after retransmission = %d, want %d", got, want)
			}
		})
	}
}
