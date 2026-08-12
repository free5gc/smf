package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	NupfEventExposure "github.com/free5gc/openapi/upf/EventExposure"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

func TestValidateNupfCreateSuccessLocationInvariants(t *testing.T) {
	target := smf_context.EventExposureTarget{
		APIroot:        "https://upf.example.com/api",
		ServiceBaseURL: "https://upf.example.com/api/nupf-ee/v1",
	}
	requestURI := "https://upf.example.com/api/nupf-ee/v1/ee-subscriptions"

	tests := []struct {
		name     string
		location string
		wantErr  bool
	}{
		{
			name:     "relative",
			location: "ee-subscriptions/upf-sub-1",
		},
		{
			name:     "absolute",
			location: "https://upf.example.com/api/nupf-ee/v1/ee-subscriptions/upf-sub-1",
		},
		{
			name:     "cross origin rejected",
			location: "https://other.example.com/api/nupf-ee/v1/ee-subscriptions/upf-sub-1",
			wantErr:  true,
		},
		{
			name:     "query rejected",
			location: "https://upf.example.com/api/nupf-ee/v1/ee-subscriptions/upf-sub-1?x=1",
			wantErr:  true,
		},
		{
			name:     "id mismatch rejected",
			location: "https://upf.example.com/api/nupf-ee/v1/ee-subscriptions/other",
			wantErr:  true,
		},
		{
			name:     "subscription id with parent segment rejected",
			location: "https://upf.example.com/api/nupf-ee/v1/x",
			wantErr:  true,
		},
		{
			name:     "subscription id with slash rejected",
			location: "https://upf.example.com/api/nupf-ee/v1/ee-subscriptions/a/b",
			wantErr:  true,
		},
		{
			name:     "subscription id with encoded slash rejected",
			location: "https://upf.example.com/api/nupf-ee/v1/ee-subscriptions/a%2Fb",
			wantErr:  true,
		},
		{
			name:     "path cleaning does not bypass validation",
			location: "https://upf.example.com/api/nupf-ee/v1/ee-subscriptions/a/../upf-sub-1",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subscriptionID := "upf-sub-1"
			switch tt.name {
			case "subscription id with parent segment rejected":
				subscriptionID = "../x"
			case "subscription id with slash rejected":
				subscriptionID = "a/b"
			case "subscription id with encoded slash rejected":
				subscriptionID = "a%2Fb"
			}
			_, err := validateNupfCreateSuccess(target, requestURI, &NupfEventExposure.CreateSubscriptionResponse{
				Location: tt.location,
				UpfCreatedEventSubscription: models.UpfCreatedEventSubscription{
					SubscriptionId: subscriptionID,
				},
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error mismatch: got %v wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNupfEventExposureCreateSendsExactRequestBody(t *testing.T) {
	var capturedPath string
	var capturedBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if unmarshalErr := json.Unmarshal(body, &capturedBody); unmarshalErr != nil {
			t.Fatalf("Unmarshal request body failed: %v", unmarshalErr)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", serverLocation(r, "upf-sub-1"))
		w.WriteHeader(http.StatusCreated)
		if _, writeErr := w.Write([]byte(`{"subscriptionId":"upf-sub-1"}`)); writeErr != nil {
			t.Fatalf("Write failed: %v", writeErr)
		}
	}))
	defer server.Close()

	service := newTestNupfEventExposureService(false, "")
	installTestEventExposureClient(service, server.URL, server.Client())
	target := testEventExposureTarget(server.URL)
	result, err := service.CreateSubscription(context.Background(), target, testNupfCreateRequest())
	if err != nil {
		t.Fatalf("CreateSubscription failed: %v", err)
	}
	if result.SubscriptionID != "upf-sub-1" {
		t.Fatalf("SubscriptionID mismatch: %q", result.SubscriptionID)
	}
	if capturedPath != "/nupf-ee/v1/ee-subscriptions" {
		t.Fatalf("path mismatch: %q", capturedPath)
	}
	subscription, ok := capturedBody["subscription"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing subscription wrapper: %#v", capturedBody)
	}
	if subscription["eventNotifyUri"] != "https://nwdaf.example.com/upf" ||
		subscription["notifyCorrelationId"] != "correlation-1" ||
		subscription["nfId"] != "smf-instance" {
		t.Fatalf("unexpected subscription body: %#v", subscription)
	}
	if _, ipOK := subscription["ueIpAddress"].(map[string]interface{}); !ipOK {
		t.Fatalf("missing IpAddr object: %#v", subscription["ueIpAddress"])
	}
	mode, ok := subscription["eventReportingMode"].(map[string]interface{})
	if !ok || mode["trigger"] != "PERIODIC" || mode["repPeriod"].(float64) != 10 {
		t.Fatalf("unexpected reporting mode: %#v", subscription["eventReportingMode"])
	}
}

func TestNupfEventExposureCreateAndDeleteRejectRedirectWithoutReplay(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var targetRequests int
			redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				targetRequests++
			}))
			defer redirectTarget.Close()

			var createRequests int
			var createBody []byte
			createServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				createRequests++
				var err error
				createBody, err = io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("ReadAll failed: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", redirectTarget.URL+"/nupf-ee/v1/ee-subscriptions/upf-sub-1")
				w.WriteHeader(status)
				if _, writeErr := w.Write([]byte(`{"cause":"TEMPORARY_REDIRECT"}`)); writeErr != nil {
					t.Fatalf("Write failed: %v", writeErr)
				}
			}))
			defer createServer.Close()

			service := newTestNupfEventExposureService(false, "")
			installTestEventExposureClient(service, createServer.URL, createServer.Client())
			_, err := service.CreateSubscription(
				context.Background(), testEventExposureTarget(createServer.URL), testNupfCreateRequest())
			assertNupfError(t, err, NupfEventExposureErrorRedirect, "create", status)
			if createRequests != 1 {
				t.Fatalf("Create requests mismatch: %d", createRequests)
			}
			if len(createBody) == 0 {
				t.Fatal("expected original Create request body")
			}
			if targetRequests != 0 {
				t.Fatalf("redirect target received %d Create requests", targetRequests)
			}

			var deleteRequests int
			deleteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				deleteRequests++
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", redirectTarget.URL+"/nupf-ee/v1/ee-subscriptions/upf-sub-1")
				w.WriteHeader(status)
				if _, writeErr := w.Write([]byte(`{"cause":"TEMPORARY_REDIRECT"}`)); writeErr != nil {
					t.Fatalf("Write failed: %v", writeErr)
				}
			}))
			defer deleteServer.Close()

			service = newTestNupfEventExposureService(false, "")
			installTestEventExposureClient(service, deleteServer.URL, deleteServer.Client())
			err = service.DeleteSubscription(
				context.Background(), testEventExposureTarget(deleteServer.URL), "upf-sub-1")
			assertNupfError(t, err, NupfEventExposureErrorRedirect, "delete", status)
			if deleteRequests != 1 {
				t.Fatalf("Delete requests mismatch: %d", deleteRequests)
			}
			if targetRequests != 0 {
				t.Fatalf("redirect target received %d total requests", targetRequests)
			}
		})
	}
}

func TestNupfEventExposureOAuthDisabledUsesCallerContext(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := newTestNupfEventExposureService(false, "")
	installTestEventExposureClient(service, server.URL, server.Client())
	_, err := service.CreateSubscription(ctx, testEventExposureTarget(server.URL), testNupfCreateRequest())
	assertNupfError(t, err, NupfEventExposureErrorTransport, "create", 0)
	if requests != 0 {
		t.Fatalf("canceled caller context still sent %d requests", requests)
	}
}

func TestNupfEventExposureOAuthTokenFailureSendsNoNupfRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	service := newTestNupfEventExposureService(true, "://invalid-nrf")
	_, err := service.CreateSubscription(
		context.Background(), testEventExposureTarget(server.URL), testNupfCreateRequest())
	assertNupfError(t, err, NupfEventExposureErrorToken, "create", 0)
	if requests != 0 {
		t.Fatalf("token failure still sent %d Nupf requests", requests)
	}

	err = service.DeleteSubscription(context.Background(), testEventExposureTarget(server.URL), "upf-sub-1")
	assertNupfError(t, err, NupfEventExposureErrorToken, "delete", 0)
	if requests != 0 {
		t.Fatalf("token failure still sent %d Nupf requests after Delete", requests)
	}
}

func TestNupfEventExposureDefaultClientSetupUsesBasePathMetricsAndNoFollow(t *testing.T) {
	var requestCount int
	var capturedPath string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "https://redirect.example.com/nupf-ee/v1/ee-subscriptions/upf-sub-1")
		w.WriteHeader(http.StatusTemporaryRedirect)
		if _, writeErr := w.Write([]byte(`{"cause":"TEMPORARY_REDIRECT"}`)); writeErr != nil {
			t.Fatalf("Write failed: %v", writeErr)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	service := newTestNupfEventExposureService(false, "")
	_, err := service.CreateSubscription(
		context.Background(), testEventExposureTarget(server.URL), testNupfCreateRequest())
	assertNupfError(t, err, NupfEventExposureErrorRedirect, "create", http.StatusTemporaryRedirect)

	if requestCount != 1 {
		t.Fatalf("expected exactly one request without redirect replay, got %d", requestCount)
	}
	if capturedPath != "/nupf-ee/v1/ee-subscriptions" {
		t.Fatalf("path mismatch: %q", capturedPath)
	}
	if service.createRequestURI(server.URL) != server.URL+"/nupf-ee/v1/ee-subscriptions" {
		t.Fatalf("request URI cache mismatch: %q", service.createRequestURI(server.URL))
	}

	client := service.EventExposureClients[server.URL]
	if client == nil {
		t.Fatal("default setup did not cache generated client")
	}
	assertDefaultClientConfiguration(t, client)
}

func newTestNupfEventExposureService(oauth bool, nrfURI string) *nupfEventExposureService {
	consumer := &Consumer{App: fakeConsumerApp{
		context: &smf_context.SMFContext{
			NfInstanceID:   "smf-instance",
			OAuth2Required: oauth,
			NrfUri:         nrfURI,
		},
	}}
	return &nupfEventExposureService{
		consumer:                    consumer,
		EventExposureClients:        make(map[string]*NupfEventExposure.APIClient),
		EventExposureCreateRequests: make(map[string]string),
	}
}

func installTestEventExposureClient(
	service *nupfEventExposureService,
	apiRoot string,
	httpClient *http.Client,
) {
	configuration := NupfEventExposure.NewConfiguration()
	configuration.SetBasePath(apiRoot)
	configuration.SetRedirectPolicy(openapi.RejectRedirects)
	configuration.SetHTTPClient(httpClient)
	service.EventExposureClients[apiRoot] = NupfEventExposure.NewAPIClient(configuration)
	service.EventExposureCreateRequests[apiRoot] = strings.TrimRight(
		configuration.BasePath(), "/") + "/ee-subscriptions"
}

func assertDefaultClientConfiguration(t *testing.T, client *NupfEventExposure.APIClient) {
	t.Helper()
	cfg := reflect.ValueOf(client).Elem().FieldByName("cfg").Elem()
	if cfg.FieldByName("MetricsHook").IsNil() {
		t.Fatal("default setup did not configure metrics hook")
	}
	if cfg.FieldByName("redirectPolicy").IsNil() {
		t.Fatal("default setup did not configure redirect policy")
	}
	if !cfg.FieldByName("httpClient").IsNil() {
		t.Fatal("default setup unexpectedly configured an explicit HTTP client")
	}
}

func testEventExposureTarget(apiRoot string) smf_context.EventExposureTarget {
	return smf_context.EventExposureTarget{
		APIroot:        apiRoot,
		ServiceBaseURL: apiRoot + "/nupf-ee/v1",
	}
}

func testNupfCreateRequest() models.UpfCreateEventSubscription {
	return models.UpfCreateEventSubscription{
		Subscription: models.UpfEventSubscription{
			EventNotifyUri:      "https://nwdaf.example.com/upf",
			NotifyCorrelationId: "correlation-1",
			NfId:                "smf-instance",
			UeIpAddress:         &models.IpAddr{Ipv4Addr: "192.0.2.1"},
			EventList: []models.UpfEvent{
				{
					Type:                     models.UpfEventType_USER_DATA_USAGE_MEASURES,
					MeasurementTypes:         []models.UpfMeasurementType{models.UpfMeasurementType_VOLUME_MEASUREMENT},
					GranularityOfMeasurement: models.UpfGranularityOfMeasurement_PER_SESSION,
				},
			},
			EventReportingMode: models.UpfEventMode{
				Trigger:   models.UpfEventTrigger_PERIODIC,
				RepPeriod: 10,
			},
		},
	}
}

func assertNupfError(
	t *testing.T,
	err error,
	wantKind NupfEventExposureErrorKind,
	wantOperation string,
	wantStatus int,
) {
	t.Helper()
	var nupfErr *NupfEventExposureError
	if !errors.As(err, &nupfErr) {
		t.Fatalf("expected NupfEventExposureError, got %T %v", err, err)
	}
	if nupfErr.Kind != wantKind || nupfErr.Operation != wantOperation || nupfErr.StatusCode != wantStatus {
		t.Fatalf("unexpected Nupf error: %+v", nupfErr)
	}
}

func serverLocation(r *http.Request, subscriptionID string) string {
	return "http://" + r.Host + "/nupf-ee/v1/ee-subscriptions/" + subscriptionID
}

type fakeConsumerApp struct {
	context *smf_context.SMFContext
}

func (f fakeConsumerApp) SetLogEnable(bool)                {}
func (f fakeConsumerApp) SetLogLevel(string)               {}
func (f fakeConsumerApp) SetReportCaller(bool)             {}
func (f fakeConsumerApp) Start()                           {}
func (f fakeConsumerApp) Terminate()                       {}
func (f fakeConsumerApp) Context() *smf_context.SMFContext { return f.context }
func (f fakeConsumerApp) Config() *factory.Config          { return nil }
