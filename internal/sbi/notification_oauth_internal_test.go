package sbi

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/internal/sbi/processor"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

type notificationTestApp struct {
	ServerSmf
	processor *processor.Processor
}

func (a *notificationTestApp) Processor() *processor.Processor { return a.processor }

func enableNotificationTestOAuth(t *testing.T) *smf_context.SMFContext {
	t.Helper()
	self := smf_context.GetSelf()
	previousID, previousURI, previousOAuth := self.NfInstanceID, self.NrfUri, self.OAuth2Required
	t.Cleanup(func() {
		self.NfInstanceID, self.NrfUri, self.OAuth2Required = previousID, previousURI, previousOAuth
	})
	self.NfInstanceID, self.OAuth2Required = uuid.NewString(), true
	return self
}

// These are Notify operations under TS 29.500 6.7.3, including the
// implicitly subscribed PCF/CHF notifications. See docs/oauth-notifications.md.
func TestIncomingNotificationsWithoutOAuthToken(t *testing.T) {
	enableNotificationTestOAuth(t)
	previousConfig, previousTEIDs := factory.SmfConfig, smf_context.TeidGenerator
	factory.SmfConfig = &factory.Config{Configuration: &factory.Configuration{}}
	smf_context.TeidGenerator = idgenerator.NewGenerator(1, math.MaxUint32)
	t.Cleanup(func() {
		factory.SmfConfig, smf_context.TeidGenerator = previousConfig, previousTEIDs
	})
	session := smf_context.NewSMContext("imsi-208930000000009", 1)
	require.NotNil(t, session)
	t.Cleanup(func() { smf_context.RemoveSMContext(session.Ref) })
	session.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000009", PduSessionId: 1,
	}
	session.SetState(smf_context.Active)
	router := newRouter(&Server{ServerSmf: &notificationTestApp{processor: &processor.Processor{}}})
	policy := &models.Pcf_SMPolCtrl_SmPolicyNotification{
		ResourceUri: "https://pcf.example/npcf-smpolicycontrol/v1/sm-policies/policy-1",
		SmPolicyDecision: &models.Pcf_SMPolCtrl_SmPolicyDecision{
			SessRules: map[string]*models.Pcf_SMPolCtrl_SessionRule{
				"notify-rule": {
					SessRuleId:   "notify-rule",
					AuthSessAmbr: &models.Ambr{Uplink: "1 Mbps", Downlink: "2 Mbps"},
					AuthDefQos: &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{
						Var5qi: 9, Arp: &models.Arp{PriorityLevel: 1},
					},
				},
			},
		},
	}
	for _, tt := range []struct {
		name string
		path string
		body any
		want int
	}{
		{"policy update", "/sm-policies/" + session.Ref + "/update", policy, http.StatusNoContent},
		{
			"policy termination", "/sm-policies/" + session.Ref + "/terminate",
			map[string]string{"resourceUri": policy.ResourceUri, "cause": "UNSPECIFIED"},
			http.StatusNotImplemented,
		},
		{"charging", "/notify_" + session.Ref, &models.Chf_ConvCharging_ChargingNotifyRequest{
			NotificationType: models.Chf_ConvCharging_NotificationType_REAUTHORIZATION,
		}, http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.body)
			require.NoError(t, err)
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				factory.SmfCallbackUriPrefix+tt.path, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tt.want, response.Code, response.Body.String())
			if tt.name == "policy update" {
				require.Contains(t, session.SessionRules, "notify-rule")
				require.Equal(t, "2 Mbps", session.SessionRules["notify-rule"].AuthSessAmbr.Downlink)
			}
		})
	}
}

func TestServiceRequestsStillRequireOAuthToken(t *testing.T) {
	enableNotificationTestOAuth(t)
	previousConfig := factory.SmfConfig
	factory.SmfConfig = &factory.Config{Configuration: &factory.Configuration{
		ServiceNameList: []string{"nsmf-pdusession", "nsmf-event-exposure"},
	}}
	t.Cleanup(func() { factory.SmfConfig = previousConfig })
	router := newRouter(&Server{})
	for _, tt := range []struct{ method, path string }{
		{http.MethodPost, factory.SmfPdusessionResUriPrefix + "/sm-contexts"},
		{http.MethodPost, factory.SmfPdusessionResUriPrefix + "/pdu-sessions/ref/retrieve"},
		{http.MethodPost, factory.SmfEventExposureResUriPrefix + "/subscriptions"},
		{http.MethodDelete, factory.SmfEventExposureResUriPrefix + "/subscriptions/sub-id"},
	} {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		})
	}
}

func TestOutgoingNotificationsWithoutOAuthToken(t *testing.T) {
	for _, operation := range []string{"SM context status", "event exposure"} {
		for _, nrfStatus := range []int{http.StatusOK, http.StatusServiceUnavailable} {
			t.Run(operation+"/"+http.StatusText(nrfStatus), func(t *testing.T) {
				self := enableNotificationTestOAuth(t)
				var tokenRequests atomic.Int32
				nrf := notificationHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					tokenRequests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(nrfStatus)
					if nrfStatus == http.StatusOK {
						_, err := w.Write([]byte(`{"access_token":"notify-token","token_type":"Bearer","expires_in":60}`))
						assert.NoError(t, err)
					}
				}))
				self.NrfUri = nrf.URL
				type deliveredNotification struct {
					method, path, authorization string
					body                        map[string]any
				}
				deliveries := make(chan deliveredNotification, 1)
				receiver := notificationHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					deliveries <- deliveredNotification{r.Method, r.URL.Path, r.Header.Get("Authorization"), body}
					w.WriteHeader(http.StatusNoContent)
				}))
				uri := receiver.URL + "/subscriber/notifications"
				if operation == "SM context status" {
					client, err := consumer.NewConsumer(nil)
					require.NoError(t, err)
					problem, err := client.SendSMContextStatusNotification(uri)
					assert.NoError(t, err)
					assert.Nil(t, problem)
				} else {
					now := time.Now().UTC()
					processor.SendUpPathChgEventExposureNotification(uri, &models.Smf_EvtExpos_NsmfEventExposureNotification{
						NotifId: "subscription-1",
						EventNotifs: []models.Smf_EvtExpos_EventNotification{{
							Event:     models.Smf_EvtExpos_SmfEvent_UP_PATH_CH,
							TimeStamp: &now, Supi: "imsi-208930000000009", PduSeId: 1,
							SourceDnai: "source", TargetDnai: "target", DnaiChgType: models.DnaiChangeType_EARLY,
						}},
					})
				}
				assert.Zero(t, tokenRequests.Load(), "Notify must not request an NRF token")
				select {
				case notification := <-deliveries:
					assert.Equal(t, http.MethodPost, notification.method)
					assert.Equal(t, "/subscriber/notifications", notification.path)
					assert.Empty(t, notification.authorization, "Notify must not send an OAuth token")
					if operation == "SM context status" {
						assert.Equal(t, map[string]any{"resourceStatus": "RELEASED"}, notification.body["statusInfo"])
					} else {
						assert.Equal(t, "subscription-1", notification.body["notifId"])
						events, ok := notification.body["eventNotifs"].([]any)
						require.True(t, ok)
						require.Len(t, events, 1)
						event, ok := events[0].(map[string]any)
						require.True(t, ok)
						assert.Equal(t, "UP_PATH_CH", event["event"])
						assert.Equal(t, "target", event["targetDnai"])
					}
				default:
					t.Error("Notify was not delivered")
				}
			})
		}
	}
}

func notificationHTTP2Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// A future callback is protected unless explicitly classified as Notify.
func TestCallbackAuthorizationDefaultsToRequired(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		self := &smf_context.SMFContext{OAuth2Required: enabled}
		router := gin.New()
		applyCallbackRoutes(router.Group(factory.SmfCallbackUriPrefix), []Route{{
			Name: "RequestResponseCallback", Method: http.MethodPost, Pattern: "/request-response",
			APIFunc: func(c *gin.Context) { c.Status(http.StatusNoContent) },
		}}, self)
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			factory.SmfCallbackUriPrefix+"/request-response", nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		want := http.StatusNoContent
		if enabled {
			want = http.StatusUnauthorized
		}
		require.Equal(t, want, response.Code, response.Body.String())
	}
}
