//nolint:testpackage // HTTP contract tests use package-private route construction.
package sbi

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/internal/sbi/processor"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

const (
	eventExposureAPITestSupi       = "imsi-001010000000002"
	eventExposureAPITestConsumerID = "c2ef23cb-9cbc-4a50-8677-b206f23408ed"
	eventExposureAPITestSMFID      = "8614bb1c-f79d-4a35-b259-682d70e405c5"
)

type eventExposureServerTestApp struct {
	ctx       *smf_context.SMFContext
	config    *factory.Config
	consumer  *consumer.Consumer
	processor *processor.Processor
}

func (*eventExposureServerTestApp) SetLogEnable(bool)    {}
func (*eventExposureServerTestApp) SetLogLevel(string)   {}
func (*eventExposureServerTestApp) SetReportCaller(bool) {}
func (*eventExposureServerTestApp) Start()               {}
func (*eventExposureServerTestApp) Terminate()           {}

func (a *eventExposureServerTestApp) Context() *smf_context.SMFContext { return a.ctx }
func (a *eventExposureServerTestApp) Config() *factory.Config          { return a.config }
func (a *eventExposureServerTestApp) Consumer() *consumer.Consumer     { return a.consumer }
func (a *eventExposureServerTestApp) Processor() *processor.Processor  { return a.processor }
func (*eventExposureServerTestApp) CancelContext() context.Context     { return context.Background() }

func TestEventExposureRouteAndServiceGating(t *testing.T) {
	disabled := newEventExposureTestRouter(t, false)
	response := performEventExposureRequest(t, disabled, http.MethodPost,
		factory.SmfEventExposureResUriPrefix+"/subscriptions", []byte(`{`))
	require.Equal(t, http.StatusNotFound, response.Code)

	enabled := newEventExposureTestRouter(t, true)
	response = performEventExposureRequest(t, enabled, http.MethodPost,
		"/nsmf_event-exposure/v1/subscriptions", []byte(`{`))
	require.Equal(t, http.StatusNotFound, response.Code)

	response = performEventExposureRequest(t, enabled, http.MethodPost,
		factory.SmfEventExposureResUriPrefix+"/subscriptions", []byte(`{`))
	require.Equal(t, http.StatusBadRequest, response.Code)

	response = performEventExposureRequest(t, enabled, http.MethodGet,
		factory.SmfEventExposureResUriPrefix+"/subscriptions/not-implemented", nil)
	require.Equal(t, http.StatusNotImplemented, response.Code)
	response = performEventExposureRequest(t, enabled, http.MethodPut,
		factory.SmfEventExposureResUriPrefix+"/subscriptions/not-implemented", []byte(`{}`))
	require.Equal(t, http.StatusNotImplemented, response.Code)
}

func TestEventExposureCreateBindingPresenceAndUnknownFields(t *testing.T) {
	router := newEventExposureTestRouter(t, true)
	uri := factory.SmfEventExposureResUriPrefix + "/subscriptions"
	tests := []struct {
		name   string
		body   []byte
		status int
	}{
		{"malformed", []byte(`{"pduSeId":`), http.StatusBadRequest},
		{"non-object", []byte(`[]`), http.StatusBadRequest},
		{"omitted pdu session id", eventExposureAPIRequestJSON(t, nil, nil), http.StatusBadRequest},
		{"explicit zero reaches lookup", eventExposureAPIRequestJSON(t, int32Pointer(0), nil), http.StatusNotFound},
		{
			"unknown field is ignored",
			eventExposureAPIRequestJSON(t, int32Pointer(0),
				map[string]any{"futureAttribute": map[string]any{"value": true}}),
			http.StatusNotFound,
		},
		{
			"known explicit false is default",
			eventExposureAPIRequestJSON(t, int32Pointer(0), map[string]any{"anyUeInd": false}),
			http.StatusNotFound,
		},
		{
			"known non-default is rejected",
			eventExposureAPIRequestJSON(t, int32Pointer(0), map[string]any{"anyUeInd": true}),
			http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performEventExposureRequest(t, router, http.MethodPost, uri, test.body)
			require.Equal(t, test.status, response.Code)
		})
	}
}

func TestEventExposureHTTPCreateAndDeleteContract(t *testing.T) {
	var (
		downstreamCreate  models.Upf_EvtExpos_CreateEventSubscription
		downstreamDeletes atomic.Int32
	)
	downstream := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost:
				if err := json.NewDecoder(r.Body).Decode(&downstreamCreate); err != nil {
					http.Error(w, "invalid generated request", http.StatusBadRequest)
					return
				}
				location := "http://" + r.Host + "/nupf-ee/v1/ee-subscriptions/downstream-http"
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusCreated)
				if err := json.NewEncoder(w).Encode(models.Upf_EvtExpos_CreatedEventSubscription{
					Subscription:   downstreamCreate.Subscription,
					SubscriptionId: location,
				}); err != nil {
					t.Errorf("encode downstream response: %v", err)
				}
			case http.MethodDelete:
				downstreamDeletes.Add(1)
				w.WriteHeader(http.StatusNoContent)
			default:
				http.NotFound(w, r)
			}
		}))
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	downstream.Config.Protocols = protocols
	downstream.Start()
	t.Cleanup(downstream.Close)

	router := newEventExposureTestRouter(t, true)
	setupEventExposureAPITestSession(t, downstream.URL)
	createResponse := performEventExposureRequest(t, router, http.MethodPost,
		factory.SmfEventExposureResUriPrefix+"/subscriptions",
		eventExposureAPIRequestJSON(t, int32Pointer(1), map[string]any{"futureAttribute": "ignored"}))
	require.Equal(t, http.StatusCreated, createResponse.Code, createResponse.Body.String())
	location := createResponse.Header().Get("Location")
	require.True(t, strings.HasPrefix(location,
		"http://127.0.0.1:8000/nsmf-event-exposure/v1/subscriptions/"))
	require.NotContains(t, location, "downstream-http")

	var responseBody models.Smf_EvtExpos_NsmfEventExposure
	require.NoError(t, json.Unmarshal(createResponse.Body.Bytes(), &responseBody))
	require.NotEmpty(t, responseBody.SubId)
	require.True(t, strings.HasSuffix(location, "/"+responseBody.SubId))
	require.Equal(t, "02000000", responseBody.SupportedFeatures)
	require.NotNil(t, downstreamCreate.Subscription)
	require.Equal(t, "https://consumer.example.com/notify", downstreamCreate.Subscription.EventNotifyUri)
	require.Equal(t, "api-correlation", downstreamCreate.Subscription.NotifyCorrelationId)
	require.Equal(t, eventExposureAPITestSMFID, downstreamCreate.Subscription.NfId)
	require.Empty(t, downstreamCreate.SupportedFeatures)

	deleteResponse := performEventExposureRequest(t, router, http.MethodDelete,
		factory.SmfEventExposureResUriPrefix+"/subscriptions/"+responseBody.SubId, nil)
	require.Equal(t, http.StatusNoContent, deleteResponse.Code)
	require.Empty(t, deleteResponse.Body.String())
	require.EqualValues(t, 1, downstreamDeletes.Load())

	deleteResponse = performEventExposureRequest(t, router, http.MethodDelete,
		factory.SmfEventExposureResUriPrefix+"/subscriptions/"+responseBody.SubId, nil)
	require.Equal(t, http.StatusNotFound, deleteResponse.Code)
}

func newEventExposureTestRouter(t *testing.T, enabled bool) http.Handler {
	t.Helper()
	oldConfig := factory.SmfConfig
	oldOAuth := smf_context.GetSelf().OAuth2Required
	serviceNames := []string{}
	if enabled {
		serviceNames = append(serviceNames, string(models.Nrf_NFMgmt_ServiceName_NSMF_EVENT_EXPOSURE))
	}
	config := &factory.Config{Configuration: &factory.Configuration{ServiceNameList: serviceNames}}
	factory.SmfConfig = config
	smf_context.GetSelf().OAuth2Required = false
	t.Cleanup(func() {
		factory.SmfConfig = oldConfig
		smf_context.GetSelf().OAuth2Required = oldOAuth
	})

	app := &eventExposureServerTestApp{ctx: smf_context.GetSelf(), config: config}
	var err error
	app.consumer, err = consumer.NewConsumer(app)
	require.NoError(t, err)
	app.processor, err = processor.NewProcessor(app)
	require.NoError(t, err)
	return newRouter(&Server{ServerSmf: app})
}

func performEventExposureRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	uri string,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, uri, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func eventExposureAPIRequestJSON(t *testing.T, pduSessionID *int32, extra map[string]any) []byte {
	t.Helper()
	request := models.Smf_EvtExpos_NsmfEventExposure{
		Supi:              eventExposureAPITestSupi,
		PduSeId:           1,
		Dnn:               "internet",
		Snssai:            &models.Snssai{Sst: 1, Sd: "abcdef"},
		NfId:              eventExposureAPITestConsumerID,
		NotifId:           "api-correlation",
		NotifUri:          "https://consumer.example.com/notify",
		NotifMethod:       models.Smf_EvtExpos_NotificationMethod_PERIODIC,
		RepPeriod:         15,
		SupportedFeatures: "02000001",
		EventSubs: []models.Smf_EvtExpos_EventSubscription{
			{
				Event: models.Smf_EvtExpos_SmfEvent_UPF_EVENT,
				UpfEvents: []models.Upf_EvtExpos_UpfEvent{
					{
						Type: models.Upf_EvtExpos_EventType_USER_DATA_USAGE_MEASURES,
						MeasurementTypes: []models.Upf_EvtExpos_MeasurementType{
							models.Upf_EvtExpos_MeasurementType_VOLUME_MEASUREMENT,
						},
						GranularityOfMeasurement: models.Upf_EvtExpos_GranularityOfMeasurement_SESSION,
					},
				},
			},
		},
	}
	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	var object map[string]any
	require.NoError(t, json.Unmarshal(encoded, &object))
	if pduSessionID == nil {
		delete(object, "pduSeId")
	} else {
		object["pduSeId"] = *pduSessionID
	}
	for key, value := range extra {
		object[key] = value
	}
	encoded, err = json.Marshal(object)
	require.NoError(t, err)
	return encoded
}

func int32Pointer(value int32) *int32 {
	return &value
}

func setupEventExposureAPITestSession(t *testing.T, apiRoot string) {
	t.Helper()
	ctx := smf_context.GetSelf()
	oldNFID := ctx.NfInstanceID
	oldScheme := ctx.URIScheme
	oldRegisterIPv4 := ctx.RegisterIPv4
	oldSBIPort := ctx.SBIPort
	oldUPI := ctx.UserPlaneInformation
	oldTeidGenerator := smf_context.TeidGenerator

	ctx.NfInstanceID = eventExposureAPITestSMFID
	ctx.URIScheme = models.UriScheme_HTTP
	ctx.RegisterIPv4 = "127.0.0.1"
	ctx.SBIPort = 8000
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

	session := smf_context.NewSMContext(eventExposureAPITestSupi, 1)
	require.NotNil(t, session)
	session.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi:         eventExposureAPITestSupi,
		PduSessionId: 1,
		Dnn:          "internet",
		SNssai:       &models.Snssai{Sst: 1, Sd: "ABCDEF"},
	}
	session.PDUAddress = net.ParseIP("10.60.0.2")
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
		ctx.UserPlaneInformation = oldUPI
	})
}
