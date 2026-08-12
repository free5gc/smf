package context

import (
	"errors"
	"net"
	"testing"

	"github.com/free5gc/openapi/models"
)

func TestEventExposureRepositoryCopiesStoredRecords(t *testing.T) {
	repository := NewEventExposureRepository()
	pduSessionID := int32(0)
	dnn := "internet"
	measurementTypes := []models.UpfMeasurementType{models.UpfMeasurementType_VOLUME_MEASUREMENT}
	ip := net.ParseIP("192.0.2.1").To4()

	subscription := EventExposureSubscription{
		ID: "sub-1",
		Selectors: EventExposureSelectors{
			PDUSessionID: &pduSessionID,
			Dnn:          &dnn,
			Snssai:       &models.Snssai{Sst: 1, Sd: "010203"},
		},
		MeasurementTypes: measurementTypes,
		Target: EventExposureTarget{
			UEIPAddress: ip,
			Snssai:      &models.Snssai{Sst: 1, Sd: "010203"},
		},
	}
	if err := repository.Store(subscription); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	pduSessionID = 9
	dnn = "mutated"
	measurementTypes[0] = models.UpfMeasurementType_THROUGHPUT_MEASUREMENT
	ip[0] = 203
	subscription.Selectors.Snssai.Sd = "ffffff"
	subscription.Target.Snssai.Sd = "ffffff"

	stored, ok := repository.Get("sub-1")
	if !ok {
		t.Fatal("stored subscription not found")
	}
	if stored.Selectors.PDUSessionID == nil || *stored.Selectors.PDUSessionID != 0 {
		t.Fatalf("PDUSessionID alias detected: %+v", stored.Selectors.PDUSessionID)
	}
	if stored.Selectors.Dnn == nil || *stored.Selectors.Dnn != "internet" {
		t.Fatalf("Dnn alias detected: %+v", stored.Selectors.Dnn)
	}
	if stored.MeasurementTypes[0] != models.UpfMeasurementType_VOLUME_MEASUREMENT {
		t.Fatalf("measurement alias detected: %v", stored.MeasurementTypes)
	}
	if !stored.Target.UEIPAddress.Equal(net.ParseIP("192.0.2.1")) {
		t.Fatalf("IP alias detected: %v", stored.Target.UEIPAddress)
	}
	if stored.Selectors.Snssai.Sd != "010203" || stored.Target.Snssai.Sd != "010203" {
		t.Fatalf("S-NSSAI alias detected: %+v %+v", stored.Selectors.Snssai, stored.Target.Snssai)
	}
}

func TestEventExposureRepositoryCollisionAndClaimDelete(t *testing.T) {
	repository := NewEventExposureRepository()
	subscription := EventExposureSubscription{ID: "sub-1"}
	if err := repository.Store(subscription); err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if err := repository.Store(subscription); !errors.Is(err, ErrEventExposureSubscriptionIDCollision) {
		t.Fatalf("expected collision, got %v", err)
	}

	if _, ok := repository.ClaimDelete("sub-1"); !ok {
		t.Fatal("ClaimDelete failed")
	}
	if _, ok := repository.ClaimDelete("sub-1"); ok {
		t.Fatal("ClaimDelete should own the record only once")
	}
}
