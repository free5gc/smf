//nolint:testpackage // The collision primitive and minimal record shape require package-local verification.
package context

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestEventExposureSubscriptionLifecycle(t *testing.T) {
	localID := StoreEventExposureSubscription("https://upf.example.com", "downstream-1")
	t.Cleanup(func() {
		if record, ok := GetEventExposureSubscription(localID); ok {
			RemoveEventExposureSubscription(localID, record)
		}
	})

	parsedID, err := uuid.Parse(localID)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(4), parsedID.Version())

	record, ok := GetEventExposureSubscription(localID)
	require.True(t, ok)
	apiRoot, downstreamID := record.Coordinates()
	require.Equal(t, "https://upf.example.com", apiRoot)
	require.Equal(t, "downstream-1", downstreamID)
	require.True(t, IsCurrentEventExposureSubscription(localID, record))
	require.True(t, RemoveEventExposureSubscription(localID, record))
	_, ok = GetEventExposureSubscription(localID)
	require.False(t, ok)
}

func TestEventExposureSubscriptionRecordIsMinimal(t *testing.T) {
	recordType := reflect.TypeOf(EventExposureSubscription{})
	require.Equal(t, 3, recordType.NumField())
	require.Equal(t, "nupfAPIRoot", recordType.Field(0).Name)
	require.Equal(t, "nupfSubscriptionID", recordType.Field(1).Name)
	require.Equal(t, "deleteMu", recordType.Field(2).Name)
}

func TestEventExposureSubscriptionCollisionKeepsExistingRecord(t *testing.T) {
	const localID = "collision"
	first := &EventExposureSubscription{nupfAPIRoot: "https://first.example.com", nupfSubscriptionID: "first"}
	second := &EventExposureSubscription{nupfAPIRoot: "https://second.example.com", nupfSubscriptionID: "second"}
	t.Cleanup(func() {
		eventExposureSubscriptions.Delete(localID)
	})

	require.True(t, storeEventExposureSubscription(localID, first))
	require.False(t, storeEventExposureSubscription(localID, second))
	stored, ok := GetEventExposureSubscription(localID)
	require.True(t, ok)
	require.Same(t, first, stored)
}

func TestEventExposureSubscriptionConcurrentDeleteOwnership(t *testing.T) {
	localID := StoreEventExposureSubscription("https://upf.example.com", "downstream-1")
	t.Cleanup(func() {
		if record, ok := GetEventExposureSubscription(localID); ok {
			RemoveEventExposureSubscription(localID, record)
		}
	})

	var downstreamCalls atomic.Int32
	var successfulOwners atomic.Int32
	var waitGroup sync.WaitGroup
	start := make(chan struct{})
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			record, ok := GetEventExposureSubscription(localID)
			if !ok {
				return
			}
			record.LockDelete()
			defer record.UnlockDelete()
			if !IsCurrentEventExposureSubscription(localID, record) {
				return
			}
			downstreamCalls.Add(1)
			if RemoveEventExposureSubscription(localID, record) {
				successfulOwners.Add(1)
			}
		}()
	}

	close(start)
	waitGroup.Wait()
	require.Equal(t, int32(1), downstreamCalls.Load())
	require.Equal(t, int32(1), successfulOwners.Load())
}
