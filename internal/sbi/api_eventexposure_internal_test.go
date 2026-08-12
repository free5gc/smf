package sbi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/internal/sbi/processor"
	"github.com/free5gc/smf/pkg/factory"
)

func TestHTTPCreateIndividualSubcriptionReturnsLocationAndBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventProcessor, err := processor.NewProcessorWithEventExposureDependencies(
		&fakeEventExposureSMF{
			context: &smf_context.SMFContext{NfInstanceID: "smf-instance"},
		},
		processor.EventExposureDependencies{
			Repository: smf_context.NewEventExposureRepository(),
			Resolver: fakeSBIEventExposureResolver{
				target: smf_context.EventExposureTarget{
					APIroot:        "https://upf.example.com",
					ServiceBaseURL: "https://upf.example.com/nupf-ee/v1",
					UEIPAddress:    net.ParseIP("192.0.2.1").To4(),
				},
			},
			NupfConsumer: fakeSBIEventExposureConsumer{
				result: smf_context.NupfCreateResult{
					SubscriptionID:    "upf-sub-1",
					ValidatedLocation: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/upf-sub-1",
				},
			},
			UUIDGenerator: fixedSBIUUIDGenerator{},
		},
	)
	if err != nil {
		t.Fatalf("NewProcessorWithEventExposureDependencies failed: %v", err)
	}

	server := &Server{ServerSmf: fakeServerSMF{processor: eventProcessor}}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/nsmf-event-exposure/v1/subscriptions",
		strings.NewReader(validEventExposureJSON("")),
	)

	server.HTTPCreateIndividualSubcription(ginContext)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status mismatch: %d body %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/nsmf-event-exposure/v1/subscriptions/sub-1" {
		t.Fatalf("Location mismatch: %q", recorder.Header().Get("Location"))
	}

	var response models.NsmfEventExposure
	if unmarshalErr := json.Unmarshal(recorder.Body.Bytes(), &response); unmarshalErr != nil {
		t.Fatalf("Unmarshal response failed: %v", unmarshalErr)
	}
	if response.SubId != "sub-1" ||
		response.Supi != "imsi-001010000000001" ||
		response.NotifId != "correlation-1" ||
		response.NotifUri != "https://nwdaf.example.com/nsmf" ||
		response.RepPeriod != 10 ||
		response.NotifMethod != models.SmfEventExposureNotificationMethod_PERIODIC ||
		len(response.EventSubs) != 1 ||
		response.EventSubs[0].BundledEventNotifyUri != "https://nwdaf.example.com/upf" {
		t.Fatalf("unexpected response body: %+v", response)
	}
}

type fakeServerSMF struct {
	processor *processor.Processor
}

func (f fakeServerSMF) SetLogEnable(bool)                {}
func (f fakeServerSMF) SetLogLevel(string)               {}
func (f fakeServerSMF) SetReportCaller(bool)             {}
func (f fakeServerSMF) Start()                           {}
func (f fakeServerSMF) Terminate()                       {}
func (f fakeServerSMF) Context() *smf_context.SMFContext { return nil }
func (f fakeServerSMF) Config() *factory.Config          { return nil }
func (f fakeServerSMF) Consumer() *consumer.Consumer     { return nil }
func (f fakeServerSMF) Processor() *processor.Processor  { return f.processor }
func (f fakeServerSMF) CancelContext() context.Context   { return context.Background() }

type fakeEventExposureSMF struct {
	context *smf_context.SMFContext
}

func (f *fakeEventExposureSMF) SetLogEnable(bool)                {}
func (f *fakeEventExposureSMF) SetLogLevel(string)               {}
func (f *fakeEventExposureSMF) SetReportCaller(bool)             {}
func (f *fakeEventExposureSMF) Start()                           {}
func (f *fakeEventExposureSMF) Terminate()                       {}
func (f *fakeEventExposureSMF) Context() *smf_context.SMFContext { return f.context }
func (f *fakeEventExposureSMF) Config() *factory.Config          { return nil }
func (f *fakeEventExposureSMF) Consumer() *consumer.Consumer     { return nil }

type fakeSBIEventExposureResolver struct {
	target smf_context.EventExposureTarget
}

func (f fakeSBIEventExposureResolver) ResolveEventExposureTarget(
	context.Context,
	string,
	smf_context.EventExposureSelectors,
) (smf_context.EventExposureTarget, error) {
	return f.target, nil
}

type fakeSBIEventExposureConsumer struct {
	result smf_context.NupfCreateResult
}

func (f fakeSBIEventExposureConsumer) CreateSubscription(
	context.Context,
	smf_context.EventExposureTarget,
	models.UpfCreateEventSubscription,
) (smf_context.NupfCreateResult, error) {
	return f.result, nil
}

func (f fakeSBIEventExposureConsumer) DeleteSubscription(
	context.Context,
	smf_context.EventExposureTarget,
	string,
) error {
	return nil
}

type fixedSBIUUIDGenerator struct{}

func (f fixedSBIUUIDGenerator) NewString() string {
	return "sub-1"
}
