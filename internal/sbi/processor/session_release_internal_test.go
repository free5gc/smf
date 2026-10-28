package processor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	nas_message "github.com/free5gc/nas/message"
	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

func TestChargingReleaseInProgressBlocksCleanup(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	sm.SMPolicyID = chargingReleaseOrderPolicyID
	started := make(chan struct{})
	unblock := make(chan struct{})
	releaseResponse := sync.OnceFunc(func() { close(unblock) })
	defer releaseResponse()
	var policyDeletes atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/delete") {
			policyDeletes.Add(1)
		} else {
			close(started)
			<-unblock
		}
		w.WriteHeader(http.StatusNoContent)
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	sm.SelectedPCFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
	result := make(chan error, 1)
	go func() { result <- p.ReleaseChargingSession(sm) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("CHF did not receive the release request")
	}
	p.RemoveSMContextFromAllNF(sm, false)
	require.Zero(t, policyDeletes.Load())
	require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
	state, lastErr := sm.ChargingReleaseResult()
	require.Equal(t, smf_context.ChargingReleaseInProgress, state)
	require.NoError(t, lastErr, "an in-flight request has no failure result yet")
	require.Len(t, sm.UrrReports, 1, "final usage must remain until CHF acknowledges it")
	_, pending, pendingErr := sm.PendingChargingRelease()
	require.NoError(t, pendingErr)
	require.NotNil(t, pending, "the full request must be saved before waiting for CHF")
	releaseResponse()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("charging release did not finish")
	}
	require.NoError(t, p.ReleaseChargingSession(sm), "completed success can be reused without another HTTP request")
}

func TestChargingReleaseWithoutChargingSession(t *testing.T) {
	p := &Processor{}
	sm := &smf_context.SMContext{}
	require.NoError(t, p.ReleaseChargingSession(sm), "no CHF resource means no settlement is needed")
	require.True(t, sm.BeginChargingRelease(), "a no-op must not consume the release guard")
}

func TestChargingInvocationSequenceAcrossRequestTypes(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	received := make(chan models.Chf_ConvCharging_ChargingDataRequest, 3)
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request models.Chf_ConvCharging_ChargingDataRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode charging request: %v", err)
		}
		received <- request
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/chargingdata"):
			w.Header().Set("Location", "http://"+r.Host+"/nchf-convergedcharging/v3/chargingdata/charging-1226")
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/release"):
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			w.WriteHeader(http.StatusOK)
		}
		if err := json.NewEncoder(w).Encode(models.Chf_ConvCharging_ChargingDataResponse{
			InvocationSequenceNumber: request.InvocationSequenceNumber,
		}); err != nil {
			t.Errorf("write charging response: %v", err)
		}
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	_, problem, err := p.Consumer().SendConvergedChargingRequest(sm, smf_context.CHARGING_INIT, nil)
	require.Nil(t, problem)
	require.NoError(t, err)
	_, err = p.sendChargingUpdate(sm, nil, 0)
	require.NoError(t, err)
	require.NoError(t, p.ReleaseChargingSession(sm))
	for expected := int32(1); expected <= 3; expected++ {
		request := <-received
		require.Equal(t, expected, request.InvocationSequenceNumber)
		require.NotNil(t, request.InvocationTimeStamp)
	}
	_, pending, err := sm.PendingChargingRelease()
	require.NoError(t, err)
	require.Nil(t, pending, "successful settlement must remove its pending request")
	require.Empty(t, sm.UrrReports)
}

func TestChargingReleaseRecoveryResumesAuthorizedCleanup(t *testing.T) {
	for _, path := range []string{chargingAMFRelease, "local release", chargingCommonCleanup} {
		t.Run(path, func(t *testing.T) {
			p, sm := chargingReleaseOrderSession(t)
			sm.SMPolicyID = chargingReleaseOrderPolicyID
			var calls, deletes, notifications atomic.Int32
			bodies := make(chan models.Chf_ConvCharging_ChargingDataRequest, 2)
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/release"):
					var request models.Chf_ConvCharging_ChargingDataRequest
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					require.Len(t, request.MultipleUnitUsage, 1)
					require.Equal(t, int32(40), request.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume)
					require.Nil(t, request.MultipleUnitUsage[0].RequestedUnit)
					bodies <- request
					if calls.Add(1) == 1 {
						chargingReleaseOrderFailure(t, w)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				case strings.HasSuffix(r.URL.Path, "/delete"):
					require.Equal(t, int32(2), calls.Load(), "settlement must precede policy deletion")
					deletes.Add(1)
					w.WriteHeader(http.StatusNoContent)
				case r.URL.Path == chargingReleasedPath:
					notifications.Add(1)
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected cleanup request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			sm.SmStatusNotifyUri = server.URL + chargingReleasedPath
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
			sm.SelectedPCFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
			switch path {
			case chargingAMFRelease:
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				p.HandlePDUSessionSMContextRelease(ctx, models.ReleaseSmContextRequestBody{}, sm.Ref)
				ctx.Writer.WriteHeaderNow()
				require.Equal(t, http.StatusNoContent, recorder.Code)
				recoverChargingNow(p)
			case "local release":
				prepareReleaseResponseTest(t, p, sm)
				// Same callback URI avoids the separate old-registrant notification.
				p.HandlePDUSessionSMContextLocalRelease(sm, &models.Smf_PDUSess_SmContextCreateData{
					SmContextStatusUri: sm.SmStatusNotifyUri,
				})
			case chargingCommonCleanup:
				sm.SMLock.Lock()
				p.RemoveSMContextFromAllNF(sm, true)
				sm.SMLock.Unlock()
			}
			require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
			require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID)
			require.Zero(t, deletes.Load())
			require.Len(t, sm.UrrReports, 1)
			require.Equal(t, uint64(40), sm.UrrReports[0].TotalVolume)
			sm.SMLock.Lock()
			p.RemoveSMContextFromAllNF(sm, false)
			sm.SMLock.Unlock()
			require.Equal(t, int32(1), calls.Load(), "ordinary cleanup must not replace the failed invocation")
			recoverChargingNow(p)
			require.Eventually(t, func() bool { return smf_context.GetSMContextByRef(sm.Ref) == nil },
				time.Second, time.Millisecond)
			require.Equal(t, <-bodies, <-bodies, "Release retry must retain its full original request")
			require.Equal(t, int32(1), deletes.Load())
			wantNotifications := int32(0)
			if path == chargingCommonCleanup {
				wantNotifications = 1
			}
			require.Equal(t, wantNotifications, notifications.Load())
			recoverChargingNow(p)
			require.Equal(t, int32(2), calls.Load(), "removed work must not run again")
		})
	}
}

func TestChargingReleaseWaitsForPendingUpdateThenSettlesRemainingUsage(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	sm.SMPolicyID = chargingReleaseOrderPolicyID
	updates := make(chan models.Chf_ConvCharging_ChargingDataRequest, 2)
	releases := make(chan models.Chf_ConvCharging_ChargingDataRequest, 1)
	var updateCalls, releaseCalls, policyDeletes atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/update"):
			var request models.Chf_ConvCharging_ChargingDataRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			updates <- request
			if updateCalls.Add(1) == 1 {
				chargingReleaseOrderFailure(t, w)
				return
			}
			chargingSuccess(t, w, &request)
		case strings.HasSuffix(r.URL.Path, "/release"):
			require.Equal(t, int32(2), updateCalls.Load())
			var request models.Chf_ConvCharging_ChargingDataRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			releases <- request
			releaseCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/delete"):
			require.Equal(t, int32(1), releaseCalls.Load())
			policyDeletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	sm.SelectedPCFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
	sm.SMLock.Lock()
	p.ReportUsageAndUpdateQuota(sm)
	sm.UrrReports = append(sm.UrrReports, smf_context.UsageReport{
		UrrId: 1, UpfId: "upf-a", TotalVolume: 20, ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL,
	})
	p.RemoveSMContextFromAllNF(sm, true)
	require.Error(t, sm.BeginChargingUpdate(), "remembered Release must block fresh updates")
	require.Zero(t, releaseCalls.Load(), "unsettled Update must finish first")
	require.Zero(t, policyDeletes.Load())
	require.Len(t, sm.UrrReports, 2)
	sm.SMLock.Unlock()
	recoverChargingNow(p)
	require.Eventually(t, func() bool { return smf_context.GetSMContextByRef(sm.Ref) == nil },
		time.Second, time.Millisecond)
	original, replay := <-updates, <-updates
	require.Equal(t, original, replay)
	final := <-releases
	require.Equal(t, original.InvocationSequenceNumber+1, final.InvocationSequenceNumber)
	require.Len(t, final.MultipleUnitUsage, 1)
	require.Len(t, final.MultipleUnitUsage[0].UsedUnitContainer, 1)
	require.Equal(t, int32(20), final.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume,
		"Release must charge only the reports not acknowledged by Update")
	require.Nil(t, final.MultipleUnitUsage[0].RequestedUnit)
	require.Equal(t, int32(1), policyDeletes.Load())
}

func TestChargingCleanupRetriesOnlyIncompleteStages(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	sm.SMPolicyID = chargingReleaseOrderPolicyID
	self := smf_context.GetSelf()
	previousUes, previousUDM := self.Ues, self.UDMProfile
	self.Ues = smf_context.InitSmfUeData()
	t.Cleanup(func() { self.Ues, self.UDMProfile = previousUes, previousUDM })
	self.Ues.IncrementPduSessionCount(sm.Supi)
	self.Ues.SetSubscriptionId(sm.Supi, "subscription-1226")
	sm.SdmCounted = true
	var chfCalls, policyCalls, udmCalls, notifications atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/release"):
			chfCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/delete"):
			if policyCalls.Add(1) == 1 {
				chargingReleaseOrderFailure(t, w)
				return
			}
			// Model a successful prior deletion whose acknowledgement was lost.
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			require.NoError(t, json.NewEncoder(w).Encode(models.ProblemDetails{Status: 404, Cause: "CONTEXT_NOT_FOUND"}))
		case strings.HasSuffix(r.URL.Path, "/sdm-subscriptions/subscription-1226"):
			if udmCalls.Add(1) == 1 {
				chargingReleaseOrderFailure(t, w)
				return
			}
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			require.NoError(t, json.NewEncoder(w).Encode(models.ProblemDetails{Status: 404, Cause: "CONTEXT_NOT_FOUND"}))
		case r.URL.Path == chargingReleasedPath:
			notifications.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	sm.SmStatusNotifyUri = server.URL + chargingReleasedPath
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	sm.SelectedPCFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
	self.UDMProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NUDM_SDM, server.URL)
	sm.SMLock.Lock()
	p.RemoveSMContextFromAllNF(sm, true)
	sm.SMLock.Unlock()
	require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID)
	require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
	// Polling and repeated callers must respect the cleanup backoff.
	p.recoverPendingCharging(context.Background(), time.Now())
	require.Equal(t, int32(1), policyCalls.Load())
	recoverChargingNow(p)
	require.Empty(t, sm.SMPolicyID, "acknowledged policy deletion must not repeat")
	require.True(t, sm.SdmCounted, "failed SDM deletion must retain its bookkeeping slot")
	require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
	require.Zero(t, notifications.Load())
	recoverChargingNow(p)
	require.Eventually(t, func() bool { return smf_context.GetSMContextByRef(sm.Ref) == nil },
		time.Second, time.Millisecond)
	require.Equal(t, int32(1), chfCalls.Load(), "cleanup retries must never repeat final settlement")
	require.Equal(t, int32(2), policyCalls.Load())
	require.Equal(t, int32(2), udmCalls.Load())
	require.Equal(t, int32(1), notifications.Load())
	require.False(t, self.Ues.UeExists(sm.Supi))
	require.False(t, sm.SdmCounted)
}

func TestChargingCleanupRunsOnceAndPreservesReplacementSession(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	self := smf_context.GetSelf()
	previousUes := self.Ues
	self.Ues = smf_context.InitSmfUeData()
	t.Cleanup(func() { self.Ues = previousUes })
	self.Ues.IncrementPduSessionCount(sm.Supi)
	self.Ues.IncrementPduSessionCount(sm.Supi)
	sm.SdmCounted = true
	replacement := smf_context.NewSMContext(sm.Supi, sm.PDUSessionID)
	require.NotNil(t, replacement)
	t.Cleanup(func() { smf_context.RemoveSMContext(replacement.Ref) })
	var calls, notifications atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == chargingReleasedPath {
			notifications.Add(1)
		} else {
			calls.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	sm.SmStatusNotifyUri = server.URL + chargingReleasedPath
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	sm.SMLock.Lock()
	p.RemoveSMContextFromAllNF(sm, false)
	p.RemoveSMContextFromAllNF(sm, true)
	p.RemoveSMContextFromAllNF(sm, true)
	require.Equal(t, 1, self.Ues.GetPduSessionCount(sm.Supi))
	sm.SMLock.Unlock()
	require.Eventually(t, func() bool { return smf_context.GetSMContextByRef(sm.Ref) == nil },
		time.Second, time.Millisecond)
	require.Same(t, replacement, smf_context.GetSMContextById(sm.Supi, sm.PDUSessionID))
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, int32(1), notifications.Load(), "pending notification requirements must merge")
	require.Equal(t, 1, self.Ues.GetPduSessionCount(sm.Supi), "replacement's SDM slot must survive")
	require.Eventually(t, func() bool {
		_, retained := p.chargingReleases.Load(sm.Ref)
		return !retained
	}, time.Second, time.Millisecond)
}

func TestChargingCleanupCancellationKeepsIncompletePolicy(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	sm.SMPolicyID = chargingReleaseOrderPolicyID
	started := make(chan struct{})
	finish := make(chan struct{})
	defer close(finish)
	var calls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/release") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if calls.Add(1) == 1 {
			chargingReleaseOrderFailure(t, w)
			return
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-finish:
		}
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	sm.SelectedPCFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
	sm.SMLock.Lock()
	p.RemoveSMContextFromAllNF(sm, false)
	sm.SMLock.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		p.recoverPendingCharging(ctx, time.Now().Add(time.Hour))
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cleanup retry did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("policy cleanup ignored cancellation")
	}
	require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
	require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID)
}

func TestChargingReleaseErrorUnionNeverCompletesCleanup(t *testing.T) {
	for _, status := range []int{400, 404, 409} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			p, sm := chargingReleaseOrderSession(t)
			sm.SMPolicyID = chargingReleaseOrderPolicyID
			var calls atomic.Int32
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(status)
				require.NoError(t, json.NewEncoder(w).Encode(models.ProblemDetails{
					Status: int32(status), Cause: "CHARGING_FAILED",
				}))
			})
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
			p.RemoveSMContextFromAllNF(sm, false)
			recoverChargingNow(p)
			state, err := sm.ChargingReleaseResult()
			require.Equal(t, smf_context.ChargingReleaseFailed, state)
			require.Error(t, err)
			require.Equal(t, int32(1), calls.Load())
			require.Len(t, sm.UrrReports, 1)
			require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID)
			require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
		})
	}
}

const (
	chargingUERelease = "UE release"
	chargingUEN2First = "UE release, N2 acknowledgement first"
)

func prepareReleaseResponseTest(t *testing.T, p *Processor, sm *smf_context.SMContext) *atomic.Int32 {
	t.Helper()
	self := smf_context.GetSelf()
	previousUPI, previousConfig := self.UserPlaneInformation, factory.SmfConfig
	self.UserPlaneInformation = &smf_context.UserPlaneInformation{UPFsIPtoID: make(map[string]string)}
	factory.SmfConfig = &factory.Config{Configuration: &factory.Configuration{
		T3592: &factory.TimerValue{Enable: false},
	}}
	t.Cleanup(func() { self.UserPlaneInformation, factory.SmfConfig = previousUPI, previousConfig })
	node := pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("192.0.2.122").To4()}
	upf := smf_context.NewUPF(&node, nil)
	upf.EstablishAssociation(context.Background())
	t.Cleanup(func() { upf.CancelAssociation(); smf_context.RemoveUPFNodeByNodeID(node) })
	self.UserPlaneInformation.UPFsIPtoID[node.IP.String()] = upf.UUID()
	sm.PFCPReleaseDone = false
	sm.PFCPContext[node.IP.String()] = &smf_context.PFCPSessionContext{LocalSEID: 100, RemoteSEID: 200}
	sm.Tunnel.DataPathPool[1] = &smf_context.DataPath{FirstDPNode: &smf_context.DataPathNode{
		UPF: upf, UpLinkTunnel: &smf_context.GTPTunnel{}, DownLinkTunnel: &smf_context.GTPTunnel{},
	}}
	sm.Tunnel.ANInformation.IPAddress = net.ParseIP("192.0.2.123")
	sm.UpCnxState = models.Smf_PDUSess_UpCnxState_ACTIVATED
	var calls atomic.Int32
	p.SetActivePFCPClient(&fakeActivePFCPClient{deleteCtx: func(
		_ context.Context, _ *message.SessionDeletionRequest, _ *net.UDPAddr, localSEID uint64,
	) (*message.SessionDeletionResponse, error) {
		calls.Add(1)
		return message.NewSessionDeletionResponse(0, 0, localSEID, 0, 0, ie.NewCause(ie.CauseRequestAccepted)), nil
	}})
	return &calls
}

func releaseResponseRequest(
	t *testing.T, p *Processor, sm *smf_context.SMContext, path string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	if path == chargingAMFRelease {
		p.HandlePDUSessionSMContextRelease(ctx, models.ReleaseSmContextRequestBody{}, sm.Ref)
	} else {
		body := models.UpdateSmContextRequestBody{JsonData: &models.Smf_PDUSess_SmContextUpdateData{}}
		if path == "duplicate session" {
			body.JsonData.Cause = models.Smf_PDUSess_Cause_REL_DUE_TO_DUPLICATE_SESSION_ID
		} else {
			payload, err := nas_message.Marshal(&nas_message.PDUSessRelReq{
				PDUSessId: 1, PTI: 1,
			}, nil, nas_message.SecHdrTypePlainNas)
			require.NoError(t, err)
			body.BinaryDataN1SmMessage = related("GSM_NAS", payload)
		}
		p.HandlePDUSessionSMContextUpdate(ctx, body, sm.Ref)
	}
	ctx.Writer.WriteHeaderNow()
	return recorder
}

func TestChargingReleaseDoesNotBlockAMFResponse(t *testing.T) {
	for _, path := range []string{chargingUERelease, chargingUEN2First, chargingAMFRelease, "duplicate session"} {
		t.Run(path, func(t *testing.T) {
			ueRelease := path == chargingUERelease || path == chargingUEN2First
			p, sm := chargingReleaseOrderSession(t)
			n4Calls := prepareReleaseResponseTest(t, p, sm)
			sm.SMPolicyID = chargingReleaseOrderPolicyID
			var calls, deletes atomic.Int32
			requests := make(chan models.Chf_ConvCharging_ChargingDataRequest, 2)
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/release"):
					var request models.Chf_ConvCharging_ChargingDataRequest
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					requests <- request
					if calls.Add(1) == 1 {
						chargingReleaseOrderFailure(t, w)
						return
					}
				case strings.HasSuffix(r.URL.Path, "/delete"):
					require.Equal(t, int32(2), calls.Load(), "settlement must precede policy deletion")
					deletes.Add(1)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
			sm.SelectedPCFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
			recorder := releaseResponseRequest(t, p, sm, path)
			require.Equal(t, int32(1), n4Calls.Load())
			require.True(t, sm.PFCPReleaseDone)
			require.Zero(t, calls.Load(), "AMF response must not wait for the first CHF request")
			if path == chargingAMFRelease {
				require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
				require.Empty(t, recorder.Body.String())
			} else {
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				var response models.UpdateSmContextResponse200
				require.NoError(t, openapi.Deserialize(&response, recorder.Body.Bytes(), recorder.Header().Get("Content-Type")))
				require.NotNil(t, response.JsonData)
				require.Equal(t, models.Smf_PDUSess_N2SmInfoType_PDU_RES_REL_CMD, response.JsonData.N2SmInfoType)
				require.NotNil(t, response.BinaryDataN2SmInformation)
				var transfer ngapie.PDUSessionResourceReleaseCommandTransfer
				require.NoError(t, ngapie.UnmarshalBinary(response.BinaryDataN2SmInformation.Content, &transfer))
				require.NotNil(t, transfer.Cause)
				if ueRelease {
					require.NotNil(t, response.BinaryDataN1SmMessage)
					command, err := nas_message.ParseGSM(response.BinaryDataN1SmMessage.Content)
					require.NoError(t, err)
					require.IsType(t, &nas_message.PDUSessRelCmd{}, command)
				}
			}
			require.Equal(t, smf_context.InActivePending, sm.State())
			require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
			require.Error(t, sm.BeginChargingUpdate(), "queued Release must prevent new charging updates")
			recoverChargingNow(p)
			state, chargingErr := sm.ChargingReleaseResult()
			require.Equal(t, smf_context.ChargingReleaseFailed, state)
			require.Error(t, chargingErr)
			require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
			require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID)
			require.Len(t, sm.UrrReports, 1)
			require.Zero(t, deletes.Load())
			recoverChargingNow(p)
			if ueRelease {
				require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref), "wait for UE and N2 completion")
				require.Zero(t, deletes.Load())
				acknowledgeN2 := func() {
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					p.HandlePDUSessionSMContextUpdate(ctx, models.UpdateSmContextRequestBody{
						JsonData: &models.Smf_PDUSess_SmContextUpdateData{
							N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_PDU_RES_REL_RSP,
						},
					}, sm.Ref)
				}
				if path == chargingUEN2First {
					acknowledgeN2()
					require.Zero(t, deletes.Load(), "N2 acknowledgement must wait for UE completion")
				}
				complete, err := nas_message.Marshal(&nas_message.PDUSessRelComplete{
					PDUSessId: 1, PTI: 1,
				}, nil, nas_message.SecHdrTypePlainNas)
				require.NoError(t, err)
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				p.HandlePDUSessionSMContextUpdate(ctx, models.UpdateSmContextRequestBody{
					JsonData: &models.Smf_PDUSess_SmContextUpdateData{}, BinaryDataN1SmMessage: related("GSM_NAS", complete),
				}, sm.Ref)
				if path == chargingUERelease {
					require.Zero(t, deletes.Load(), "UE completion must still wait for N2 release acknowledgement")
					acknowledgeN2()
				}
			}
			require.Eventually(t, func() bool { return smf_context.GetSMContextByRef(sm.Ref) == nil },
				time.Second, time.Millisecond)
			require.Equal(t, <-requests, <-requests, "background retry must preserve its original invocation and usage")
			require.Equal(t, int32(1), deletes.Load())
			require.Empty(t, sm.UrrReports)
			require.Equal(t, int32(1), n4Calls.Load(), "background accounting must not repeat N4 deletion")
		})
	}
}
