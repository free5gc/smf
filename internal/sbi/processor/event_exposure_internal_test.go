package processor

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
)

func TestEventExposureCreateSuccessStoresStateAndMapsNupfRequest(t *testing.T) {
	repository := smf_context.NewEventExposureRepository()
	nupf := &fakeEventExposureConsumer{
		createResult: smf_context.NupfCreateResult{
			SubscriptionID:    "upf-sub-1",
			ValidatedLocation: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/upf-sub-1",
		},
	}
	processor := newTestEventExposureProcessor(t, EventExposureDependencies{
		Repository:    repository,
		Resolver:      fakeEventExposureResolver{target: validEventExposureTarget()},
		NupfConsumer:  nupf,
		UUIDGenerator: &fixedUUIDGenerator{ids: []string{"sub-1"}},
	})

	result, problem := processor.CreateEventExposureSubscription(context.Background(), validEventExposureCreateRequest())
	if problem != nil {
		t.Fatalf("unexpected ProblemDetails: %+v", problem)
	}
	if result.Subscription.ID != "sub-1" ||
		result.Subscription.NupfSubscriptionID != "upf-sub-1" ||
		result.Subscription.NupfLocation != nupf.createResult.ValidatedLocation {
		t.Fatalf("unexpected subscription: %+v", result.Subscription)
	}
	if _, ok := repository.Get("sub-1"); !ok {
		t.Fatal("subscription was not stored")
	}
	if nupf.createCalls != 1 {
		t.Fatalf("expected one Nupf Create, got %d", nupf.createCalls)
	}
	subscription := nupf.lastCreateRequest.Subscription
	if subscription.EventNotifyUri != "https://nwdaf.example.com/upf" ||
		subscription.NotifyCorrelationId != "correlation-1" ||
		subscription.NfId != "smf-instance" ||
		subscription.EventReportingMode.Trigger != models.UpfEventTrigger_PERIODIC ||
		subscription.EventReportingMode.RepPeriod != 10 ||
		subscription.UeIpAddress == nil ||
		subscription.UeIpAddress.Ipv4Addr != "192.0.2.1" {
		t.Fatalf("unexpected Nupf mapping: %+v", subscription)
	}
	if len(subscription.EventList) != 1 ||
		subscription.EventList[0].Type != models.UpfEventType_USER_DATA_USAGE_MEASURES ||
		subscription.EventList[0].GranularityOfMeasurement != models.UpfGranularityOfMeasurement_PER_SESSION ||
		subscription.EventList[0].MeasurementTypes[0] != models.UpfMeasurementType_VOLUME_MEASUREMENT {
		t.Fatalf("unexpected Nupf event list: %+v", subscription.EventList)
	}
}

func TestEventExposureStoreFailureDoesNotCompensateOrRetryCreate(t *testing.T) {
	nupf := &fakeEventExposureConsumer{
		createResult: smf_context.NupfCreateResult{
			SubscriptionID:    "upf-sub-1",
			ValidatedLocation: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/upf-sub-1",
		},
	}
	repository := &failingEventExposureRepository{err: errors.New("store failed")}
	processor := newTestEventExposureProcessor(t, EventExposureDependencies{
		Repository:    repository,
		Resolver:      fakeEventExposureResolver{target: validEventExposureTarget()},
		NupfConsumer:  nupf,
		UUIDGenerator: &fixedUUIDGenerator{ids: []string{"sub-1"}},
	})

	_, problem := processor.CreateEventExposureSubscription(context.Background(), validEventExposureCreateRequest())
	if problem == nil || problem.Status != 500 {
		t.Fatalf("expected 500 ProblemDetails, got %+v", problem)
	}
	if nupf.createCalls != 1 {
		t.Fatalf("expected one Nupf Create, got %d", nupf.createCalls)
	}
	if nupf.deleteCalls != 0 {
		t.Fatalf("expected no compensating Delete, got %d", nupf.deleteCalls)
	}
	if repository.storeCalls != 1 {
		t.Fatalf("expected one Store attempt, got %d", repository.storeCalls)
	}
	if _, ok := repository.Get("sub-1"); ok {
		t.Fatal("store failure must not leave local record")
	}
}

func TestEventExposureUUIDCollisionRetryAndMaxAttempts(t *testing.T) {
	repository := smf_context.NewEventExposureRepository()
	if err := repository.Store(smf_context.EventExposureSubscription{ID: "collision"}); err != nil {
		t.Fatalf("Store collision seed failed: %v", err)
	}
	nupf := &fakeEventExposureConsumer{
		createResult: smf_context.NupfCreateResult{
			SubscriptionID:    "upf-sub-1",
			ValidatedLocation: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/upf-sub-1",
		},
	}
	processor := newTestEventExposureProcessor(t, EventExposureDependencies{
		Repository:    repository,
		Resolver:      fakeEventExposureResolver{target: validEventExposureTarget()},
		NupfConsumer:  nupf,
		UUIDGenerator: &fixedUUIDGenerator{ids: []string{"collision", "sub-2"}},
	})

	result, problem := processor.CreateEventExposureSubscription(context.Background(), validEventExposureCreateRequest())
	if problem != nil {
		t.Fatalf("unexpected ProblemDetails: %+v", problem)
	}
	if result.Subscription.ID != "sub-2" {
		t.Fatalf("expected retry ID sub-2, got %q", result.Subscription.ID)
	}

	processor = newTestEventExposureProcessor(t, EventExposureDependencies{
		Repository:    repository,
		Resolver:      fakeEventExposureResolver{target: validEventExposureTarget()},
		NupfConsumer:  nupf,
		UUIDGenerator: &fixedUUIDGenerator{ids: []string{"collision", "collision", "collision"}},
	})
	_, problem = processor.CreateEventExposureSubscription(context.Background(), validEventExposureCreateRequest())
	if problem == nil || problem.Status != 500 {
		t.Fatalf("expected max collision 500, got %+v", problem)
	}
}

func TestEventExposureDeleteUsesStoredTargetSnapshot(t *testing.T) {
	repository := smf_context.NewEventExposureRepository()
	target := validEventExposureTarget()
	err := repository.Store(smf_context.EventExposureSubscription{
		ID:                 "sub-1",
		Target:             target,
		NupfSubscriptionID: "upf-sub-1",
		NupfLocation:       "https://malicious.example.com/arbitrary",
	})
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	resolver := &countingEventExposureResolver{}
	nupf := &fakeEventExposureConsumer{}
	processor := newTestEventExposureProcessor(t, EventExposureDependencies{
		Repository:    repository,
		Resolver:      resolver,
		NupfConsumer:  nupf,
		UUIDGenerator: &fixedUUIDGenerator{},
	})

	problem := processor.DeleteEventExposureSubscription(context.Background(), "sub-1")
	if problem != nil {
		t.Fatalf("unexpected ProblemDetails: %+v", problem)
	}
	if resolver.calls != 0 {
		t.Fatalf("Delete must not rerun resolver, got %d calls", resolver.calls)
	}
	if nupf.deleteCalls != 1 {
		t.Fatalf("expected one Nupf Delete, got %d", nupf.deleteCalls)
	}
	if nupf.lastDeleteTarget.APIroot != target.APIroot ||
		nupf.lastDeleteSubscriptionID != "upf-sub-1" {
		t.Fatalf("Delete did not use stored target/subscription ID: %+v %q",
			nupf.lastDeleteTarget, nupf.lastDeleteSubscriptionID)
	}
}

func TestEventExposureResolverErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "no matching", err: smf_context.ErrEventExposureNoMatchingSession, wantStatus: 404},
		{name: "missing ue ip", err: smf_context.ErrEventExposureMissingUEIP, wantStatus: 404},
		{name: "missing upf", err: smf_context.ErrEventExposureMissingUPF, wantStatus: 404},
		{name: "multiple", err: smf_context.ErrEventExposureMultipleSessions, wantStatus: 409},
		{name: "missing endpoint", err: smf_context.ErrEventExposureMissingEndpoint, wantStatus: 503},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nupf := &fakeEventExposureConsumer{}
			processor := newTestEventExposureProcessor(t, EventExposureDependencies{
				Repository:    smf_context.NewEventExposureRepository(),
				Resolver:      fakeEventExposureResolver{err: tt.err},
				NupfConsumer:  nupf,
				UUIDGenerator: &fixedUUIDGenerator{},
			})

			_, problem := processor.CreateEventExposureSubscription(
				context.Background(), validEventExposureCreateRequest())
			if problem == nil || problem.Status != tt.wantStatus ||
				problem.ProblemDetails.Status != int32(tt.wantStatus) {
				t.Fatalf("ProblemDetails mismatch: %+v", problem)
			}
			if nupf.createCalls != 0 {
				t.Fatalf("resolver failure sent %d Nupf requests", nupf.createCalls)
			}
		})
	}
}

func TestEventExposureDownstreamCreateFailuresAreSanitized502(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "token", err: &consumer.NupfEventExposureError{Kind: consumer.NupfEventExposureErrorToken}},
		{name: "redirect", err: &consumer.NupfEventExposureError{Kind: consumer.NupfEventExposureErrorRedirect}},
		{name: "transport", err: &consumer.NupfEventExposureError{Kind: consumer.NupfEventExposureErrorTransport}},
		{name: "upstream", err: &consumer.NupfEventExposureError{Kind: consumer.NupfEventExposureErrorUpstreamProblem}},
		{name: "malformed", err: &consumer.NupfEventExposureError{Kind: consumer.NupfEventExposureErrorMalformedSuccess}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := smf_context.NewEventExposureRepository()
			nupf := &fakeEventExposureConsumer{createErr: tt.err}
			processor := newTestEventExposureProcessor(t, EventExposureDependencies{
				Repository:    repository,
				Resolver:      fakeEventExposureResolver{target: validEventExposureTarget()},
				NupfConsumer:  nupf,
				UUIDGenerator: &fixedUUIDGenerator{ids: []string{"sub-1"}},
			})

			_, problem := processor.CreateEventExposureSubscription(
				context.Background(), validEventExposureCreateRequest())
			if problem == nil ||
				problem.Status != 502 ||
				problem.ProblemDetails.Status != 502 ||
				problem.ProblemDetails.Title != "Bad Gateway" ||
				problem.ProblemDetails.Cause != "" ||
				problem.ProblemDetails.Detail != eventExposureGatewayFailureDetail {
				t.Fatalf("expected sanitized 502, got %+v", problem)
			}
			if _, ok := repository.Get("sub-1"); ok {
				t.Fatal("downstream failure must not leave local state")
			}
		})
	}
}

func TestEventExposureDeleteUnknownAndDownstreamFailureCleanup(t *testing.T) {
	repository := smf_context.NewEventExposureRepository()
	nupf := &fakeEventExposureConsumer{}
	processor := newTestEventExposureProcessor(t, EventExposureDependencies{
		Repository:    repository,
		Resolver:      &countingEventExposureResolver{},
		NupfConsumer:  nupf,
		UUIDGenerator: &fixedUUIDGenerator{},
	})

	problem := processor.DeleteEventExposureSubscription(context.Background(), "missing")
	if problem == nil || problem.Status != 404 {
		t.Fatalf("expected 404 for unknown Delete, got %+v", problem)
	}
	if nupf.deleteCalls != 0 {
		t.Fatalf("unknown Delete sent %d downstream requests", nupf.deleteCalls)
	}

	if err := repository.Store(smf_context.EventExposureSubscription{
		ID:                 "sub-1",
		Target:             validEventExposureTarget(),
		NupfSubscriptionID: "upf-sub-1",
	}); err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	nupf.deleteErr = &consumer.NupfEventExposureError{Kind: consumer.NupfEventExposureErrorToken}
	problem = processor.DeleteEventExposureSubscription(context.Background(), "sub-1")
	if problem != nil {
		t.Fatalf("downstream Delete failure should still return 204/nil problem, got %+v", problem)
	}
	if _, ok := repository.Get("sub-1"); ok {
		t.Fatal("downstream Delete failure must still remove local state")
	}
}

func TestEventExposureWarningsDoNotLogSensitiveValues(t *testing.T) {
	var logOutput bytes.Buffer
	oldOutput := logger.Log.Out
	logger.Log.SetOutput(&logOutput)
	t.Cleanup(func() {
		logger.Log.SetOutput(oldOutput)
	})

	nupf := &fakeEventExposureConsumer{
		createResult: smf_context.NupfCreateResult{
			SubscriptionID:    "upf-sub-sensitive",
			ValidatedLocation: "https://upf.example.com/nupf-ee/v1/ee-subscriptions/upf-sub-sensitive",
		},
	}
	processor := newTestEventExposureProcessor(t, EventExposureDependencies{
		Repository: &failingEventExposureRepository{err: errors.New("store failed")},
		Resolver: fakeEventExposureResolver{target: smf_context.EventExposureTarget{
			APIroot:        "https://apiroot-sensitive.example.com",
			ServiceBaseURL: "https://apiroot-sensitive.example.com/nupf-ee/v1",
			UEIPAddress:    net.ParseIP("192.0.2.55").To4(),
		}},
		NupfConsumer:  nupf,
		UUIDGenerator: &fixedUUIDGenerator{ids: []string{"sub-1"}},
	})
	request := validEventExposureCreateRequest()
	request.Supi = "imsi-001010999999999"
	request.NotifID = "notif-sensitive"
	request.NotifURI = "https://nwdaf.example.com/nsmf-sensitive"
	request.BundledEventNotifyURI = "https://nwdaf.example.com/upf-sensitive"

	_, _ = processor.CreateEventExposureSubscription(context.Background(), request)
	for _, sentinel := range []string{
		request.Supi,
		request.NotifID,
		request.NotifURI,
		request.BundledEventNotifyURI,
		"192.0.2.55",
		"apiroot-sensitive",
		"upf-sub-sensitive",
	} {
		if bytes.Contains(logOutput.Bytes(), []byte(sentinel)) {
			t.Fatalf("log output leaked sensitive sentinel %q: %s", sentinel, logOutput.String())
		}
	}
}

func newTestEventExposureProcessor(t *testing.T, deps EventExposureDependencies) *Processor {
	t.Helper()
	processor, err := NewProcessorWithEventExposureDependencies(&fakeProcessorSMF{
		context: &smf_context.SMFContext{NfInstanceID: "smf-instance"},
	}, deps)
	if err != nil {
		t.Fatalf("NewProcessorWithEventExposureDependencies failed: %v", err)
	}
	return processor
}

func validEventExposureCreateRequest() EventExposureCreateRequest {
	return EventExposureCreateRequest{
		Supi:                  "imsi-001010000000001",
		NotifID:               "correlation-1",
		NotifURI:              "https://nwdaf.example.com/nsmf",
		BundledEventNotifyURI: "https://nwdaf.example.com/upf",
		MeasurementTypes:      []models.UpfMeasurementType{models.UpfMeasurementType_VOLUME_MEASUREMENT},
		ReportingPeriod:       10,
	}
}

func validEventExposureTarget() smf_context.EventExposureTarget {
	return smf_context.EventExposureTarget{
		UPFName:        "upf-1",
		UPFID:          "upf-id-1",
		APIroot:        "https://upf.example.com",
		ServiceBaseURL: "https://upf.example.com/nupf-ee/v1",
		UEIPAddress:    net.ParseIP("192.0.2.1").To4(),
		Dnn:            "internet",
		PDUSessionID:   1,
	}
}

type fakeProcessorSMF struct {
	context *smf_context.SMFContext
}

func (f *fakeProcessorSMF) SetLogEnable(bool)                {}
func (f *fakeProcessorSMF) SetLogLevel(string)               {}
func (f *fakeProcessorSMF) SetReportCaller(bool)             {}
func (f *fakeProcessorSMF) Start()                           {}
func (f *fakeProcessorSMF) Terminate()                       {}
func (f *fakeProcessorSMF) Context() *smf_context.SMFContext { return f.context }
func (f *fakeProcessorSMF) Config() *factory.Config          { return nil }
func (f *fakeProcessorSMF) Consumer() *consumer.Consumer     { return nil }

type fakeEventExposureResolver struct {
	target smf_context.EventExposureTarget
	err    error
}

func (f fakeEventExposureResolver) ResolveEventExposureTarget(
	context.Context,
	string,
	smf_context.EventExposureSelectors,
) (smf_context.EventExposureTarget, error) {
	return f.target, f.err
}

type countingEventExposureResolver struct {
	calls int
}

func (f *countingEventExposureResolver) ResolveEventExposureTarget(
	context.Context,
	string,
	smf_context.EventExposureSelectors,
) (smf_context.EventExposureTarget, error) {
	f.calls++
	return smf_context.EventExposureTarget{}, errors.New("unexpected resolver call")
}

type fakeEventExposureConsumer struct {
	createCalls              int
	deleteCalls              int
	createResult             smf_context.NupfCreateResult
	createErr                error
	deleteErr                error
	lastCreateRequest        models.UpfCreateEventSubscription
	lastCreateTarget         smf_context.EventExposureTarget
	lastDeleteTarget         smf_context.EventExposureTarget
	lastDeleteSubscriptionID string
}

func (f *fakeEventExposureConsumer) CreateSubscription(
	_ context.Context,
	target smf_context.EventExposureTarget,
	request models.UpfCreateEventSubscription,
) (smf_context.NupfCreateResult, error) {
	f.createCalls++
	f.lastCreateTarget = target
	f.lastCreateRequest = request
	if f.createErr != nil {
		return smf_context.NupfCreateResult{}, f.createErr
	}
	return f.createResult, nil
}

func (f *fakeEventExposureConsumer) DeleteSubscription(
	_ context.Context,
	target smf_context.EventExposureTarget,
	subscriptionID string,
) error {
	f.deleteCalls++
	f.lastDeleteTarget = target
	f.lastDeleteSubscriptionID = subscriptionID
	return f.deleteErr
}

type failingEventExposureRepository struct {
	err        error
	storeCalls int
}

func (f *failingEventExposureRepository) Store(smf_context.EventExposureSubscription) error {
	f.storeCalls++
	return f.err
}

func (f *failingEventExposureRepository) Get(string) (smf_context.EventExposureSubscription, bool) {
	return smf_context.EventExposureSubscription{}, false
}

func (f *failingEventExposureRepository) ClaimDelete(string) (smf_context.EventExposureSubscription, bool) {
	return smf_context.EventExposureSubscription{}, false
}

type fixedUUIDGenerator struct {
	ids  []string
	next int
}

func (f *fixedUUIDGenerator) NewString() string {
	if len(f.ids) == 0 {
		return "sub-id"
	}
	if f.next >= len(f.ids) {
		return f.ids[len(f.ids)-1]
	}
	id := f.ids[f.next]
	f.next++
	return id
}
