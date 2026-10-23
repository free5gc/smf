package processor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	nas_message "github.com/free5gc/nas/message"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

func TestChargingReleasePrecedesPolicyDeletion(t *testing.T) {
	for _, path := range []string{chargingAMFRelease, "local release"} {
		t.Run(path, func(t *testing.T) {
			p, sm := chargingReleaseOrderSession(t)
			var mu sync.Mutex
			var events []string
			accountExists := true
			settlementSucceeded := false
			var received models.Chf_ConvCharging_ChargingDataRequest
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/npcf-smpolicycontrol/v1/sm-policies/policy-1226/delete":
					events = append(events, "PCF policy delete")
					accountExists = false // Model PCF deleting the record used by settlement.
					w.WriteHeader(http.StatusNoContent)
				case "/nchf-convergedcharging/v3/chargingdata/charging-1226/release":
					err := json.NewDecoder(r.Body).Decode(&received)
					if err != nil {
						t.Errorf("decode charging request: %v", err)
					}
					events = append(events, "CHF final settlement")
					if !accountExists {
						chargingReleaseOrderFailure(t, w)
						return
					}
					settlementSucceeded = true
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			sm.SMPolicyID = chargingReleaseOrderPolicyID
			sm.SelectedPCFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL,
				server.URL)
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING,
				server.URL)
			status := 0
			if path == chargingAMFRelease {
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				p.HandlePDUSessionSMContextRelease(ctx, models.ReleaseSmContextRequestBody{}, sm.Ref)
				ctx.Writer.WriteHeaderNow()
				status = recorder.Code
				require.Equal(t, http.StatusNoContent, status)
			} else {
				p.HandlePDUSessionSMContextLocalRelease(sm, &models.Smf_PDUSess_SmContextCreateData{})
			}
			require.Eventually(t, func() bool { return smf_context.GetSMContextByRef(sm.Ref) == nil },
				time.Second, time.Millisecond, "wait for asynchronous context cleanup")
			mu.Lock()
			defer mu.Unlock()
			require.Len(t, received.MultipleUnitUsage, 1, "CHF must actually receive final usage")
			require.Equal(t, int32(40), received.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume)
			require.Nil(t, received.MultipleUnitUsage[0].RequestedUnit)
			t.Logf("observed: events=%v; settlementSucceeded=%v; handlerHTTP=%d; reportsRemaining=%d; contextRemoved=true",
				events, settlementSucceeded, status, len(sm.UrrReports))
			want := []string{"CHF final settlement", "PCF policy delete"}
			if !reflect.DeepEqual(want, events) {
				t.Errorf("unsafe release order: got %v; want %v", events, want)
			}
			if !settlementSucceeded {
				t.Error("policy deletion invalidated the pending final settlement")
			}
		})
	}
}

func TestChargingReleaseFailurePreservesPolicyAndContext(t *testing.T) {
	for _, path := range []string{chargingAMFRelease, "local release", "UE release", chargingCommonCleanup} {
		t.Run(path, func(t *testing.T) {
			p, sm := chargingReleaseOrderSession(t)
			if path == "UE release" {
				self := smf_context.GetSelf()
				oldUPI, oldConfig := self.UserPlaneInformation, factory.SmfConfig
				self.UserPlaneInformation = &smf_context.UserPlaneInformation{}
				factory.SmfConfig = &factory.Config{Configuration: &factory.Configuration{
					T3592: &factory.TimerValue{Enable: false},
				}}
				t.Cleanup(func() {
					self.UserPlaneInformation, factory.SmfConfig = oldUPI, oldConfig
				})
			}
			var mu sync.Mutex
			chargingCalls, policyDeletes := 0, 0
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/npcf-smpolicycontrol/v1/sm-policies/policy-1226/delete":
					policyDeletes++
					w.WriteHeader(http.StatusNoContent)
				case "/nchf-convergedcharging/v3/chargingdata/charging-1226/release":
					chargingCalls++
					chargingReleaseOrderFailure(t, w)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			sm.SMPolicyID = chargingReleaseOrderPolicyID
			sm.SelectedPCFProfile = chargingReleaseOrderProfile(
				models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(
				models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
			for attempt := 0; attempt < 2; attempt++ {
				switch path {
				case chargingAMFRelease:
					recorder := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(recorder)
					p.HandlePDUSessionSMContextRelease(ctx, models.ReleaseSmContextRequestBody{}, sm.Ref)
					ctx.Writer.WriteHeaderNow()
					require.Equal(t, http.StatusInternalServerError, recorder.Code)
					var problem models.ProblemDetails
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &problem))
					require.Equal(t, "SYSTEM_FAILURE", problem.Cause)
				case "local release":
					p.HandlePDUSessionSMContextLocalRelease(sm, &models.Smf_PDUSess_SmContextCreateData{})
				case "UE release":
					if attempt == 0 {
						nasPayload, err := nas_message.Marshal(&nas_message.PDUSessRelReq{
							PDUSessId: 1, PTI: 1,
						}, nil, nas_message.SecHdrTypePlainNas)
						require.NoError(t, err)
						recorder := httptest.NewRecorder()
						ctx, _ := gin.CreateTestContext(recorder)
						p.HandlePDUSessionSMContextUpdate(ctx, models.UpdateSmContextRequestBody{
							JsonData:              &models.Smf_PDUSess_SmContextUpdateData{},
							BinaryDataN1SmMessage: related("GSM_NAS", nasPayload),
						}, sm.Ref)
						ctx.Writer.WriteHeaderNow()
						require.Equal(t, http.StatusInternalServerError, recorder.Code)
					} else {
						p.RemoveSMContextFromAllNF(sm, false)
					}
				case chargingCommonCleanup:
					sm.SMLock.Lock()
					p.RemoveSMContextFromAllNF(sm, false)
					sm.SMLock.Unlock()
				}
				require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID)
				require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
				require.Equal(t, smf_context.InActivePending, sm.State())
				require.Len(t, sm.UrrReports, 1, "failed final usage must not be discarded")
				require.Equal(t, uint64(40), sm.UrrReports[0].TotalVolume)
			}
			// Even a later cleanup entry point must retain the failed result rather
			// than treating the one-shot guard as a successful settlement.
			sm.SMLock.Lock()
			p.RemoveSMContextFromAllNF(sm, false)
			sm.SMLock.Unlock()
			require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, 1, chargingCalls, "an uncertain settlement must not be blindly retried")
			require.Zero(t, policyDeletes)
		})
	}
}

func TestChargingReleaseConnectionFailureReturnsRecordedError(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("closed CHF must not receive a request")
		w.WriteHeader(http.StatusNoContent)
	})
	server.Close()
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	firstErr := p.ReleaseChargingSession(sm)
	require.Error(t, firstErr)
	require.Len(t, sm.UrrReports, 1)
	_, pending, pendingErr := sm.PendingChargingRelease()
	require.NoError(t, pendingErr)
	require.NotNil(t, pending, "transport failure must preserve the prepared request")
	require.ErrorIs(t, p.ReleaseChargingSession(sm), firstErr)
	require.Len(t, sm.UrrReports, 1, "repeated cleanup must not duplicate restored usage")
}

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

func TestChargingReleaseFailureKeepsOriginalRequest(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	originalRef := sm.ChargingDataRef
	var calls atomic.Int32
	received := make(chan models.Chf_ConvCharging_ChargingDataRequest, 1)
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request models.Chf_ConvCharging_ChargingDataRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode final charging request: %v", err)
		}
		received <- request
		chargingReleaseOrderFailure(t, w)
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	firstErr := p.ReleaseChargingSession(sm)
	require.Error(t, firstErr)
	wireRequest := <-received
	ref, snapshot, err := sm.PendingChargingRelease()
	require.NoError(t, err)
	require.Equal(t, originalRef, ref)
	require.Equal(t, wireRequest, *snapshot)
	require.Positive(t, snapshot.InvocationSequenceNumber)
	require.NotNil(t, snapshot.InvocationTimeStamp)
	require.Len(t, sm.UrrReports, 1)

	// Neither changing the live context nor editing a returned copy may change
	// the request that identifies this unsettled operation.
	sm.ChargingDataRef = "changed-resource"
	sm.Supi = "imsi-changed"
	sm.SNssai.Sd = "ffffff"
	sm.UrrReports[0].TotalVolume = 999
	snapshot.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume = 123
	ref, snapshot, err = sm.PendingChargingRelease()
	require.NoError(t, err)
	require.Equal(t, originalRef, ref)
	require.Equal(t, wireRequest, *snapshot)
	require.ErrorIs(t, p.ReleaseChargingSession(sm), firstErr)
	require.Equal(t, int32(1), calls.Load(), "preserving a request must not enable automatic retries")
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
				require.Equal(t, http.StatusInternalServerError, recorder.Code)
			case "local release":
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

func TestChargingReleaseRecoveryWaitsForUECompletionBeforeCleanup(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	self := smf_context.GetSelf()
	previousUPI, previousConfig := self.UserPlaneInformation, factory.SmfConfig
	self.UserPlaneInformation = &smf_context.UserPlaneInformation{}
	factory.SmfConfig = &factory.Config{Configuration: &factory.Configuration{
		T3592: &factory.TimerValue{Enable: false},
	}}
	t.Cleanup(func() { self.UserPlaneInformation, factory.SmfConfig = previousUPI, previousConfig })
	sm.SMPolicyID = chargingReleaseOrderPolicyID
	var calls, deletes atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/delete") {
			deletes.Add(1)
		} else if calls.Add(1) == 1 {
			chargingReleaseOrderFailure(t, w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	sm.SelectedPCFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
	releaseRequest, err := nas_message.Marshal(&nas_message.PDUSessRelReq{
		PDUSessId: 1, PTI: 1,
	}, nil, nas_message.SecHdrTypePlainNas)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	p.HandlePDUSessionSMContextUpdate(ctx, models.UpdateSmContextRequestBody{
		JsonData:              &models.Smf_PDUSess_SmContextUpdateData{},
		BinaryDataN1SmMessage: related("GSM_NAS", releaseRequest),
	}, sm.Ref)
	ctx.Writer.WriteHeaderNow()
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	recoverChargingNow(p)
	state, chargingErr := sm.ChargingReleaseResult()
	require.Equal(t, smf_context.ChargingReleaseSucceeded, state)
	require.NoError(t, chargingErr)
	require.Zero(t, deletes.Load(), "CHF success must not bypass UE/AMF completion")
	require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
	require.Equal(t, smf_context.InActivePending, sm.State())
	complete, err := nas_message.Marshal(&nas_message.PDUSessRelComplete{
		PDUSessId: 1, PTI: 1,
	}, nil, nas_message.SecHdrTypePlainNas)
	require.NoError(t, err)
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	p.HandlePDUSessionSMContextUpdate(ctx, models.UpdateSmContextRequestBody{
		JsonData:              &models.Smf_PDUSess_SmContextUpdateData{},
		BinaryDataN1SmMessage: related("GSM_NAS", complete),
	}, sm.Ref)
	require.Eventually(t, func() bool { return smf_context.GetSMContextByRef(sm.Ref) == nil },
		time.Second, time.Millisecond)
	require.Equal(t, int32(1), deletes.Load())
	require.Equal(t, int32(2), calls.Load(), "UE completion must reuse successful settlement")
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
