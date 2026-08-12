package context

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/free5gc/openapi/models"
)

var ErrEventExposureSubscriptionIDCollision = errors.New("event exposure subscription id collision")

type EventExposureSelectors struct {
	PDUSessionID *int32
	Dnn          *string
	Snssai       *models.Snssai
}

type EventExposureTarget struct {
	UPFName        string
	UPFID          string
	APIroot        string
	ServiceBaseURL string
	UEIPAddress    net.IP
	Dnn            string
	Snssai         *models.Snssai
	PDUSessionID   int32
}

type NupfCreateResult struct {
	SubscriptionID    string
	ValidatedLocation string
	CreateRequestURI  string
	Response          models.UpfCreatedEventSubscription
	StatusCode        int
}

type EventExposureSubscription struct {
	ID                    string
	Supi                  string
	Selectors             EventExposureSelectors
	NotifID               string
	NotifURI              string
	BundledEventNotifyURI string
	MeasurementTypes      []models.UpfMeasurementType
	ReportingPeriod       int32
	Granularity           models.UpfGranularityOfMeasurement
	CreatedAt             time.Time

	Target             EventExposureTarget
	NupfSubscriptionID string
	NupfLocation       string
}

type EventExposureRepository struct {
	mu            sync.RWMutex
	subscriptions map[string]EventExposureSubscription
}

func NewEventExposureRepository() *EventExposureRepository {
	return &EventExposureRepository{
		subscriptions: make(map[string]EventExposureSubscription),
	}
}

func (r *EventExposureRepository) Store(subscription EventExposureSubscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.subscriptions[subscription.ID]; ok {
		return ErrEventExposureSubscriptionIDCollision
	}
	r.subscriptions[subscription.ID] = cloneEventExposureSubscription(subscription)
	return nil
}

func (r *EventExposureRepository) Get(id string) (EventExposureSubscription, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	subscription, ok := r.subscriptions[id]
	if !ok {
		return EventExposureSubscription{}, false
	}
	return cloneEventExposureSubscription(subscription), true
}

func (r *EventExposureRepository) ClaimDelete(id string) (EventExposureSubscription, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	subscription, ok := r.subscriptions[id]
	if !ok {
		return EventExposureSubscription{}, false
	}
	delete(r.subscriptions, id)
	return cloneEventExposureSubscription(subscription), true
}

func cloneEventExposureSubscription(subscription EventExposureSubscription) EventExposureSubscription {
	subscription.Selectors = cloneEventExposureSelectors(subscription.Selectors)
	subscription.MeasurementTypes = append([]models.UpfMeasurementType(nil), subscription.MeasurementTypes...)
	subscription.Target = cloneEventExposureTarget(subscription.Target)
	return subscription
}

func cloneEventExposureSelectors(selectors EventExposureSelectors) EventExposureSelectors {
	if selectors.PDUSessionID != nil {
		pduSessionID := *selectors.PDUSessionID
		selectors.PDUSessionID = &pduSessionID
	}
	if selectors.Dnn != nil {
		dnn := *selectors.Dnn
		selectors.Dnn = &dnn
	}
	if selectors.Snssai != nil {
		selectors.Snssai = cloneModelSnssai(selectors.Snssai)
	}
	return selectors
}

func cloneEventExposureTarget(target EventExposureTarget) EventExposureTarget {
	target.UEIPAddress = append(net.IP(nil), target.UEIPAddress...)
	target.Snssai = cloneModelSnssai(target.Snssai)
	return target
}

func cloneModelSnssai(snssai *models.Snssai) *models.Snssai {
	if snssai == nil {
		return nil
	}
	return &models.Snssai{
		Sst: snssai.Sst,
		Sd:  snssai.Sd,
	}
}
