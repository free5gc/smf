package context

import (
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/free5gc/openapi/models"
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

type EventExposureNotification struct {
	*models.Smf_EvtExpos_NsmfEventExposureNotification

	Uri string
}

func (c *SMContext) BuildUpPathChgEventExposureNotification(
	chgEvent *models.Pcf_SMPolCtrl_UpPathChgEvent,
	srcRoute, tgtRoute *models.RouteToLocation,
) {
	if chgEvent == nil {
		return
	}
	if chgEvent.NotificationUri == "" {
		c.Log.Warnf("No NotificationUri [%s]", chgEvent.NotificationUri)
		return
	}

	en := models.Smf_EvtExpos_EventNotification{
		Event:            models.Smf_EvtExpos_SmfEvent_UP_PATH_CH,
		SourceTraRouting: srcRoute,
		TargetTraRouting: tgtRoute,
	}
	if srcRoute.Dnai != tgtRoute.Dnai {
		en.SourceDnai = srcRoute.Dnai
		en.TargetDnai = tgtRoute.Dnai
	}
	// TODO: sourceUeIpv4Addr, sourceUeIpv6Prefix, targetUeIpv4Addr, targetUeIpv6Prefix

	k := chgEvent.NotificationUri + chgEvent.NotifCorreId
	if strings.Contains(string(chgEvent.DnaiChgType), "EARLY") {
		en.DnaiChgType = models.DnaiChangeType("EARLY")
		v, ok := c.UpPathChgEarlyNotification[k]
		if ok {
			v.EventNotifs = append(v.EventNotifs, en)
		} else {
			c.UpPathChgEarlyNotification[k] = newEventExposureNotification(
				chgEvent.NotificationUri, chgEvent.NotifCorreId, &en)
		}
	}
	if strings.Contains(string(chgEvent.DnaiChgType), "LATE") {
		en.DnaiChgType = models.DnaiChangeType("LATE")
		v, ok := c.UpPathChgLateNotification[k]
		if ok {
			v.EventNotifs = append(v.EventNotifs, en)
		} else {
			c.UpPathChgLateNotification[k] = newEventExposureNotification(
				chgEvent.NotificationUri, chgEvent.NotifCorreId, &en)
		}
	}
}

func newEventExposureNotification(
	uri, id string,
	en *models.Smf_EvtExpos_EventNotification,
) *EventExposureNotification {
	return &EventExposureNotification{
		Smf_EvtExpos_NsmfEventExposureNotification: &models.Smf_EvtExpos_NsmfEventExposureNotification{
			NotifId:     id,
			EventNotifs: []models.Smf_EvtExpos_EventNotification{*en},
		},
		Uri: uri,
	}
}

type NotifCallback func(uri string,
	notification *models.Smf_EvtExpos_NsmfEventExposureNotification)

func (c *SMContext) SendUpPathChgNotification(chgType string, notifCb NotifCallback) {
	var notifications map[string]*EventExposureNotification
	switch chgType {
	case "EARLY":
		notifications = c.UpPathChgEarlyNotification
	case "LATE":
		notifications = c.UpPathChgLateNotification
	default:
		return
	}
	for k, n := range notifications {
		c.Log.Infof("Send UpPathChg Event Exposure Notification [%s][%s] to NEF/AF", chgType, n.NotifId)
		go notifCb(n.Uri, n.Smf_EvtExpos_NsmfEventExposureNotification)
		delete(notifications, k)
	}
}
