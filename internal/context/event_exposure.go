package context

import (
	"sync"

	"github.com/google/uuid"
)

var eventExposureSubscriptions sync.Map

// EventExposureSubscription stores only the coordinates needed to delete a
// downstream Nupf Event Exposure subscription and serializes Delete for it.
type EventExposureSubscription struct {
	nupfAPIRoot        string
	nupfSubscriptionID string
	deleteMu           sync.Mutex
}

// StoreEventExposureSubscription publishes a downstream subscription under a
// process-local Nsmf subscription ID.
func StoreEventExposureSubscription(nupfAPIRoot, nupfSubscriptionID string) string {
	record := &EventExposureSubscription{
		nupfAPIRoot:        nupfAPIRoot,
		nupfSubscriptionID: nupfSubscriptionID,
	}

	for {
		localID := uuid.New().String()
		if storeEventExposureSubscription(localID, record) {
			return localID
		}
	}
}

func storeEventExposureSubscription(localID string, record *EventExposureSubscription) bool {
	_, loaded := eventExposureSubscriptions.LoadOrStore(localID, record)
	return !loaded
}

// GetEventExposureSubscription returns the record for one local subscription.
func GetEventExposureSubscription(localID string) (*EventExposureSubscription, bool) {
	value, ok := eventExposureSubscriptions.Load(localID)
	if !ok {
		return nil, false
	}
	return value.(*EventExposureSubscription), true
}

// Coordinates returns the validated Nupf API root and downstream subscription ID.
func (s *EventExposureSubscription) Coordinates() (string, string) {
	return s.nupfAPIRoot, s.nupfSubscriptionID
}

// LockDelete serializes downstream Delete for this record.
func (s *EventExposureSubscription) LockDelete() {
	s.deleteMu.Lock()
}

// UnlockDelete releases this record's Delete ownership.
func (s *EventExposureSubscription) UnlockDelete() {
	s.deleteMu.Unlock()
}

// IsCurrentEventExposureSubscription re-checks that the local ID still maps to
// the same record after Delete ownership has been acquired.
func IsCurrentEventExposureSubscription(localID string, record *EventExposureSubscription) bool {
	current, ok := GetEventExposureSubscription(localID)
	return ok && current == record
}

// RemoveEventExposureSubscription removes the local ID only if it still maps
// to the record that completed downstream Delete.
func RemoveEventExposureSubscription(localID string, record *EventExposureSubscription) bool {
	return eventExposureSubscriptions.CompareAndDelete(localID, record)
}
