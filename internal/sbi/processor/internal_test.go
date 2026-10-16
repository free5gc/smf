package processor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

// Tests consolidated from association_active_internal_test.go.
type fakeActivePFCPClient struct {
	setup        func(*net.UDPAddr) (*message.AssociationSetupResponse, error)
	setupCtx     func(context.Context, *net.UDPAddr) (*message.AssociationSetupResponse, error)
	heartbeat    func(*net.UDPAddr) (*message.HeartbeatResponse, error)
	heartbeatCtx func(context.Context, *net.UDPAddr) (*message.HeartbeatResponse, error)
	release      func(*net.UDPAddr) (*message.AssociationReleaseResponse, error)
	releaseCtx   func(context.Context, *net.UDPAddr) (*message.AssociationReleaseResponse, error)
	deleteCtx    func(
		context.Context, *message.SessionDeletionRequest, *net.UDPAddr, uint64,
	) (*message.SessionDeletionResponse, error)
}

func (f *fakeActivePFCPClient) SendAssociationSetupRequest(
	ctx context.Context,
	addr *net.UDPAddr,
) (*message.AssociationSetupResponse, error) {
	if f.setupCtx != nil {
		return f.setupCtx(ctx, addr)
	}
	return f.setup(addr)
}

func (f *fakeActivePFCPClient) SendHeartbeatRequest(
	ctx context.Context,
	addr *net.UDPAddr,
) (*message.HeartbeatResponse, error) {
	if f.heartbeatCtx != nil {
		return f.heartbeatCtx(ctx, addr)
	}
	return f.heartbeat(addr)
}

func (f *fakeActivePFCPClient) SendAssociationReleaseRequest(
	ctx context.Context,
	addr *net.UDPAddr,
) (*message.AssociationReleaseResponse, error) {
	if f.releaseCtx != nil {
		return f.releaseCtx(ctx, addr)
	}
	return f.release(addr)
}

func (f *fakeActivePFCPClient) SendSessionDeletionRequest(
	ctx context.Context,
	request *message.SessionDeletionRequest,
	addr *net.UDPAddr,
	localSEID uint64,
) (*message.SessionDeletionResponse, error) {
	return f.deleteCtx(ctx, request, addr, localSEID)
}

func newActiveAssociationTestUPF(t *testing.T) *smf_context.UPF {
	t.Helper()
	upf := &smf_context.UPF{
		NodeID: pfcptype.NodeID{
			NodeIdType: pfcptype.NodeIdTypeIpv4Address,
			IP:         net.ParseIP("192.0.2.10").To4(),
		},
	}
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)
	return upf
}

func TestEnsureSetupReturnsAssociationContext(t *testing.T) {
	upf := &smf_context.UPF{
		NodeID: pfcptype.NodeID{
			NodeIdType: pfcptype.NodeIdTypeIpv4Address,
			IP:         net.ParseIP("192.0.2.11").To4(),
		},
	}
	parentContext, cancelParent := context.WithCancel(context.Background())
	t.Cleanup(cancelParent)
	recoveryTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		setupCtx: func(ctx context.Context, _ *net.UDPAddr) (*message.AssociationSetupResponse, error) {
			if ctx != parentContext {
				t.Errorf("Association Setup context does not match PFCP parent context")
			}
			return message.NewAssociationSetupResponse(
				1,
				ie.NewNodeIDHeuristic("192.0.2.11"),
				ie.NewCause(ie.CauseRequestAccepted),
				ie.NewRecoveryTimeStamp(recoveryTime),
			), nil
		},
	})

	associationContext, established := p.ensureSetupPfcpAssociation(
		parentContext, upf, "[192.0.2.11]",
	)
	if !established || associationContext == nil {
		t.Fatal("Association Setup did not return an association context")
	}
	if associationContext == parentContext {
		t.Fatal("association context is the PFCP parent instead of a child context")
	}
	select {
	case <-associationContext.Done():
		t.Fatal("new association context is already canceled")
	default:
	}

	upf.CancelAssociation()
	select {
	case <-associationContext.Done():
	case <-time.After(time.Second):
		t.Fatal("CancelAssociation did not cancel the returned association context")
	}
}

func TestActiveAssociationLoopUsesAssociationContextForHeartbeat(t *testing.T) {
	upf := &smf_context.UPF{
		NodeID: pfcptype.NodeID{
			NodeIdType: pfcptype.NodeIdTypeIpv4Address,
			IP:         net.ParseIP("192.0.2.12").To4(),
		},
	}
	parentContext, cancelParent := context.WithCancel(context.Background())
	t.Cleanup(cancelParent)

	smfSelf := smf_context.GetSelf()
	previousInterval := smfSelf.PfcpHeartbeatInterval
	smfSelf.PfcpHeartbeatInterval = time.Hour
	t.Cleanup(func() { smfSelf.PfcpHeartbeatInterval = previousInterval })

	recoveryTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	heartbeatContexts := make(chan context.Context, 1)
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		setup: func(*net.UDPAddr) (*message.AssociationSetupResponse, error) {
			return message.NewAssociationSetupResponse(
				1,
				ie.NewNodeIDHeuristic("192.0.2.12"),
				ie.NewCause(ie.CauseRequestAccepted),
				ie.NewRecoveryTimeStamp(recoveryTime),
			), nil
		},
		heartbeatCtx: func(ctx context.Context, _ *net.UDPAddr) (*message.HeartbeatResponse, error) {
			heartbeatContexts <- ctx
			return message.NewHeartbeatResponse(2, ie.NewRecoveryTimeStamp(recoveryTime)), nil
		},
	})

	loopDone := make(chan struct{})
	go func() {
		p.ToBeAssociatedWithUPF(parentContext, upf)
		close(loopDone)
	}()

	var heartbeatContext context.Context
	select {
	case heartbeatContext = <-heartbeatContexts:
	case <-time.After(time.Second):
		t.Fatal("association loop did not send Heartbeat")
	}
	if heartbeatContext == parentContext {
		t.Fatal("Heartbeat used the PFCP parent instead of the association context")
	}
	cancelParent()
	select {
	case <-heartbeatContext.Done():
	case <-time.After(time.Second):
		t.Fatal("PFCP parent cancellation did not cancel Heartbeat association context")
	}
	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("association loop did not stop after PFCP parent cancellation")
	}
}

func waitForAssociationEstablished(t *testing.T, upf *smf_context.UPF) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		if upf.AssociationState() == smf_context.AssociationEstablished {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("UPF association did not reach Established state")
		}
	}
}

func TestAssociationLifecycleWithoutHeartbeatWaitsCleansAndReconnects(t *testing.T) {
	initAssociationReleaseTestContext(t)
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.20").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })

	smContext := smf_context.NewSMContext("imsi-heartbeat-disabled", 1)
	t.Cleanup(func() { smf_context.RemoveSMContext(smContext.Ref) })
	smContext.PFCPContext[nodeID.String()] = &smf_context.PFCPSessionContext{
		NodeID: nodeID, LocalSEID: 1001, RemoteSEID: 2001,
	}

	smfSelf := smf_context.GetSelf()
	previousHeartbeatInterval := smfSelf.PfcpHeartbeatInterval
	smfSelf.PfcpHeartbeatInterval = 0
	t.Cleanup(func() { smfSelf.PfcpHeartbeatInterval = previousHeartbeatInterval })

	recoveryTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	setupCalls := make(chan int, 3)
	heartbeatCalls := make(chan struct{}, 1)
	var setupCount atomic.Int32
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		setup: func(*net.UDPAddr) (*message.AssociationSetupResponse, error) {
			call := int(setupCount.Add(1))
			if call == 2 {
				smContext.SMLock.Lock()
				remoteSEID := smContext.PFCPContext[nodeID.String()].RemoteSEID
				smContext.SMLock.Unlock()
				if remoteSEID != 0 {
					return nil, fmt.Errorf("second setup started with stale RemoteSEID %d", remoteSEID)
				}
			}
			setupCalls <- call
			return message.NewAssociationSetupResponse(
				uint32(call),
				ie.NewNodeIDHeuristic(nodeID.String()),
				ie.NewCause(ie.CauseRequestAccepted),
				ie.NewRecoveryTimeStamp(recoveryTime),
			), nil
		},
		heartbeat: func(*net.UDPAddr) (*message.HeartbeatResponse, error) {
			heartbeatCalls <- struct{}{}
			return nil, errors.New("heartbeat must be disabled")
		},
	})

	parentContext, cancelParent := context.WithCancel(context.Background())
	t.Cleanup(cancelParent)
	loopDone := make(chan struct{})
	go func() {
		p.ToBeAssociatedWithUPF(parentContext, upf)
		close(loopDone)
	}()

	select {
	case call := <-setupCalls:
		if call != 1 {
			t.Fatalf("first setup call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("initial Association Setup was not sent")
	}
	waitForAssociationEstablished(t, upf)
	select {
	case <-heartbeatCalls:
		t.Fatal("Heartbeat was sent while PfcpHeartbeatInterval is zero")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-loopDone:
		t.Fatal("association lifecycle exited while Heartbeat was disabled")
	default:
	}

	if cause := p.ReleaseAssociation(nodeID); cause != ie.CauseRequestAccepted {
		t.Fatalf("ReleaseAssociation() cause = %d", cause)
	}
	select {
	case call := <-setupCalls:
		if call != 2 {
			t.Fatalf("reconnect setup call = %d, want 2", call)
		}
	case <-time.After(time.Second):
		t.Fatal("Association Release did not trigger reconnection")
	}

	cancelParent()
	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("association lifecycle did not stop during SMF shutdown")
	}
	select {
	case call := <-setupCalls:
		t.Fatalf("Association Setup call %d occurred after SMF shutdown", call)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestPassiveAssociationSetupRestartWakesHeartbeatDisabledLifecycleOnce(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.21").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })

	smfSelf := smf_context.GetSelf()
	previousHeartbeatInterval := smfSelf.PfcpHeartbeatInterval
	smfSelf.PfcpHeartbeatInterval = 0
	t.Cleanup(func() { smfSelf.PfcpHeartbeatInterval = previousHeartbeatInterval })

	oldRecoveryTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	newRecoveryTime := oldRecoveryTime.Add(time.Minute)
	setupCalls := make(chan int, 3)
	var setupCount atomic.Int32
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		setup: func(*net.UDPAddr) (*message.AssociationSetupResponse, error) {
			call := int(setupCount.Add(1))
			setupCalls <- call
			recoveryTime := oldRecoveryTime
			if call > 1 {
				recoveryTime = newRecoveryTime
			}
			return message.NewAssociationSetupResponse(
				uint32(call), ie.NewNodeIDHeuristic(nodeID.String()),
				ie.NewCause(ie.CauseRequestAccepted), ie.NewRecoveryTimeStamp(recoveryTime),
			), nil
		},
		heartbeat: func(*net.UDPAddr) (*message.HeartbeatResponse, error) {
			return nil, errors.New("unexpected Heartbeat")
		},
	})

	parentContext, cancelParent := context.WithCancel(context.Background())
	t.Cleanup(cancelParent)
	loopDone := make(chan struct{})
	go func() {
		p.ToBeAssociatedWithUPF(parentContext, upf)
		close(loopDone)
	}()
	select {
	case <-setupCalls:
	case <-time.After(time.Second):
		t.Fatal("initial Association Setup was not sent")
	}
	waitForAssociationEstablished(t, upf)

	cause, firstCallback := p.SetupAssociation(nodeID, newRecoveryTime)
	if cause != ie.CauseRequestAccepted || firstCallback == nil {
		t.Fatalf("first passive Setup cause=%d callback=%t", cause, firstCallback != nil)
	}
	_, duplicateCallback := p.SetupAssociation(nodeID, newRecoveryTime)
	if duplicateCallback == nil {
		t.Fatal("duplicate passive Setup did not return a callback")
	}
	if err := upf.IsAssociated(); err != nil {
		t.Fatalf("association changed before Setup Response: %v", err)
	}
	firstCallback()
	duplicateCallback()

	select {
	case call := <-setupCalls:
		if call != 2 {
			t.Fatalf("restart setup call = %d, want 2", call)
		}
	case <-time.After(time.Second):
		t.Fatal("new passive Recovery Time Stamp did not trigger reconnection")
	}
	select {
	case call := <-setupCalls:
		t.Fatalf("duplicate restart event triggered setup call %d", call)
	case <-time.After(20 * time.Millisecond):
	}

	cancelParent()
	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("association lifecycle did not stop")
	}
}

func TestPassiveAssociationSetupRecoveryTimeSemantics(t *testing.T) {
	tests := []struct {
		name       string
		baseline   time.Time
		incoming   time.Time
		wantCancel bool
	}{
		{name: "first baseline", incoming: time.Unix(100, 0)},
		{name: "unchanged", baseline: time.Unix(100, 0), incoming: time.Unix(100, 0)},
		{name: "newer means restart", baseline: time.Unix(100, 0), incoming: time.Unix(101, 0), wantCancel: true},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nodeID := pfcptype.NodeID{
				NodeIdType: pfcptype.NodeIdTypeIpv4Address,
				IP:         net.IPv4(192, 0, 2, byte(30+index)).To4(),
			}
			upf := smf_context.NewUPF(&nodeID, nil)
			t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
			upf.EstablishAssociation(context.Background())
			if !test.baseline.IsZero() {
				upf.SetRecoveryTimeStamp(test.baseline)
			}

			cause, afterResponse := (&Processor{}).SetupAssociation(nodeID, test.incoming)
			if cause != ie.CauseRequestAccepted || afterResponse == nil {
				t.Fatalf("SetupAssociation() cause=%d callback=%t", cause, afterResponse != nil)
			}
			if test.baseline.IsZero() && !upf.RecoveryTimeStamp().IsZero() {
				t.Fatal("first baseline was committed before response")
			}
			afterResponse()
			if test.wantCancel {
				if err := upf.IsAssociated(); err == nil {
					t.Fatal("newer Recovery Time Stamp did not invalidate association")
				}
				return
			}
			if err := upf.IsAssociated(); err != nil {
				t.Fatalf("Recovery Time Stamp incorrectly invalidated association: %v", err)
			}
			if got := upf.RecoveryTimeStamp(); !got.Equal(test.incoming) {
				t.Fatalf("RecoveryTimeStamp() = %v, want %v", got, test.incoming)
			}
		})
	}
}

func TestDelayedPassiveSetupCallbackDoesNotCancelReplacementAssociation(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.40").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	oldRecoveryTime := time.Unix(100, 0)
	newRecoveryTime := time.Unix(200, 0)
	upf.EstablishAssociation(context.Background())
	upf.SetRecoveryTimeStamp(oldRecoveryTime)

	_, delayedCallback := (&Processor{}).SetupAssociation(nodeID, newRecoveryTime)
	upf.CancelAssociation()
	upf.EstablishAssociation(context.Background())
	upf.SetRecoveryTimeStamp(newRecoveryTime)
	delayedCallback()

	if err := upf.IsAssociated(); err != nil {
		t.Fatalf("delayed callback canceled replacement association: %v", err)
	}
	if got := upf.RecoveryTimeStamp(); !got.Equal(newRecoveryTime) {
		t.Fatalf("replacement RecoveryTimeStamp() = %v, want %v", got, newRecoveryTime)
	}
}

func TestAssociationLifecycleHasSingleOwner(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.41").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	parentContext, cancelParent := context.WithCancel(context.Background())

	setupStarted := make(chan struct{}, 1)
	var setupCount atomic.Int32
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		setupCtx: func(ctx context.Context, _ *net.UDPAddr) (*message.AssociationSetupResponse, error) {
			setupCount.Add(1)
			setupStarted <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	ownersDone := make(chan struct{}, 2)
	go func() {
		p.ToBeAssociatedWithUPF(parentContext, upf)
		ownersDone <- struct{}{}
	}()
	select {
	case <-setupStarted:
	case <-time.After(time.Second):
		t.Fatal("lifecycle owner did not start setup")
	}
	go func() {
		p.ToBeAssociatedWithUPF(parentContext, upf)
		ownersDone <- struct{}{}
	}()
	select {
	case <-ownersDone:
	case <-time.After(time.Second):
		t.Fatal("second lifecycle caller did not return")
	}
	if got := setupCount.Load(); got != 1 {
		t.Fatalf("concurrent lifecycle setup calls = %d, want 1", got)
	}

	cancelParent()
	select {
	case <-ownersDone:
	case <-time.After(time.Second):
		t.Fatal("lifecycle owner did not stop")
	}
}

func TestActiveAssociationDoesNotRetryAfterParentCancellation(t *testing.T) {
	upf := newActiveAssociationTestUPF(t)
	parentContext, cancel := context.WithCancel(context.Background())
	cancel()
	setupCalled := false
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		setup: func(*net.UDPAddr) (*message.AssociationSetupResponse, error) {
			setupCalled = true
			return nil, errors.New("unexpected setup")
		},
	})

	if associationContext, established := p.ensureSetupPfcpAssociation(
		parentContext, upf, "[192.0.2.10]",
	); established || associationContext != nil {
		t.Fatal("ensureSetupPfcpAssociation() succeeded after parent cancellation")
	}
	if setupCalled {
		t.Fatal("Association Setup was sent after parent cancellation")
	}
}

func TestActiveAssociationRetryWaitStopsOnParentCancellation(t *testing.T) {
	upf := newActiveAssociationTestUPF(t)
	upf.CancelAssociation()
	parentContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	smfSelf := smf_context.GetSelf()
	previousRetryInterval := smfSelf.AssocFailRetryInterval
	smfSelf.AssocFailRetryInterval = time.Hour
	t.Cleanup(func() { smfSelf.AssocFailRetryInterval = previousRetryInterval })

	setupCalled := make(chan struct{}, 1)
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		setupCtx: func(context.Context, *net.UDPAddr) (*message.AssociationSetupResponse, error) {
			setupCalled <- struct{}{}
			return nil, errors.New("UPF unavailable")
		},
	})

	type setupResult struct {
		associationContext context.Context
		established        bool
	}
	resultChannel := make(chan setupResult, 1)
	go func() {
		associationContext, established := p.ensureSetupPfcpAssociation(
			parentContext, upf, "[192.0.2.10]",
		)
		resultChannel <- setupResult{associationContext, established}
	}()

	select {
	case <-setupCalled:
	case <-time.After(time.Second):
		t.Fatal("Association Setup was not attempted")
	}
	cancel()
	select {
	case result := <-resultChannel:
		if result.established || result.associationContext != nil {
			t.Fatal("Association Setup succeeded after parent cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("retry wait did not stop after parent cancellation")
	}
	if got := upf.AssociationState(); got != smf_context.AssociationDown {
		t.Fatalf("association state after canceled retry = %s, want down", got)
	}
}

func TestActiveAssociationSetupStoresUPFRecoveryTime(t *testing.T) {
	upf := newActiveAssociationTestUPF(t)
	recoveryTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		setup: func(addr *net.UDPAddr) (*message.AssociationSetupResponse, error) {
			if got, want := addr.IP.String(), "192.0.2.10"; got != want {
				t.Fatalf("destination IP = %s, want %s", got, want)
			}
			if got, want := addr.Port, 8805; got != want {
				t.Fatalf("destination port = %d, want %d", got, want)
			}
			return message.NewAssociationSetupResponse(
				1,
				ie.NewNodeIDHeuristic("192.0.2.10"),
				ie.NewCause(ie.CauseRequestAccepted),
				ie.NewRecoveryTimeStamp(recoveryTime),
			), nil
		},
	})

	if err := p.setupPfcpAssociation(context.Background(), upf, "[192.0.2.10]"); err != nil {
		t.Fatalf("setupPfcpAssociation() error: %v", err)
	}
	if got, want := upf.RecoveryTimeStamp().Unix(), recoveryTime.Unix(); got != want {
		t.Fatalf("UPF RecoveryTimeStamp = %d, want %d", got, want)
	}
}

func TestActiveHeartbeatKeepsAssociationForSameRecoveryTime(t *testing.T) {
	upf := newActiveAssociationTestUPF(t)
	recoveryTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	upf.SetRecoveryTimeStamp(recoveryTime)
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		heartbeat: func(*net.UDPAddr) (*message.HeartbeatResponse, error) {
			return message.NewHeartbeatResponse(
				2,
				ie.NewRecoveryTimeStamp(recoveryTime),
			), nil
		},
	})

	if err := p.doPfcpHeartbeat(context.Background(), upf, "[192.0.2.10]"); err != nil {
		t.Fatalf("doPfcpHeartbeat() error: %v", err)
	}
	if err := upf.IsAssociated(); err != nil {
		t.Fatalf("association was canceled: %v", err)
	}
}

func TestActiveHeartbeatFailureCancelsAssociation(t *testing.T) {
	upf := newActiveAssociationTestUPF(t)
	upf.SetRecoveryTimeStamp(time.Now().Add(-time.Hour).Truncate(time.Second))
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		heartbeat: func(*net.UDPAddr) (*message.HeartbeatResponse, error) {
			return nil, errors.New("timeout")
		},
	})

	err := p.doPfcpHeartbeat(context.Background(), upf, "[192.0.2.10]")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("doPfcpHeartbeat() error = %v, want timeout", err)
	}
	if err = upf.IsAssociated(); err == nil {
		t.Fatal("UPF remains associated after heartbeat failure")
	}
	if !upf.RecoveryTimeStamp().IsZero() {
		t.Fatalf("UPF RecoveryTimeStamp = %v, want zero", upf.RecoveryTimeStamp())
	}
}

func TestActiveHeartbeatDetectsUPFRestart(t *testing.T) {
	upf := newActiveAssociationTestUPF(t)
	oldRecoveryTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	upf.SetRecoveryTimeStamp(oldRecoveryTime)
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		heartbeat: func(*net.UDPAddr) (*message.HeartbeatResponse, error) {
			return message.NewHeartbeatResponse(
				3,
				ie.NewRecoveryTimeStamp(oldRecoveryTime.Add(time.Minute)),
			), nil
		},
	})

	err := p.doPfcpHeartbeat(context.Background(), upf, "[192.0.2.10]")
	if err == nil || !strings.Contains(err.Error(), "updated") {
		t.Fatalf("doPfcpHeartbeat() error = %v, want updated Recovery Time Stamp", err)
	}
	if err = upf.IsAssociated(); err == nil {
		t.Fatal("UPF remains associated after Recovery Time Stamp changed")
	}
	if !upf.RecoveryTimeStamp().IsZero() {
		t.Fatalf("UPF RecoveryTimeStamp = %v, want zero", upf.RecoveryTimeStamp())
	}
}

func TestActiveHeartbeatUsesFirstRecoveryTimeAsBaseline(t *testing.T) {
	upf := newActiveAssociationTestUPF(t)
	recoveryTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		heartbeat: func(*net.UDPAddr) (*message.HeartbeatResponse, error) {
			return message.NewHeartbeatResponse(
				4,
				ie.NewRecoveryTimeStamp(recoveryTime),
			), nil
		},
	})

	if err := p.doPfcpHeartbeat(context.Background(), upf, "[192.0.2.10]"); err != nil {
		t.Fatalf("doPfcpHeartbeat() error: %v", err)
	}
	if got, want := upf.RecoveryTimeStamp().Unix(), recoveryTime.Unix(); got != want {
		t.Fatalf("UPF RecoveryTimeStamp = %d, want %d", got, want)
	}
}

// Tests consolidated from association_state_internal_test.go.
func installAssociationReleasePFCPContext(t *testing.T) context.Context {
	t.Helper()
	smfSelf := smf_context.GetSelf()
	previousContext := smfSelf.PfcpContext
	previousCancel := smfSelf.PfcpCancelFunc
	pfcpContext, cancelPFCP := context.WithCancel(context.Background())
	smfSelf.PfcpContext = pfcpContext
	smfSelf.PfcpCancelFunc = cancelPFCP
	t.Cleanup(func() {
		cancelPFCP()
		smfSelf.PfcpContext = previousContext
		smfSelf.PfcpCancelFunc = previousCancel
	})
	return pfcpContext
}

func TestAssociationReleaseContextFollowsPFCPParent(t *testing.T) {
	parentContext, cancelParent := context.WithCancel(context.Background())
	releaseContext, cancelRelease := associationReleaseContext(parentContext, nil)
	t.Cleanup(cancelRelease)

	cancelParent()
	select {
	case <-releaseContext.Done():
	case <-time.After(time.Second):
		t.Fatal("PFCP parent cancellation did not cancel Association Release context")
	}
}

func initAssociationReleaseTestContext(t *testing.T) {
	t.Helper()
	err := smf_context.InitSmfContext(&factory.Config{
		Info: &factory.Info{Version: "1.0.7"},
		Configuration: &factory.Configuration{
			Sbi: &factory.Sbi{Scheme: "http", BindingIPv4: "127.0.0.1"},
			UserPlaneInformation: factory.UserPlaneInformation{
				UPNodes: map[string]*factory.UPNode{},
				Links:   []*factory.UPLink{},
			},
		},
	})
	if err != nil {
		t.Fatalf("InitSmfContext() error: %v", err)
	}
	installAssociationReleasePFCPContext(t)
}

func TestProcessorPassiveAssociationStateUsesConfiguredUPF(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.81").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	peer := nodeID
	p := &Processor{}

	recoveryTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	cause, afterResponse := p.SetupAssociation(peer, recoveryTime)
	if cause != ie.CauseRequestAccepted {
		t.Fatalf("SetupAssociation() cause = %d, want Request Accepted", cause)
	}
	if afterResponse == nil {
		t.Fatal("SetupAssociation() did not defer recovery-time commit")
	}
	if !upf.RecoveryTimeStamp().IsZero() {
		t.Fatal("SetupAssociation() committed recovery time before response")
	}
	afterResponse()
	if got := upf.RecoveryTimeStamp(); !got.Equal(recoveryTime) {
		t.Fatalf("RecoveryTimeStamp() = %v, want %v", got, recoveryTime)
	}

	upf.EstablishAssociation(context.Background())
	if cause = p.ReleaseAssociation(peer); cause != ie.CauseRequestAccepted {
		t.Fatalf("ReleaseAssociation() cause = %d, want Request Accepted", cause)
	}
	if err := upf.IsAssociated(); err == nil {
		t.Fatal("UPF remains associated after passive Association Release")
	}
	if !upf.RecoveryTimeStamp().IsZero() {
		t.Fatalf("RecoveryTimeStamp = %v, want zero", upf.RecoveryTimeStamp())
	}
}

func TestProcessorPassiveAssociationUpdateStoresFeatures(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.83").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)
	p := &Processor{}
	featureIE := ie.NewUPFunctionFeatures(0x21, 0x43)

	cause, afterResponse := p.UpdateAssociation(nodeID,
		message.NewAssociationUpdateRequest(1, featureIE))
	if cause != ie.CauseRequestAccepted {
		t.Fatalf("UpdateAssociation() cause = %d, want Request Accepted", cause)
	}
	if afterResponse == nil {
		t.Fatal("feature-only update did not prepare deferred feature commit")
	}
	featureIE.Payload[0] = 0xff
	if got := upf.UPFunctionFeatures(); len(got) != 0 {
		t.Fatalf("features changed before response: %v", got)
	}
	afterResponse()
	got := upf.UPFunctionFeatures()
	if len(got) != 2 || got[0] != 0x21 || got[1] != 0x43 {
		t.Fatalf("stored UP Function Features = %v, want [0x21 0x43]", got)
	}
}

func TestProcessorPassiveAssociationUpdateRejectsInvalidRequestWithoutChangingFeatures(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.91").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)
	upf.SetUPFunctionFeatures([]byte{0x01, 0x02})

	// A malformed later IE must not commit the already parsed Features IE.
	invalidPeriod := ie.NewGracefulReleasePeriod(time.Second)
	invalidPeriod.Payload = nil
	processor := &Processor{}
	cause, afterResponse := processor.UpdateAssociation(nodeID, message.NewAssociationUpdateRequest(
		1,
		ie.NewUPFunctionFeatures(0x21, 0x43),
		ie.NewPFCPAssociationReleaseRequest(1, 0),
		invalidPeriod,
	))
	if cause != ie.CauseInvalidLength || afterResponse != nil {
		t.Fatalf("UpdateAssociation() cause=%d callback=%t, want invalid length without callback",
			cause, afterResponse != nil)
	}
	if got := upf.UPFunctionFeatures(); len(got) != 2 || got[0] != 0x01 || got[1] != 0x02 {
		t.Fatalf("features changed after rejected update: %v", got)
	}
}

func TestProcessorPassiveAssociationUpdateURSSDoesNotRelease(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.90").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)
	p := &Processor{}

	cause, afterResponse := p.UpdateAssociation(nodeID, message.NewAssociationUpdateRequest(
		1, ie.NewUPFunctionFeatures(0x11, 0x22), ie.NewPFCPAssociationReleaseRequest(0, 1),
	))
	if cause != ie.CauseRequestAccepted || afterResponse == nil {
		t.Fatalf("UpdateAssociation() cause=%d callback=%t, want deferred feature commit",
			cause, afterResponse != nil)
	}
	if got := upf.UPFunctionFeatures(); len(got) != 0 {
		t.Fatalf("URSS update changed features before response: %v", got)
	}
	afterResponse()
	if got := upf.UPFunctionFeatures(); len(got) != 2 || got[0] != 0x11 || got[1] != 0x22 {
		t.Fatalf("URSS update did not commit features: %v", got)
	}
	if err := upf.IsAssociated(); err != nil {
		t.Fatalf("URSS-only Association Update changed association state: %v", err)
	}
	cause, afterResponse = p.UpdateAssociation(nodeID, message.NewAssociationUpdateRequest(
		2, ie.NewPFCPAssociationReleaseRequest(0, 1),
	))
	if cause != ie.CauseRequestAccepted || afterResponse != nil {
		t.Fatalf("URSS-only update cause=%d callback=%t, want accepted without callback",
			cause, afterResponse != nil)
	}
}

func TestProcessorPassiveAssociationUpdateReleasesAfterResponse(t *testing.T) {
	pfcpContext := installAssociationReleasePFCPContext(t)
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.84").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	associationContext := upf.EstablishAssociation(pfcpContext)
	upf.SetRecoveryTimeStamp(time.Now())
	t.Cleanup(upf.CancelAssociation)
	releaseCount := 0
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		releaseCtx: func(ctx context.Context, addr *net.UDPAddr) (*message.AssociationReleaseResponse, error) {
			if ctx != associationContext {
				t.Errorf("Association Release Request did not use the UPF association context")
			}
			releaseCount++
			if got, want := addr.IP.String(), "192.0.2.84"; got != want {
				t.Fatalf("release destination IP = %s, want %s", got, want)
			}
			if got, want := addr.Port, pfcpPeerPort; got != want {
				t.Fatalf("release destination port = %d, want %d", got, want)
			}
			return message.NewAssociationReleaseResponse(
				1,
				ie.NewNodeIDHeuristic("192.0.2.84"),
				ie.NewCause(ie.CauseRequestAccepted),
			), nil
		},
	})

	cause, afterResponse := p.UpdateAssociation(nodeID, message.NewAssociationUpdateRequest(
		1, ie.NewUPFunctionFeatures(0x21, 0x43), ie.NewPFCPAssociationReleaseRequest(1, 0),
	))
	if cause != ie.CauseRequestAccepted || afterResponse == nil {
		t.Fatalf("UpdateAssociation() cause = %d, afterResponse present = %t; want accepted release work",
			cause, afterResponse != nil)
	}
	if releaseCount != 0 {
		t.Fatal("Association Release was sent before afterResponse")
	}
	if got := upf.UPFunctionFeatures(); len(got) != 0 {
		t.Fatalf("features changed before afterResponse: %v", got)
	}
	if err := upf.IsAssociated(); err != nil {
		t.Fatalf("association was canceled before afterResponse: %v", err)
	}
	afterResponse()
	if got := upf.UPFunctionFeatures(); len(got) != 2 || got[0] != 0x21 || got[1] != 0x43 {
		t.Fatalf("features were not committed before release: %v", got)
	}
	if releaseCount != 1 {
		t.Fatalf("Association Release calls = %d, want 1", releaseCount)
	}
	// BeginAssociationRelease prevents a duplicate cleanup even if a caller
	// mistakenly invokes the same callback again.
	afterResponse()
	if releaseCount != 1 {
		t.Fatalf("Association Release calls after duplicate callback = %d, want 1", releaseCount)
	}
	if err := upf.IsAssociated(); err == nil {
		t.Fatal("UPF remains associated after Association Release")
	}
	if !upf.RecoveryTimeStamp().IsZero() {
		t.Fatalf("RecoveryTimeStamp = %v, want zero", upf.RecoveryTimeStamp())
	}
}

func TestAssociationUpdateDeletesAffectedSessionsBeforeRelease(t *testing.T) {
	initAssociationReleaseTestContext(t)
	targetNodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.86").To4(),
	}
	otherNodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.87").To4(),
	}
	upf := smf_context.NewUPF(&targetNodeID, nil)
	otherUPF := smf_context.NewUPF(&otherNodeID, nil)
	t.Cleanup(func() {
		smf_context.RemoveUPFNodeByNodeID(targetNodeID)
		smf_context.RemoveUPFNodeByNodeID(otherNodeID)
	})
	upf.EstablishAssociation(context.Background())
	upf.SetRecoveryTimeStamp(time.Now())
	t.Cleanup(upf.CancelAssociation)
	otherUPF.EstablishAssociation(context.Background())
	t.Cleanup(otherUPF.CancelAssociation)

	first := smf_context.NewSMContext("imsi-association-release-1", 1)
	second := smf_context.NewSMContext("imsi-association-release-2", 2)
	t.Cleanup(func() {
		smf_context.RemoveSMContext(first.Ref)
		smf_context.RemoveSMContext(second.Ref)
	})
	first.PFCPContext[targetNodeID.String()] = &smf_context.PFCPSessionContext{
		NodeID: targetNodeID, LocalSEID: 501, RemoteSEID: 601,
	}
	first.PFCPContext[otherNodeID.String()] = &smf_context.PFCPSessionContext{
		NodeID: otherNodeID, LocalSEID: 502, RemoteSEID: 602,
	}
	second.PFCPContext[targetNodeID.String()] = &smf_context.PFCPSessionContext{
		NodeID: targetNodeID, LocalSEID: 503, RemoteSEID: 603,
	}

	var mu sync.Mutex
	deletedRemoteSEIDs := make(map[uint64]bool)
	releaseCalled := false
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		deleteCtx: func(
			ctx context.Context,
			request *message.SessionDeletionRequest,
			addr *net.UDPAddr,
			localSEID uint64,
		) (*message.SessionDeletionResponse, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !upf.IsAssociationReleasing() {
				t.Error("Session Deletion was sent before UPF started Association Release")
			}
			gotIP := addr.IP.String()
			if gotIP != targetNodeID.String() && gotIP != otherNodeID.String() {
				t.Errorf("unexpected Session Deletion destination = %s", gotIP)
			}
			mu.Lock()
			deletedRemoteSEIDs[request.SEID()] = true
			mu.Unlock()
			return message.NewSessionDeletionResponse(
				0, 0, localSEID, request.Sequence(), 0,
				ie.NewCause(ie.CauseRequestAccepted),
				ie.NewUsageReportWithinSessionDeletionResponse(
					ie.NewURRID(uint32(localSEID)),
					ie.NewUsageReportTrigger(0, 0x08),
					ie.NewVolumeMeasurement(0x07, 300, 100, 200, 0, 0, 0),
				),
			), nil
		},
		release: func(*net.UDPAddr) (*message.AssociationReleaseResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			if len(deletedRemoteSEIDs) != 3 || !deletedRemoteSEIDs[601] ||
				!deletedRemoteSEIDs[602] || !deletedRemoteSEIDs[603] {
				t.Errorf("Association Release ran before complete PDU Session deletion: %v", deletedRemoteSEIDs)
			}
			if len(first.UrrReports) != 2 || len(second.UrrReports) != 1 {
				t.Errorf("Association Release ran before final reports were collected: first=%d second=%d",
					len(first.UrrReports), len(second.UrrReports))
			}
			releaseCalled = true
			return message.NewAssociationReleaseResponse(
				1, ie.NewNodeIDHeuristic(targetNodeID.String()), ie.NewCause(ie.CauseRequestAccepted),
			), nil
		},
	})

	cause, afterResponse := p.UpdateAssociation(targetNodeID, message.NewAssociationUpdateRequest(
		1, ie.NewPFCPAssociationReleaseRequest(1, 0),
	))
	if cause != ie.CauseRequestAccepted || afterResponse == nil {
		t.Fatalf("UpdateAssociation() cause = %d, afterResponse present = %t",
			cause, afterResponse != nil)
	}
	afterResponse()
	if !releaseCalled {
		t.Fatal("Association Release was not sent after PFCP Session Deletion")
	}
	if got := first.PFCPContext[targetNodeID.String()].RemoteSEID; got != 0 {
		t.Errorf("first target RemoteSEID = %d, want 0", got)
	}
	if got := second.PFCPContext[targetNodeID.String()].RemoteSEID; got != 0 {
		t.Errorf("second target RemoteSEID = %d, want 0", got)
	}
	if got := first.PFCPContext[otherNodeID.String()].RemoteSEID; got != 0 {
		t.Errorf("other UPF RemoteSEID = %d, want 0", got)
	}
	if got := len(first.UrrReports); got != 2 {
		t.Errorf("first Usage Reports = %d, want 2", got)
	}
	if got := len(second.UrrReports); got != 1 {
		t.Errorf("second Usage Reports = %d, want 1", got)
	}
}

func TestAssociationReleaseDeadlineCancelsDeletionBeforeRelease(t *testing.T) {
	initAssociationReleaseTestContext(t)
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.88").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)
	smContext := smf_context.NewSMContext("imsi-association-release-deadline", 3)
	t.Cleanup(func() { smf_context.RemoveSMContext(smContext.Ref) })
	smContext.PFCPContext[nodeID.String()] = &smf_context.PFCPSessionContext{
		NodeID: nodeID, LocalSEID: 701, RemoteSEID: 801,
	}

	deletionCanceled := false
	releaseCalled := false
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		deleteCtx: func(
			ctx context.Context,
			_ *message.SessionDeletionRequest,
			_ *net.UDPAddr,
			_ uint64,
		) (*message.SessionDeletionResponse, error) {
			<-ctx.Done()
			deletionCanceled = true
			return nil, ctx.Err()
		},
		release: func(*net.UDPAddr) (*message.AssociationReleaseResponse, error) {
			if !deletionCanceled {
				t.Error("Association Release ran before deadline canceled Session Deletion")
			}
			releaseCalled = true
			return message.NewAssociationReleaseResponse(
				1, ie.NewNodeIDHeuristic(nodeID.String()), ie.NewCause(ie.CauseRequestAccepted),
			), nil
		},
	})
	period := 2 * time.Second
	_, afterResponse := p.UpdateAssociation(nodeID, message.NewAssociationUpdateRequest(
		1,
		ie.NewPFCPAssociationReleaseRequest(1, 0),
		ie.NewGracefulReleasePeriod(period),
	))
	start := time.Now()
	afterResponse()
	if !releaseCalled {
		t.Fatal("Association Release was not sent after deadline")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Association Release deadline took %s, want less than 500ms", elapsed)
	}
}

func TestAssociationReleaseDeadlineIncludesInFlightSessionWork(t *testing.T) {
	initAssociationReleaseTestContext(t)
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.89").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)
	smContext := smf_context.NewSMContext("imsi-association-release-in-flight", 4)
	t.Cleanup(func() { smf_context.RemoveSMContext(smContext.Ref) })
	smContext.PFCPContext[nodeID.String()] = &smf_context.PFCPSessionContext{
		NodeID: nodeID, LocalSEID: 901, RemoteSEID: 902,
	}

	_, finishSessionWork, err := upf.BeginSessionWork()
	if err != nil {
		t.Fatalf("BeginSessionWork() error: %v", err)
	}
	deletionCalled := make(chan struct{}, 1)
	releaseCalled := make(chan struct{}, 1)
	p := &Processor{}
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		deleteCtx: func(
			context.Context,
			*message.SessionDeletionRequest,
			*net.UDPAddr,
			uint64,
		) (*message.SessionDeletionResponse, error) {
			deletionCalled <- struct{}{}
			return nil, context.DeadlineExceeded
		},
		release: func(*net.UDPAddr) (*message.AssociationReleaseResponse, error) {
			releaseCalled <- struct{}{}
			return message.NewAssociationReleaseResponse(
				1, ie.NewNodeIDHeuristic(nodeID.String()), ie.NewCause(ie.CauseRequestAccepted),
			), nil
		},
	})
	period := time.Duration(0)
	_, afterResponse := p.UpdateAssociation(nodeID, message.NewAssociationUpdateRequest(
		1,
		ie.NewPFCPAssociationReleaseRequest(1, 0),
		ie.NewGracefulReleasePeriod(period),
	))
	afterDone := make(chan struct{})
	go func() {
		afterResponse()
		close(afterDone)
	}()

	deadline := time.Now().Add(time.Second)
	for !upf.IsAssociationReleasing() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !upf.IsAssociationReleasing() {
		t.Fatal("UPF did not start Association Release while waiting for in-flight session work")
	}
	// A zero PFCP Graceful Release Period represents an immediate deadline.
	// Keep the existing work held until the release path is waiting on it.
	finishSessionWork()

	select {
	case <-afterDone:
	case <-time.After(time.Second):
		t.Fatal("association release did not finish after in-flight work completed")
	}
	select {
	case <-deletionCalled:
		t.Fatal("Session Deletion started after graceful-release deadline expired")
	default:
	}
	select {
	case <-releaseCalled:
	default:
		t.Fatal("Association Release was not sent after graceful-release deadline")
	}
}

func TestProcessorPassiveAssociationUpdateRequiresEstablishedAssociation(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.85").To4(),
	}
	smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	p := &Processor{}

	cause, afterResponse := p.UpdateAssociation(nodeID, message.NewAssociationUpdateRequest(1))
	if cause != ie.CauseNoEstablishedPFCPAssociation || afterResponse != nil {
		t.Fatalf("UpdateAssociation() cause = %d, afterResponse present = %t; want No Established Association",
			cause, afterResponse != nil)
	}
}

func TestProcessorPassiveAssociationRejectsUnknownUPF(t *testing.T) {
	p := &Processor{}
	peer := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.82").To4(),
	}
	cause, _ := p.SetupAssociation(peer, time.Now())
	if cause != ie.CauseRequestRejected {
		t.Fatalf("SetupAssociation() cause = %d, want Request Rejected", cause)
	}
	if cause = p.ReleaseAssociation(peer); cause != ie.CauseNoEstablishedPFCPAssociation {
		t.Fatalf("ReleaseAssociation() cause = %d, want No Established Association", cause)
	}
}

// Tests consolidated from charging_trigger_internal_test.go.
func TestBuildMultiUnitUsageIncludesFinalUsageReports(t *testing.T) {
	const urrID = uint32(77)
	smContext := &smf_context.SMContext{
		RequestedUnit: 4096,
		ChargingInfo: map[uint32]*smf_context.ChargingInfo{
			urrID: {
				ChargingMethod: models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING,
				ChargingLevel:  smf_context.PduSessionCharging,
				RatingGroup:    9,
				UpfId:          "192.0.2.10",
			},
		},
		UrrReports: []smf_context.UsageReport{
			{
				UrrId:          urrID,
				UpfId:          "192.0.2.10",
				TotalVolume:    300,
				UplinkVolume:   100,
				DownlinkVolume: 200,
				ReportTpye:     models.Chf_ConvCharging_TriggerType_FINAL,
			},
		},
	}

	usages := buildMultiUnitUsageFromUsageReport(smContext)
	if len(usages) != 1 {
		t.Fatalf("MultipleUnitUsage count = %d, want 1", len(usages))
	}
	if got := usages[0].RatingGroup; got != 9 {
		t.Errorf("RatingGroup = %d, want 9", got)
	}
	if len(usages[0].UsedUnitContainer) != 1 {
		t.Fatalf("UsedUnitContainer count = %d, want 1", len(usages[0].UsedUnitContainer))
	}
	used := usages[0].UsedUnitContainer[0]
	if used.TotalVolume != 300 || used.UplinkVolume != 100 || used.DownlinkVolume != 200 {
		t.Errorf("reported volume = total:%d uplink:%d downlink:%d, want 300/100/200",
			used.TotalVolume, used.UplinkVolume, used.DownlinkVolume)
	}
	if len(used.Triggers) != 1 ||
		used.Triggers[0].TriggerType != models.Chf_ConvCharging_TriggerType_FINAL {
		t.Errorf("charging triggers = %+v, want FINAL", used.Triggers)
	}
	if len(smContext.UrrReports) != 0 {
		t.Errorf("cached Usage Reports count after conversion = %d, want 0", len(smContext.UrrReports))
	}
}

// Tests consolidated from session_establishment_internal_test.go.
func TestApplyCreatedPDRsUpdatesUPFAllocatedFTEID(t *testing.T) {
	pdr := &smf_context.PDR{PDRID: 7}
	sessionContext := &smf_context.PFCPSessionContext{
		PDRs: map[uint16]*smf_context.PDR{7: pdr},
	}
	createdPDR := ie.NewCreatedPDR(
		ie.NewPDRID(7),
		ie.NewFTEID(0x01, 0x10203040, net.ParseIP("192.0.2.20").To4(), nil, 0),
	)

	if err := applyCreatedPDRs([]*ie.IE{createdPDR}, sessionContext, nil); err != nil {
		t.Fatalf("applyCreatedPDRs() error: %v", err)
	}
	got := pdr.PDI.LocalFTeid
	if got == nil {
		t.Fatal("LocalFTeid was not updated")
	}
	if got.Teid != 0x10203040 || !got.V4 || got.V6 {
		t.Fatalf("LocalFTeid = %+v, want IPv4 TEID %#x", got, uint32(0x10203040))
	}
	if !got.Ipv4Address.Equal(net.ParseIP("192.0.2.20")) {
		t.Fatalf("LocalFTeid IPv4 = %v, want 192.0.2.20", got.Ipv4Address)
	}
}

func TestApplyCreatedPDRsAllowsNoAllocatedFTEID(t *testing.T) {
	pdr := &smf_context.PDR{PDRID: 7}
	sessionContext := &smf_context.PFCPSessionContext{
		PDRs: map[uint16]*smf_context.PDR{7: pdr},
	}
	if err := applyCreatedPDRs(
		[]*ie.IE{ie.NewCreatedPDR(ie.NewPDRID(7))}, sessionContext, nil,
	); err != nil {
		t.Fatalf("applyCreatedPDRs() error: %v", err)
	}
	if pdr.PDI.LocalFTeid != nil {
		t.Fatalf("LocalFTeid = %+v, want unchanged nil", pdr.PDI.LocalFTeid)
	}
}

func TestApplyCreatedPDRsRejectsUnknownPDR(t *testing.T) {
	sessionContext := &smf_context.PFCPSessionContext{PDRs: make(map[uint16]*smf_context.PDR)}
	err := applyCreatedPDRs([]*ie.IE{
		ie.NewCreatedPDR(
			ie.NewPDRID(99),
			ie.NewFTEID(0x01, 1, net.ParseIP("192.0.2.20").To4(), nil, 0),
		),
	}, sessionContext, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown PDR ID 99") {
		t.Fatalf("applyCreatedPDRs() error = %v, want unknown PDR error", err)
	}
}

type fakeRollbackPFCPClient struct {
	delete func(
		*message.SessionDeletionRequest, *net.UDPAddr, uint64,
	) (*message.SessionDeletionResponse, error)
}

func (f *fakeRollbackPFCPClient) SendAssociationSetupRequest(
	context.Context, *net.UDPAddr,
) (*message.AssociationSetupResponse, error) {
	panic("unexpected Association Setup")
}

func (f *fakeRollbackPFCPClient) SendHeartbeatRequest(
	context.Context, *net.UDPAddr,
) (*message.HeartbeatResponse, error) {
	panic("unexpected Heartbeat")
}

func (f *fakeRollbackPFCPClient) SendSessionDeletionRequest(
	_ context.Context,
	request *message.SessionDeletionRequest,
	addr *net.UDPAddr,
	localSEID uint64,
) (*message.SessionDeletionResponse, error) {
	return f.delete(request, addr, localSEID)
}

func rollbackTestUPF(ip string) *smf_context.UPF {
	upf := &smf_context.UPF{
		NodeID: pfcptype.NodeID{
			NodeIdType: pfcptype.NodeIdTypeIpv4Address,
			IP:         net.ParseIP(ip).To4(),
		},
	}
	upf.EstablishAssociation(context.Background())
	return upf
}

func TestWaitAllPfcpRspReportsFailureWithoutCallback(t *testing.T) {
	results := make(chan SendPfcpResult, 2)
	results <- SendPfcpResult{Status: smf_context.SessionEstablishSuccess}
	results <- SendPfcpResult{Status: smf_context.SessionEstablishFailed}

	if waitAllPfcpRsp(&smf_context.SMContext{}, 2, results, nil) {
		t.Fatal("waitAllPfcpRsp() = success, want failure")
	}
}

func TestRollbackEstablishedPfcpSessionsDeletesOnlyCreatedSessions(t *testing.T) {
	const createdIP = "192.0.2.10"
	const failedIP = "192.0.2.11"
	const localSEID uint64 = 101
	const remoteSEID uint64 = 201
	type deletionCall struct {
		request   *message.SessionDeletionRequest
		addr      *net.UDPAddr
		localSEID uint64
	}
	calls := make(chan deletionCall, 2)
	client := &fakeRollbackPFCPClient{
		delete: func(
			request *message.SessionDeletionRequest,
			addr *net.UDPAddr,
			gotLocalSEID uint64,
		) (*message.SessionDeletionResponse, error) {
			calls <- deletionCall{request: request, addr: addr, localSEID: gotLocalSEID}
			return message.NewSessionDeletionResponse(
				0, 0, gotLocalSEID, request.Sequence(), 0,
				ie.NewCause(ie.CauseRequestAccepted),
			), nil
		},
	}
	processor := &Processor{}
	processor.SetActivePFCPClient(client)
	createdSession := &smf_context.PFCPSessionContext{LocalSEID: localSEID, RemoteSEID: remoteSEID}
	failedSession := &smf_context.PFCPSessionContext{LocalSEID: 102}
	smContext := &smf_context.SMContext{PFCPContext: map[string]*smf_context.PFCPSessionContext{
		createdIP: createdSession,
		failedIP:  failedSession,
	}}
	targets := map[string]*PFCPState{
		createdIP: {upf: rollbackTestUPF(createdIP)},
		failedIP:  {upf: rollbackTestUPF(failedIP)},
	}

	if err := processor.rollbackEstablishedPfcpSessions(smContext, targets); err != nil {
		t.Fatalf("rollbackEstablishedPfcpSessions() error: %v", err)
	}
	call := <-calls
	if call.request.SEID() != remoteSEID {
		t.Errorf("deletion request SEID = %d, want %d", call.request.SEID(), remoteSEID)
	}
	if call.localSEID != localSEID {
		t.Errorf("deletion local SEID = %d, want %d", call.localSEID, localSEID)
	}
	if got := call.addr.IP.String(); got != createdIP {
		t.Errorf("deletion destination IP = %s, want %s", got, createdIP)
	}
	select {
	case extra := <-calls:
		t.Fatalf("unexpected deletion for %v", extra.addr)
	default:
	}
	if createdSession.RemoteSEID != 0 {
		t.Errorf("accepted deletion left RemoteSEID = %d, want 0", createdSession.RemoteSEID)
	}
}

func TestRollbackEstablishedPfcpSessionsKeepsSEIDWhenDeletionRejected(t *testing.T) {
	const upfIP = "192.0.2.20"
	const remoteSEID uint64 = 301
	client := &fakeRollbackPFCPClient{
		delete: func(
			request *message.SessionDeletionRequest,
			_ *net.UDPAddr,
			localSEID uint64,
		) (*message.SessionDeletionResponse, error) {
			return message.NewSessionDeletionResponse(
				0, 0, localSEID, request.Sequence(), 0,
				ie.NewCause(ie.CauseNoResourcesAvailable),
			), nil
		},
	}
	processor := &Processor{}
	processor.SetActivePFCPClient(client)
	session := &smf_context.PFCPSessionContext{LocalSEID: 202, RemoteSEID: remoteSEID}
	smContext := &smf_context.SMContext{PFCPContext: map[string]*smf_context.PFCPSessionContext{upfIP: session}}

	err := processor.rollbackEstablishedPfcpSessions(smContext, map[string]*PFCPState{
		upfIP: {upf: rollbackTestUPF(upfIP)},
	})
	if err == nil || !strings.Contains(err.Error(), "Deletion rejected") {
		t.Fatalf("rollbackEstablishedPfcpSessions() error = %v, want rejection", err)
	}
	if session.RemoteSEID != remoteSEID {
		t.Errorf("rejected deletion changed RemoteSEID = %d, want %d", session.RemoteSEID, remoteSEID)
	}
}
