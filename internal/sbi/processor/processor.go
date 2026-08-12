package processor

import (
	"context"

	"github.com/google/uuid"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/app"
)

const (
	CONTEXT_NOT_FOUND = "CONTEXT_NOT_FOUND"
)

type ProcessorSmf interface {
	app.App

	Consumer() *consumer.Consumer
}

type Processor struct {
	ProcessorSmf

	eventExposure EventExposureDependencies
}

func NewProcessor(smf ProcessorSmf) (*Processor, error) {
	return NewProcessorWithEventExposureDependencies(smf, EventExposureDependencies{})
}

func NewProcessorWithEventExposureDependencies(
	smf ProcessorSmf,
	deps EventExposureDependencies,
) (*Processor, error) {
	if deps.Repository == nil {
		deps.Repository = smf_context.NewEventExposureRepository()
	}
	if deps.Resolver == nil {
		deps.Resolver = smf_context.NewEventExposureTargetResolver()
	}
	if deps.UUIDGenerator == nil {
		deps.UUIDGenerator = uuidGenerator{}
	}

	return &Processor{
		ProcessorSmf:  smf,
		eventExposure: deps,
	}, nil
}

type EventExposureRepository interface {
	Store(smf_context.EventExposureSubscription) error
	Get(id string) (smf_context.EventExposureSubscription, bool)
	ClaimDelete(id string) (smf_context.EventExposureSubscription, bool)
}

type EventExposureResolver interface {
	ResolveEventExposureTarget(
		ctx context.Context,
		supi string,
		selectors smf_context.EventExposureSelectors,
	) (smf_context.EventExposureTarget, error)
}

type NupfEventExposureConsumer interface {
	CreateSubscription(
		ctx context.Context,
		target smf_context.EventExposureTarget,
		request models.UpfCreateEventSubscription,
	) (smf_context.NupfCreateResult, error)
	DeleteSubscription(
		ctx context.Context,
		target smf_context.EventExposureTarget,
		subscriptionID string,
	) error
}

type UUIDGenerator interface {
	NewString() string
}

type EventExposureDependencies struct {
	Repository    EventExposureRepository
	Resolver      EventExposureResolver
	NupfConsumer  NupfEventExposureConsumer
	UUIDGenerator UUIDGenerator
}

type uuidGenerator struct{}

func (uuidGenerator) NewString() string {
	return uuid.NewString()
}
