package processor

import (
	"encoding/json"
	"math"
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
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

const chargingReleaseOrderPolicyID = "policy-1226"

// PCF and CHF are local HTTP/2 stubs. The cached FINAL report represents
// an already deleted PFCP session whose charging data must survive settlement.
type chargingReleaseOrderApp struct {
	ctx      *smf_context.SMFContext
	consumer *consumer.Consumer
}

func (*chargingReleaseOrderApp) SetLogEnable(bool)                  {}
func (*chargingReleaseOrderApp) SetLogLevel(string)                 {}
func (*chargingReleaseOrderApp) SetReportCaller(bool)               {}
func (*chargingReleaseOrderApp) Start()                             {}
func (*chargingReleaseOrderApp) Terminate()                         {}
func (*chargingReleaseOrderApp) Config() *factory.Config            { return nil }
func (a *chargingReleaseOrderApp) Context() *smf_context.SMFContext { return a.ctx }
func (a *chargingReleaseOrderApp) Consumer() *consumer.Consumer     { return a.consumer }

func chargingReleaseOrderSession(t *testing.T) (*Processor, *smf_context.SMContext) {
	t.Helper()
	self := smf_context.GetSelf()
	oldOAuth := self.OAuth2Required
	oldTEID := smf_context.TeidGenerator
	self.OAuth2Required = false
	smf_context.TeidGenerator = idgenerator.NewGenerator(1, math.MaxUint32)
	t.Cleanup(func() {
		self.OAuth2Required = oldOAuth
		smf_context.TeidGenerator = oldTEID
	})
	app := &chargingReleaseOrderApp{ctx: self}
	var err error
	app.consumer, err = consumer.NewConsumer(app)
	require.NoError(t, err)
	p, err := NewProcessor(app)
	require.NoError(t, err)
	sm := smf_context.NewSMContext("imsi-208930000001226", 1)
	require.NotNil(t, sm)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000001226", PduSessionId: 1, Dnn: "internet",
		SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
	}
	sm.SetState(smf_context.Active)
	sm.PFCPReleaseDone = true
	sm.ChargingDataRef = "charging-1226"
	sm.ChargingInfo[1] = &smf_context.ChargingInfo{
		RatingGroup: 10, UpfId: "upf-a",
		ChargingMethod: models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING,
	}
	sm.UrrReports = []smf_context.UsageReport{{
		UrrId: 1, UpfId: "upf-a", TotalVolume: 40,
		ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL,
	}}
	return p, sm
}

func chargingReleaseOrderServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(handler)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	s.Config.Protocols = protocols
	s.Start()
	t.Cleanup(s.Close)
	return s
}

func chargingReleaseOrderProfile(name models.Nrf_NFMgmt_ServiceName, root string) models.Nrf_NFDisc_NFProfile {
	return models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{{
		ServiceName: name, ApiPrefix: root,
	}}}
}

func chargingReleaseOrderFailure(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusInternalServerError)
	body := []byte(`{"status":500,"cause":"SYSTEM_FAILURE","detail":"injected settlement failure"}`)
	if _, err := w.Write(body); err != nil {
		t.Errorf("write CHF error response: %v", err)
	}
}

func TestChargingReleasePrecedesPolicyDeletion(t *testing.T) {
	for _, path := range []string{"AMF release", "local release"} {
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
			if path == "AMF release" {
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
	for _, path := range []string{"AMF release", "local release", "UE release", "common cleanup"} {
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
				case "AMF release":
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
				case "common cleanup":
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
