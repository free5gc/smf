//nolint:testpackage // Tests exercise package-private validation and mapping boundaries.
package processor

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"path"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

const (
	testEventExposureSupi         = "imsi-001010000000001"
	testEventExposureConsumerID   = "ad85c869-8895-4ef1-8a8b-19b29f12f31d"
	testEventExposureSMFID        = "c4f9b38e-94b1-4ba4-a05f-9f829417e68b"
	testEventExposureNotification = "https://consumer.example.com/notify"
)

type eventExposureProcessorTestApp struct {
	ctx      *smf_context.SMFContext
	consumer *consumer.Consumer
}

func (*eventExposureProcessorTestApp) SetLogEnable(bool)       {}
func (*eventExposureProcessorTestApp) SetLogLevel(string)      {}
func (*eventExposureProcessorTestApp) SetReportCaller(bool)    {}
func (*eventExposureProcessorTestApp) Start()                  {}
func (*eventExposureProcessorTestApp) Terminate()              {}
func (*eventExposureProcessorTestApp) Config() *factory.Config { return nil }

func (a *eventExposureProcessorTestApp) Context() *smf_context.SMFContext {
	return a.ctx
}

func (a *eventExposureProcessorTestApp) Consumer() *consumer.Consumer {
	return a.consumer
}

func newEventExposureProcessor(t *testing.T) *Processor {
	t.Helper()
	app := &eventExposureProcessorTestApp{ctx: smf_context.GetSelf()}
	var err error
	app.consumer, err = consumer.NewConsumer(app)
	require.NoError(t, err)
	processor, err := NewProcessor(app)
	require.NoError(t, err)
	return processor
}

func validEventExposureRequestForTest() *models.Smf_EvtExpos_NsmfEventExposure {
	return &models.Smf_EvtExpos_NsmfEventExposure{
		Supi:              testEventExposureSupi,
		PduSeId:           1,
		Dnn:               "internet",
		Snssai:            &models.Snssai{Sst: 1, Sd: "abcdef"},
		NfId:              testEventExposureConsumerID,
		NotifId:           "consumer-correlation",
		NotifUri:          testEventExposureNotification,
		NotifMethod:       models.Smf_EvtExpos_NotificationMethod_PERIODIC,
		RepPeriod:         10,
		SupportedFeatures: "02000001",
		EventSubs: []models.Smf_EvtExpos_EventSubscription{
			{
				Event: models.Smf_EvtExpos_SmfEvent_UPF_EVENT,
				UpfEvents: []models.Upf_EvtExpos_UpfEvent{
					{
						Type: models.Upf_EvtExpos_EventType_USER_DATA_USAGE_MEASURES,
						MeasurementTypes: []models.Upf_EvtExpos_MeasurementType{
							models.Upf_EvtExpos_MeasurementType_VOLUME_MEASUREMENT,
							models.Upf_EvtExpos_MeasurementType_THROUGHPUT_MEASUREMENT,
						},
						GranularityOfMeasurement: models.Upf_EvtExpos_GranularityOfMeasurement_SESSION,
					},
				},
			},
		},
	}
}

func TestValidateEventExposureRequestProfile(t *testing.T) {
	validMeasurements := [][]models.Upf_EvtExpos_MeasurementType{
		{models.Upf_EvtExpos_MeasurementType_VOLUME_MEASUREMENT},
		{models.Upf_EvtExpos_MeasurementType_THROUGHPUT_MEASUREMENT},
		{
			models.Upf_EvtExpos_MeasurementType_THROUGHPUT_MEASUREMENT,
			models.Upf_EvtExpos_MeasurementType_VOLUME_MEASUREMENT,
			models.Upf_EvtExpos_MeasurementType_THROUGHPUT_MEASUREMENT,
		},
	}
	for _, measurements := range validMeasurements {
		request := validEventExposureRequestForTest()
		request.EventSubs[0].UpfEvents[0].MeasurementTypes = measurements
		features, problem := validateEventExposureRequest(request, true)
		require.Nil(t, problem)
		require.Equal(t, smfEventExposureSupportedFeatures, features)
	}

	invalid := map[string]func(*models.Smf_EvtExpos_NsmfEventExposure){
		"missing supi":            func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.Supi = "" },
		"invalid supi":            func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.Supi = "not-a-supi" },
		"pdu session below range": func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.PduSeId = -1 },
		"pdu session above range": func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.PduSeId = 256 },
		"non-v4 nf id": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.NfId = "00000000-0000-1000-8000-000000000000"
		},
		"empty notification id": func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.NotifId = "" },
		"relative callback":     func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.NotifUri = "/notify" },
		"non-http callback":     func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.NotifUri = "ftp://example.com/notify" },
		"hostless callback":     func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.NotifUri = "https:///notify" },
		"callback userinfo": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.NotifUri = "https://user@example.com/notify"
		},
		"callback query": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.NotifUri = "https://example.com/notify?a=b"
		},
		"callback fragment": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.NotifUri = "https://example.com/notify#fragment"
		},
		"no event": func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.EventSubs = nil },
		"multiple events": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.EventSubs = append(r.EventSubs, r.EventSubs[0])
		},
		"wrong event":  func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.EventSubs[0].Event = "PDU_SES_REL" },
		"no upf event": func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.EventSubs[0].UpfEvents = nil },
		"multiple upf events": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.EventSubs[0].UpfEvents = append(r.EventSubs[0].UpfEvents, r.EventSubs[0].UpfEvents[0])
		},
		"wrong upf event": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.EventSubs[0].UpfEvents[0].Type = "APPLICATION_RELATED_INFO"
		},
		"no measurement": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.EventSubs[0].UpfEvents[0].MeasurementTypes = nil
		},
		"unsupported measurement": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.EventSubs[0].UpfEvents[0].MeasurementTypes = []models.Upf_EvtExpos_MeasurementType{"OTHER"}
		},
		"wrong granularity": func(r *models.Smf_EvtExpos_NsmfEventExposure) {
			r.EventSubs[0].UpfEvents[0].GranularityOfMeasurement = "PER_APPLICATION"
		},
		"omitted reporting method": func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.NotifMethod = "" },
		"wrong reporting method":   func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.NotifMethod = "ONE_TIME" },
		"non-positive period":      func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.RepPeriod = 0 },
		"malformed feature mask":   func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.SupportedFeatures = "xyz" },
		"feature 26 not supported": func(r *models.Smf_EvtExpos_NsmfEventExposure) { r.SupportedFeatures = "1" },
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			request := validEventExposureRequestForTest()
			mutate(request)
			_, problem := validateEventExposureRequest(request, true)
			require.NotNil(t, problem)
			require.Equal(t, http.StatusBadRequest, problem.Status)
		})
	}

	_, problem := validateEventExposureRequest(validEventExposureRequestForTest(), false)
	require.NotNil(t, problem)
	require.Equal(t, http.StatusBadRequest, problem.Status)
}

func TestValidateEventExposureRequestRejectsKnownOutOfProfileFields(t *testing.T) {
	topLevel := []string{
		"Gpsi", "AnyUeInd", "GroupId", "Dnai", "SsId", "BssId", "UpfId", "SubId",
		"AltNotifIpv4Addrs", "AltNotifIpv6Addrs", "AltNotifFqdns", "EventNotifs", "ImmeRep",
		"MaxReportNbr", "Expiry", "Guami", "ServiveName", "SampRatio", "PartitionCriteria",
		"GrpRepTime", "NotifFlag", "NotifFlagInstruct", "MutingSetting", "DefQosSupp", "QosMonPending",
	}
	event := []string{
		"DnaiChgType", "DddTraDescriptors", "DddStati", "AppIds", "NetworkArea", "TargetPeriod",
		"TransacDispInd", "TransacMetrics", "UeIpAddr",
	}
	upfEvent := []string{"ImmediateFlag", "AppIds", "TrafficFilters", "ReportingSuggestionInfo"}

	for _, field := range topLevel {
		t.Run("top-level "+field, func(t *testing.T) {
			request := validEventExposureRequestForTest()
			setEventExposureTestField(t, request, field)
			_, problem := validateEventExposureRequest(request, true)
			require.NotNil(t, problem)
		})
	}
	for _, field := range event {
		t.Run("event "+field, func(t *testing.T) {
			request := validEventExposureRequestForTest()
			setEventExposureTestField(t, &request.EventSubs[0], field)
			_, problem := validateEventExposureRequest(request, true)
			require.NotNil(t, problem)
		})
	}
	for _, field := range upfEvent {
		t.Run("upf event "+field, func(t *testing.T) {
			request := validEventExposureRequestForTest()
			setEventExposureTestField(t, &request.EventSubs[0].UpfEvents[0], field)
			_, problem := validateEventExposureRequest(request, true)
			require.NotNil(t, problem)
		})
	}
}

func setEventExposureTestField(t *testing.T, target any, fieldName string) {
	t.Helper()
	field := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	require.True(t, field.IsValid(), "generated field %s must exist", fieldName)
	switch field.Kind() {
	case reflect.Bool:
		field.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(1)
	case reflect.String:
		field.SetString("out-of-profile")
	case reflect.Pointer:
		field.Set(reflect.New(field.Type().Elem()))
	case reflect.Slice:
		field.Set(reflect.MakeSlice(field.Type(), 1, 1))
	default:
		t.Fatalf("unsupported generated field kind %s for %s", field.Kind(), fieldName)
	}
}

func TestBuildNupfEventExposureRequestUsesSessionOwnedValues(t *testing.T) {
	request := validEventExposureRequestForTest()
	request.Dnn = "request-selector"
	request.Snssai = &models.Snssai{Sst: 9, Sd: "request"}
	target := &eventExposureTarget{
		supi:      testEventExposureSupi,
		dnn:       "session-dnn",
		snssai:    &models.Snssai{Sst: 1, Sd: "ABCDEF"},
		ipAddress: &models.IpAddr{Ipv4Addr: "10.60.0.1"},
	}

	downstream := buildNupfEventExposureRequest(testEventExposureSMFID, request, target)
	require.NotNil(t, downstream.Subscription)
	require.Empty(t, downstream.SupportedFeatures)
	subscription := downstream.Subscription
	require.Equal(t, testEventExposureNotification, subscription.EventNotifyUri)
	require.Equal(t, request.NotifId, subscription.NotifyCorrelationId)
	require.Equal(t, testEventExposureSMFID, subscription.NfId)
	require.Equal(t, target.supi, subscription.Supi)
	require.Equal(t, target.dnn, subscription.Dnn)
	require.Equal(t, target.snssai, subscription.Snssai)
	require.NotSame(t, target.snssai, subscription.Snssai)
	require.Equal(t, target.ipAddress, subscription.UeIpAddress)
	require.Len(t, subscription.EventList, 1)
	require.Equal(t, request.EventSubs[0].UpfEvents[0].MeasurementTypes, subscription.EventList[0].MeasurementTypes)
	require.Equal(t, models.Upf_EvtExpos_EventType_USER_DATA_USAGE_MEASURES, subscription.EventList[0].Type)
	require.Equal(t,
		models.Upf_EvtExpos_GranularityOfMeasurement_SESSION,
		subscription.EventList[0].GranularityOfMeasurement)
	require.Equal(t, models.Upf_EvtExpos_UpfEventTrigger_PERIODIC, subscription.EventReportingMode.Trigger)
	require.Equal(t, request.RepPeriod, subscription.EventReportingMode.RepPeriod)
}

func TestCreateAndDeleteEventExposureSubscriptionLifecycle(t *testing.T) {
	var (
		captured models.Upf_EvtExpos_CreateEventSubscription
		deletes  atomic.Int32
	)
	server := newUnencryptedHTTP2TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/nupf-ee/v1/ee-subscriptions":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
			location := "http://" + r.Host + "/nupf-ee/v1/ee-subscriptions/downstream-1"
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusCreated)
			require.NoError(t, json.NewEncoder(w).Encode(models.Upf_EvtExpos_CreatedEventSubscription{
				Subscription:   captured.Subscription,
				SubscriptionId: location,
			}))
		case r.Method == http.MethodDelete && r.URL.Path == "/nupf-ee/v1/ee-subscriptions/downstream-1":
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))

	processor := newEventExposureProcessor(t)
	setupEventExposureSession(t, server.URL)
	request := validEventExposureRequestForTest()
	result, problem := processor.CreateEventExposureSubscription(context.Background(), request, true)
	require.Nil(t, problem)
	require.NotNil(t, result)
	require.Equal(t, smfEventExposureSupportedFeatures, result.Response.SupportedFeatures)
	require.NotEmpty(t, result.Response.SubId)
	require.Equal(t, result.Response.SubId, path.Base(result.Location))
	require.Equal(t, "http://127.0.0.1:8000/nsmf-event-exposure/v1/subscriptions/"+result.Response.SubId, result.Location)

	require.NotNil(t, captured.Subscription)
	require.Equal(t, testEventExposureSMFID, captured.Subscription.NfId)
	require.Equal(t, "internet", captured.Subscription.Dnn)
	require.Equal(t, &models.Snssai{Sst: 1, Sd: "ABCDEF"}, captured.Subscription.Snssai)
	require.Equal(t, &models.IpAddr{Ipv4Addr: "10.60.0.1"}, captured.Subscription.UeIpAddress)
	require.Empty(t, captured.SupportedFeatures)

	record, ok := smf_context.GetEventExposureSubscription(result.Response.SubId)
	require.True(t, ok)
	apiRoot, downstreamID := record.Coordinates()
	require.Equal(t, server.URL, apiRoot)
	require.Equal(t, "downstream-1", downstreamID)

	require.Nil(t, processor.DeleteEventExposureSubscription(context.Background(), result.Response.SubId))
	_, ok = smf_context.GetEventExposureSubscription(result.Response.SubId)
	require.False(t, ok)
	require.EqualValues(t, 1, deletes.Load())
}

func TestCreateEventExposureSubscriptionRejectsAliasedResourceBeforePublishingState(t *testing.T) {
	resourcePaths := map[string]string{
		"aliased deployment prefix": "/%64eployment/nupf-ee/v1/ee-subscriptions/downstream-1",
		"aliased service path":      "/deployment/nupf%2Dee/v1/ee-subscriptions/downstream-1",
		"normalized dot path":       "/deployment/./nupf-ee/v1/ee-subscriptions/downstream-1",
		"malformed escape":          "/deployment/nupf-ee/v1/ee-subscriptions/downstream%ZZ",
		"raw space":                 "/deployment/nupf-ee/v1/ee-subscriptions/downstream id",
		"raw non-ASCII":             "/deployment/nupf-ee/v1/ee-subscriptions/訂閱",
	}

	for name, resourcePath := range resourcePaths {
		t.Run(name, func(t *testing.T) {
			var nupfCalls atomic.Int32
			server := newUnencryptedHTTP2TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nupfCalls.Add(1)
				require.Equal(t, http.MethodPost, r.Method)
				location := "http://" + r.Host + resourcePath
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusCreated)
				require.NoError(t, json.NewEncoder(w).Encode(models.Upf_EvtExpos_CreatedEventSubscription{
					Subscription: &models.Upf_EvtExpos_UpfEventSubscription{
						NotifyCorrelationId: "downstream-response-secret",
					},
					SubscriptionId: location,
				}))
			}))

			processor := newEventExposureProcessor(t)
			setupEventExposureSession(t, server.URL+"/deployment")
			result, problem := processor.CreateEventExposureSubscription(
				context.Background(),
				validEventExposureRequestForTest(),
				true,
			)
			require.Nil(t, result)
			require.NotNil(t, problem)
			require.Equal(t, http.StatusBadGateway, problem.Status)
			require.Equal(t, int32(http.StatusBadGateway), problem.ProblemDetails.Status)
			require.Equal(t, "Bad Gateway", problem.ProblemDetails.Title)
			require.Equal(t, badGatewayDetail, problem.ProblemDetails.Detail)
			require.Empty(t, problem.ProblemDetails.Cause)
			require.EqualValues(t, 1, nupfCalls.Load())
			encodedProblem, err := json.Marshal(problem.ProblemDetails)
			require.NoError(t, err)
			require.NotContains(t, string(encodedProblem), server.URL)
			require.NotContains(t, string(encodedProblem), resourcePath)
			require.NotContains(t, string(encodedProblem), "downstream-response-secret")
		})
	}
}

func TestCreateEventExposureSubscriptionRejectsUnavailableSessionStateBeforeNupf(t *testing.T) {
	tests := map[string]func(*smf_context.SMContext){
		"inactive session": func(session *smf_context.SMContext) {
			session.SetState(smf_context.InActive)
		},
		"missing selected UPF": func(session *smf_context.SMContext) {
			session.SelectedUPF = nil
		},
		"missing PDU address": func(session *smf_context.SMContext) {
			session.PDUAddress = nil
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var nupfCalls atomic.Int32
			server := newUnencryptedHTTP2TestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				nupfCalls.Add(1)
			}))
			processor := newEventExposureProcessor(t)
			session := setupEventExposureSession(t, server.URL)
			session.SMLock.Lock()
			mutate(session)
			session.SMLock.Unlock()

			result, problem := processor.CreateEventExposureSubscription(
				context.Background(),
				validEventExposureRequestForTest(),
				true,
			)
			require.Nil(t, result)
			require.NotNil(t, problem)
			require.Equal(t, http.StatusNotFound, problem.Status)
			require.Zero(t, nupfCalls.Load())
		})
	}
}

func TestResolveEventExposureTargetPreNupfFailuresAndLockOrder(t *testing.T) {
	processor := newEventExposureProcessor(t)
	session := setupEventExposureSession(t, "")

	request := validEventExposureRequestForTest()
	target, problem := processor.resolveEventExposureTarget(request)
	require.Nil(t, target)
	require.NotNil(t, problem)
	require.Equal(t, http.StatusServiceUnavailable, problem.Status)

	request.Dnn = "mismatch"
	_, problem = processor.resolveEventExposureTarget(request)
	require.NotNil(t, problem)
	require.Equal(t, http.StatusNotFound, problem.Status)

	request = validEventExposureRequestForTest()
	processor.Context().UserPlaneInformation.Mu.Lock()
	done := make(chan *EventExposureProblem, 1)
	go func() {
		_, createProblem := processor.CreateEventExposureSubscription(context.Background(), request, true)
		done <- createProblem
	}()
	time.Sleep(25 * time.Millisecond)

	sessionLockAcquired := make(chan struct{})
	go func() {
		session.SMLock.Lock()
		close(sessionLockAcquired)
		session.SMLock.Unlock()
	}()
	select {
	case <-sessionLockAcquired:
	case <-time.After(time.Second):
		processor.Context().UserPlaneInformation.Mu.Unlock()
		t.Fatal("session lock remained held while waiting for topology lock")
	}
	processor.Context().UserPlaneInformation.Mu.Unlock()

	select {
	case createProblem := <-done:
		require.NotNil(t, createProblem)
		require.Equal(t, http.StatusServiceUnavailable, createProblem.Status)
	case <-time.After(time.Second):
		t.Fatal("Create did not finish after topology lock release")
	}
}

func TestEventExposureDownstreamErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		title  string
		detail string
		cause  string
	}{
		{
			"clear timeout",
			&consumer.NupfEventExposureError{TimedOut: true},
			http.StatusGatewayTimeout,
			"Gateway Timeout",
			gatewayTimeoutDetail,
			"",
		},
		{
			"received 429",
			&consumer.NupfEventExposureError{StatusCode: http.StatusTooManyRequests},
			http.StatusBadGateway,
			"Bad Gateway",
			badGatewayDetail,
			"INBOUND_SERVER_ERROR",
		},
		{
			"received 500",
			&consumer.NupfEventExposureError{StatusCode: http.StatusInternalServerError},
			http.StatusBadGateway,
			"Bad Gateway",
			badGatewayDetail,
			"INBOUND_SERVER_ERROR",
		},
		{
			"received 504",
			&consumer.NupfEventExposureError{StatusCode: http.StatusGatewayTimeout},
			http.StatusBadGateway,
			"Bad Gateway",
			badGatewayDetail,
			"INBOUND_SERVER_ERROR",
		},
		{
			"received 599",
			&consumer.NupfEventExposureError{StatusCode: 599},
			http.StatusBadGateway,
			"Bad Gateway",
			badGatewayDetail,
			"INBOUND_SERVER_ERROR",
		},
		{
			"received 600",
			&consumer.NupfEventExposureError{StatusCode: 600},
			http.StatusBadGateway,
			"Bad Gateway",
			badGatewayDetail,
			"",
		},
		{
			"received 404",
			&consumer.NupfEventExposureError{StatusCode: http.StatusNotFound},
			http.StatusBadGateway,
			"Bad Gateway",
			badGatewayDetail,
			"",
		},
		{
			"redirect",
			&consumer.NupfEventExposureError{StatusCode: http.StatusTemporaryRedirect},
			http.StatusBadGateway,
			"Bad Gateway",
			badGatewayDetail,
			"",
		},
		{
			"transport",
			&consumer.NupfEventExposureError{},
			http.StatusBadGateway,
			"Bad Gateway",
			badGatewayDetail,
			"",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			problem := nupfEventExposureProblem(test.err)
			require.Equal(t, test.status, problem.Status)
			require.Equal(t, int32(test.status), problem.ProblemDetails.Status)
			require.Equal(t, test.title, problem.ProblemDetails.Title)
			require.Equal(t, test.detail, problem.ProblemDetails.Detail)
			require.Equal(t, test.cause, problem.ProblemDetails.Cause)
		})
	}
}

func TestDeleteEventExposureSubscriptionConcurrencyAndFailureRetention(t *testing.T) {
	var successfulDeletes atomic.Int32
	successServer := newUnencryptedHTTP2TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		successfulDeletes.Add(1)
		time.Sleep(25 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	processor := newEventExposureProcessor(t)
	localID := smf_context.StoreEventExposureSubscription(successServer.URL, "downstream-concurrent")

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	results := make(chan *EventExposureProblem, 2)
	for range 2 {
		go func() {
			defer waitGroup.Done()
			results <- processor.DeleteEventExposureSubscription(context.Background(), localID)
		}()
	}
	waitGroup.Wait()
	close(results)
	var success, notFound int
	for problem := range results {
		if problem == nil {
			success++
		} else if problem.Status == http.StatusNotFound {
			notFound++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, notFound)
	require.EqualValues(t, 1, successfulDeletes.Load())
	_, ok := smf_context.GetEventExposureSubscription(localID)
	require.False(t, ok)
}

func TestDeleteEventExposureSubscriptionRetainsStateAndSanitizesFailures(t *testing.T) {
	tests := []struct {
		name           string
		status         int
		timeout        bool
		expectedStatus int
		expectedTitle  string
		expectedDetail string
		expectedCause  string
	}{
		{
			name:           "not found",
			status:         http.StatusNotFound,
			expectedStatus: http.StatusBadGateway,
			expectedTitle:  "Bad Gateway",
			expectedDetail: badGatewayDetail,
		},
		{
			name:           "other non-204",
			status:         http.StatusTeapot,
			expectedStatus: http.StatusBadGateway,
			expectedTitle:  "Bad Gateway",
			expectedDetail: badGatewayDetail,
		},
		{
			name:           "server error",
			status:         http.StatusInternalServerError,
			expectedStatus: http.StatusBadGateway,
			expectedTitle:  "Bad Gateway",
			expectedDetail: badGatewayDetail,
			expectedCause:  "INBOUND_SERVER_ERROR",
		},
		{
			name:           "timeout",
			timeout:        true,
			expectedStatus: http.StatusGatewayTimeout,
			expectedTitle:  "Gateway Timeout",
			expectedDetail: gatewayTimeoutDetail,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var nupfCalls atomic.Int32
			server := newUnencryptedHTTP2TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nupfCalls.Add(1)
				if test.timeout {
					select {
					case <-r.Context().Done():
					case <-time.After(100 * time.Millisecond):
					}
					return
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.Header().Set("Location", "https://downstream.example.com/header-secret")
				w.Header().Set("Retry-After", "header-secret")
				w.Header().Set("WWW-Authenticate", "header-secret")
				w.WriteHeader(test.status)
				_, err := w.Write([]byte(`{"title":"body-secret","detail":"internal-error-secret"}`))
				require.NoError(t, err)
			}))

			smfContext := smf_context.GetSelf()
			oldOAuth := smfContext.OAuth2Required
			smfContext.OAuth2Required = false
			t.Cleanup(func() { smfContext.OAuth2Required = oldOAuth })
			processor := newEventExposureProcessor(t)
			localID := smf_context.StoreEventExposureSubscription(
				server.URL+"/endpoint-secret",
				"downstream-failure",
			)
			record, ok := smf_context.GetEventExposureSubscription(localID)
			require.True(t, ok)
			t.Cleanup(func() { smf_context.RemoveEventExposureSubscription(localID, record) })

			requestContext := context.Background()
			if test.timeout {
				var cancel context.CancelFunc
				requestContext, cancel = context.WithTimeout(requestContext, 20*time.Millisecond)
				defer cancel()
			}
			problem := processor.DeleteEventExposureSubscription(requestContext, localID)
			require.NotNil(t, problem)
			require.Equal(t, test.expectedStatus, problem.Status)
			require.Equal(t, int32(test.expectedStatus), problem.ProblemDetails.Status)
			require.Equal(t, test.expectedTitle, problem.ProblemDetails.Title)
			require.Equal(t, test.expectedDetail, problem.ProblemDetails.Detail)
			require.Equal(t, test.expectedCause, problem.ProblemDetails.Cause)
			require.EqualValues(t, 1, nupfCalls.Load())

			encodedProblem, err := json.Marshal(problem.ProblemDetails)
			require.NoError(t, err)
			for _, forbidden := range []string{
				server.URL,
				"endpoint-secret",
				"header-secret",
				"body-secret",
				"internal-error-secret",
				"nupf event exposure operation failed",
				"context deadline exceeded",
			} {
				require.NotContains(t, string(encodedProblem), forbidden)
			}

			retained, ok := smf_context.GetEventExposureSubscription(localID)
			require.True(t, ok)
			require.Same(t, record, retained)
		})
	}
}

func newUnencryptedHTTP2TestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func setupEventExposureSession(t *testing.T, apiRoot string) *smf_context.SMContext {
	t.Helper()
	ctx := smf_context.GetSelf()
	oldNFID := ctx.NfInstanceID
	oldScheme := ctx.URIScheme
	oldRegisterIPv4 := ctx.RegisterIPv4
	oldSBIPort := ctx.SBIPort
	oldOAuth := ctx.OAuth2Required
	oldUPI := ctx.UserPlaneInformation
	oldTeidGenerator := smf_context.TeidGenerator

	ctx.NfInstanceID = testEventExposureSMFID
	ctx.URIScheme = models.UriScheme_HTTP
	ctx.RegisterIPv4 = "127.0.0.1"
	ctx.SBIPort = 8000
	ctx.OAuth2Required = false
	selectedUPF := &smf_context.UPNode{Name: "UPF1", Type: smf_context.UPNODE_UPF, NupfEeApiRoot: apiRoot}
	ctx.UserPlaneInformation = &smf_context.UserPlaneInformation{
		UPNodes:                   map[string]*smf_context.UPNode{"UPF1": selectedUPF},
		UPFs:                      map[string]*smf_context.UPNode{"UPF1": selectedUPF},
		AccessNetwork:             make(map[string]*smf_context.UPNode),
		UPFIPToName:               make(map[string]string),
		UPFsID:                    make(map[string]string),
		UPFsIPtoID:                make(map[string]string),
		DefaultUserPlanePath:      make(map[string][]*smf_context.UPNode),
		DefaultUserPlanePathToUPF: make(map[string]map[string][]*smf_context.UPNode),
	}
	smf_context.TeidGenerator = idgenerator.NewGenerator(1, math.MaxUint32)

	session := smf_context.NewSMContext(testEventExposureSupi, 1)
	require.NotNil(t, session)
	session.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi:         testEventExposureSupi,
		PduSessionId: 1,
		Dnn:          "internet",
		SNssai:       &models.Snssai{Sst: 1, Sd: "ABCDEF"},
	}
	session.PDUAddress = net.ParseIP("10.60.0.1")
	session.SelectedUPF = selectedUPF
	session.SetState(smf_context.Active)

	t.Cleanup(func() {
		session.SMLock.Lock()
		session.SelectedUPF = nil
		session.PDUAddress = nil
		session.SMLock.Unlock()
		smf_context.RemoveSMContext(session.Ref)
		smf_context.TeidGenerator = oldTeidGenerator
		ctx.NfInstanceID = oldNFID
		ctx.URIScheme = oldScheme
		ctx.RegisterIPv4 = oldRegisterIPv4
		ctx.SBIPort = oldSBIPort
		ctx.OAuth2Required = oldOAuth
		ctx.UserPlaneInformation = oldUPI
	})
	return session
}
