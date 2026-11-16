//nolint:testpackage // Package-local access is required to verify URI validation and client policy.
package consumer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/upf/EvtExpos"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

type nupfTestApp struct {
	smfContext *smf_context.SMFContext
}

func (a *nupfTestApp) SetLogEnable(bool)                {}
func (a *nupfTestApp) SetLogLevel(string)               {}
func (a *nupfTestApp) SetReportCaller(bool)             {}
func (a *nupfTestApp) Start()                           {}
func (a *nupfTestApp) Terminate()                       {}
func (a *nupfTestApp) Context() *smf_context.SMFContext { return a.smfContext }
func (a *nupfTestApp) Config() *factory.Config          { return nil }

func TestNupfEventExposureCreateAndDeleteWireContract(t *testing.T) {
	var createCalls atomic.Int32
	var deleteCalls atomic.Int32
	var received models.Upf_EvtExpos_CreateEventSubscription

	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			createCalls.Add(1)
			require.Equal(t, "/nupf-ee/v1/ee-subscriptions", request.URL.Path)
			require.NoError(t, json.NewDecoder(request.Body).Decode(&received))
			location := "http://" + request.Host + "/nupf-ee/v1/ee-subscriptions/downstream-1"
			responseWriter.Header().Set("Content-Type", "application/json")
			responseWriter.Header().Set("Location", location)
			responseWriter.WriteHeader(http.StatusCreated)
			require.NoError(t, json.NewEncoder(responseWriter).Encode(models.Upf_EvtExpos_CreatedEventSubscription{
				Subscription:   &models.Upf_EvtExpos_UpfEventSubscription{},
				SubscriptionId: location,
			}))
		case http.MethodDelete:
			deleteCalls.Add(1)
			require.Equal(t, "/nupf-ee/v1/ee-subscriptions/downstream-1", request.URL.Path)
			responseWriter.WriteHeader(http.StatusNoContent)
		default:
			responseWriter.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	consumer := newNupfTestConsumer(t, &smf_context.SMFContext{})
	setNupfTestHTTPClient(consumer, server.URL, server.Client())
	downstreamID, err := consumer.CreateNupfEventSubscription(context.Background(), server.URL, testNupfRequest())
	require.NoError(t, err)
	require.Equal(t, "downstream-1", downstreamID)
	require.Equal(t, int32(1), createCalls.Load())
	require.NotNil(t, received.Subscription)
	require.Equal(t, "https://consumer.example.com/notify", received.Subscription.EventNotifyUri)
	require.Equal(t, "correlation-1", received.Subscription.NotifyCorrelationId)

	err = consumer.DeleteNupfEventSubscription(context.Background(), server.URL, downstreamID)
	require.NoError(t, err)
	require.Equal(t, int32(1), deleteCalls.Load())
}

func TestNupfEventExposurePreservesEscapedResourceCoordinates(t *testing.T) {
	const (
		deploymentPrefix = "/tenant%2Fblue"
		downstreamID     = "sub%2Fpart%3Fquery%23fragment"
	)
	collectionPath := deploymentPrefix + "/nupf-ee/v1/ee-subscriptions"

	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			require.Equal(t, collectionPath, request.RequestURI)
			location := "http://" + request.Host + collectionPath + "/" + downstreamID
			responseWriter.Header().Set("Content-Type", "application/json")
			responseWriter.Header().Set("Location", location)
			responseWriter.WriteHeader(http.StatusCreated)
			require.NoError(t, json.NewEncoder(responseWriter).Encode(models.Upf_EvtExpos_CreatedEventSubscription{
				Subscription:   &models.Upf_EvtExpos_UpfEventSubscription{},
				SubscriptionId: location,
			}))
		case http.MethodDelete:
			require.Equal(t, collectionPath+"/"+downstreamID, request.RequestURI)
			require.Empty(t, request.URL.RawQuery)
			require.Empty(t, request.URL.Fragment)
			responseWriter.WriteHeader(http.StatusNoContent)
		default:
			responseWriter.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	apiRoot := server.URL + deploymentPrefix
	consumer := newNupfTestConsumer(t, &smf_context.SMFContext{})
	setNupfTestHTTPClient(consumer, apiRoot, server.Client())
	actualID, err := consumer.CreateNupfEventSubscription(context.Background(), apiRoot, testNupfRequest())
	require.NoError(t, err)
	require.Equal(t, downstreamID, actualID)
	require.NoError(t, consumer.DeleteNupfEventSubscription(context.Background(), apiRoot, actualID))
}

func TestNupfEventExposureRejectsRedirectWithoutReplay(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirectTargetCalls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				redirectTargetCalls.Add(1)
			}))
			t.Cleanup(target.Close)

			var originCalls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				originCalls.Add(1)
				responseWriter.Header().Set("Content-Type", "application/json")
				responseWriter.Header().Set("Location", target.URL)
				responseWriter.WriteHeader(status)
				_, writeErr := responseWriter.Write([]byte("{}"))
				require.NoError(t, writeErr)
			}))
			t.Cleanup(origin.Close)

			consumer := newNupfTestConsumer(t, &smf_context.SMFContext{})
			setNupfTestHTTPClient(consumer, origin.URL, origin.Client())
			_, createErr := consumer.CreateNupfEventSubscription(context.Background(), origin.URL, testNupfRequest())
			assertNupfFailure(t, createErr, status, false)
			deleteErr := consumer.DeleteNupfEventSubscription(context.Background(), origin.URL, "downstream-1")
			assertNupfFailure(t, deleteErr, status, false)
			require.Equal(t, int32(2), originCalls.Load())
			require.Zero(t, redirectTargetCalls.Load())
		})
	}
}

func TestValidateNupfCreatedResource(t *testing.T) {
	testCases := []struct {
		name     string
		apiRoot  string
		resource string
		wantID   string
	}{
		{
			name:     "root",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/sub-1",
			wantID:   "sub-1",
		},
		{
			name:     "deployment prefix",
			apiRoot:  "https://upf.example.com/deployment",
			resource: "https://upf.example.com/deployment/nupf-ee/v1/ee-subscriptions/sub-2",
			wantID:   "sub-2",
		},
		{
			name:     "escaped deployment prefix and reserved id characters",
			apiRoot:  "https://upf.example.com/tenant%2Fblue",
			resource: "https://upf.example.com/tenant%2Fblue/nupf-ee/v1/ee-subscriptions/sub%2Fpart%3Fquery%23fragment",
			wantID:   "sub%2Fpart%3Fquery%23fragment",
		},
		{name: "relative", apiRoot: "https://upf.example.com", resource: "/nupf-ee/v1/ee-subscriptions/sub"},
		{
			name:     "cross origin",
			apiRoot:  "https://upf.example.com",
			resource: "https://other.example.com/nupf-ee/v1/ee-subscriptions/sub",
		},
		{
			name:     "wrong prefix",
			apiRoot:  "https://upf.example.com/deployment",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/sub",
		},
		{
			name:     "wrong service",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/not-nupf-ee/v1/ee-subscriptions/sub",
		},
		{
			name:     "wrong version",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v2/ee-subscriptions/sub",
		},
		{
			name:     "empty id",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/",
		},
		{
			name:     "extra segment",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/sub/extra",
		},
		{
			name:     "query",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/sub?x=1",
		},
		{
			name:     "fragment",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/sub#part",
		},
		{
			name:     "userinfo",
			apiRoot:  "https://upf.example.com",
			resource: "https://user@upf.example.com/nupf-ee/v1/ee-subscriptions/sub",
		},
		{
			name:     "aliased deployment prefix",
			apiRoot:  "https://upf.example.com/deployment",
			resource: "https://upf.example.com/%64eployment/nupf-ee/v1/ee-subscriptions/sub",
		},
		{
			name:     "different escaped prefix spelling",
			apiRoot:  "https://upf.example.com/tenant%2Fblue",
			resource: "https://upf.example.com/tenant%2fblue/nupf-ee/v1/ee-subscriptions/sub",
		},
		{
			name:     "aliased service path",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf%2Dee/v1/ee-subscriptions/sub",
		},
		{
			name:     "aliased collection path",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/%65e-subscriptions/sub",
		},
		{
			name:     "normalized dot path",
			apiRoot:  "https://upf.example.com/deployment",
			resource: "https://upf.example.com/deployment/./nupf-ee/v1/ee-subscriptions/sub",
		},
		{
			name:     "malformed escape",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/sub%ZZ",
		},
		{
			name:     "raw space",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/sub id",
		},
		{
			name:     "raw non-ASCII",
			apiRoot:  "https://upf.example.com",
			resource: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/訂閱",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			downstreamID, err := validateNupfCreatedResource(tc.apiRoot, tc.resource)
			if tc.wantID != "" {
				require.NoError(t, err)
				require.Equal(t, tc.wantID, downstreamID)
				return
			}
			require.Error(t, err)
			require.Empty(t, downstreamID)
		})
	}
}

func TestValidateNupfCreateResponseRequiresCompleteResource(t *testing.T) {
	const resource = "https://upf.example.com/nupf-ee/v1/ee-subscriptions/sub-1"
	testCases := []struct {
		name     string
		response *EvtExpos.CreateSubscriptionResponse
	}{
		{name: "nil generated response"},
		{
			name:     "missing generated body",
			response: &EvtExpos.CreateSubscriptionResponse{Location: resource},
		},
		{
			name: "missing mandatory subscription",
			response: &EvtExpos.CreateSubscriptionResponse{
				Location: resource,
				Upf_EvtExpos_CreatedEventSubscription: &models.Upf_EvtExpos_CreatedEventSubscription{
					SubscriptionId: resource,
				},
			},
		},
		{
			name: "empty body subscription id",
			response: &EvtExpos.CreateSubscriptionResponse{
				Location: resource,
				Upf_EvtExpos_CreatedEventSubscription: &models.Upf_EvtExpos_CreatedEventSubscription{
					Subscription: &models.Upf_EvtExpos_UpfEventSubscription{},
				},
			},
		},
		{
			name: "empty location",
			response: &EvtExpos.CreateSubscriptionResponse{
				Upf_EvtExpos_CreatedEventSubscription: &models.Upf_EvtExpos_CreatedEventSubscription{
					Subscription:   &models.Upf_EvtExpos_UpfEventSubscription{},
					SubscriptionId: resource,
				},
			},
		},
		{
			name: "body and location mismatch",
			response: &EvtExpos.CreateSubscriptionResponse{
				Location: resource,
				Upf_EvtExpos_CreatedEventSubscription: &models.Upf_EvtExpos_CreatedEventSubscription{
					Subscription:   &models.Upf_EvtExpos_UpfEventSubscription{},
					SubscriptionId: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/other",
				},
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			downstreamID, err := validateNupfCreateResponse("https://upf.example.com", test.response)
			require.Error(t, err)
			require.Empty(t, downstreamID)
		})
	}
}

func TestNupfEventExposureStatusAndTimeoutClassification(t *testing.T) {
	for _, status := range []int{
		http.StatusAccepted,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusGatewayTimeout,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				responseWriter.Header().Set("Content-Type", "application/json")
				responseWriter.WriteHeader(status)
				_, writeErr := responseWriter.Write([]byte("{}"))
				require.NoError(t, writeErr)
			}))
			t.Cleanup(server.Close)

			consumer := newNupfTestConsumer(t, &smf_context.SMFContext{})
			setNupfTestHTTPClient(consumer, server.URL, server.Client())
			_, err := consumer.CreateNupfEventSubscription(context.Background(), server.URL, testNupfRequest())
			assertNupfFailure(t, err, status, false)
		})
	}

	timeoutServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	t.Cleanup(timeoutServer.Close)
	consumer := newNupfTestConsumer(t, &smf_context.SMFContext{})
	setNupfTestHTTPClient(consumer, timeoutServer.URL, timeoutServer.Client())
	requestContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := consumer.CreateNupfEventSubscription(requestContext, timeoutServer.URL, testNupfRequest())
	assertNupfFailure(t, err, 0, true)
}

func TestNupfEventExposureOAuthFailureSendsNoDownstreamRequest(t *testing.T) {
	var downstreamCalls atomic.Int32
	downstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		downstreamCalls.Add(1)
	}))
	t.Cleanup(downstream.Close)

	smfContext := &smf_context.SMFContext{
		OAuth2Required: true,
		NrfUri:         "://invalid",
		NfInstanceID:   "00000000-0000-4000-8000-000000000001",
	}
	consumer := newNupfTestConsumer(t, smfContext)
	_, err := consumer.CreateNupfEventSubscription(context.Background(), downstream.URL, testNupfRequest())
	assertNupfFailure(t, err, 0, false)
	require.Zero(t, downstreamCalls.Load())
	require.NotContains(t, err.Error(), downstream.URL)
}

func newNupfTestConsumer(t *testing.T, smfContext *smf_context.SMFContext) *Consumer {
	t.Helper()
	consumer, err := NewConsumer(&nupfTestApp{smfContext: smfContext})
	require.NoError(t, err)
	return consumer
}

func setNupfTestHTTPClient(consumer *Consumer, apiRoot string, httpClient *http.Client) {
	configuration := EvtExpos.NewConfiguration()
	configuration.SetBasePath(apiRoot)
	configuration.SetHTTPClient(httpClient)
	configuration.SetRedirectPolicy(openapi.RejectRedirects)
	consumer.nupfEventExposureService.clients[apiRoot] = EvtExpos.NewAPIClient(configuration)
}

func testNupfRequest() *models.Upf_EvtExpos_CreateEventSubscription {
	return &models.Upf_EvtExpos_CreateEventSubscription{
		Subscription: &models.Upf_EvtExpos_UpfEventSubscription{
			EventNotifyUri:      "https://consumer.example.com/notify",
			NotifyCorrelationId: "correlation-1",
		},
	}
}

func assertNupfFailure(t *testing.T, err error, statusCode int, timedOut bool) {
	t.Helper()
	require.Error(t, err)
	classified, ok := err.(*NupfEventExposureError)
	require.True(t, ok)
	require.Equal(t, statusCode, classified.StatusCode)
	require.Equal(t, timedOut, classified.TimedOut)
	require.Equal(t, nupfEventExposureFailure, err.Error())
}
