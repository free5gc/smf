package context_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/context"
)

// A stale SM context must not remove the canonical reference of the SM context that
// replaced it, otherwise the newer one becomes unreachable and leaks its UE IP address.
func TestRemoveSMContextKeepsCanonicalRefOfNewerSMContext(t *testing.T) {
	initConfig()

	const (
		supi         = "imsi-001016100000041"
		pduSessionID = int32(1)
	)

	oldSMContext := context.NewSMContext(supi, pduSessionID)
	require.NotNil(t, oldSMContext)
	newSMContext := context.NewSMContext(supi, pduSessionID)
	require.NotNil(t, newSMContext)
	require.NotEqual(t, oldSMContext.Ref, newSMContext.Ref)

	// NewSMContext has already handed the canonical name over to newSMContext.
	require.Equal(t, newSMContext, context.GetSMContextById(supi, pduSessionID))

	context.RemoveSMContext(oldSMContext.Ref)

	require.Nil(t, context.GetSMContextByRef(oldSMContext.Ref))
	require.Equal(t, newSMContext, context.GetSMContextById(supi, pduSessionID),
		"removing the stale SM context must not detach the newer one from its canonical name")

	// The owner of the canonical name still cleans it up.
	context.RemoveSMContext(newSMContext.Ref)
	require.Nil(t, context.GetSMContextById(supi, pduSessionID))
}

func TestSMContextBeginChargingReleaseIsOneShot(t *testing.T) {
	smContext := &context.SMContext{}
	if !smContext.BeginChargingRelease() {
		t.Fatal("first BeginChargingRelease() = false, want true")
	}
	if smContext.BeginChargingRelease() {
		t.Fatal("second BeginChargingRelease() = true, want false")
	}
}

func TestChargingReleaseStateTracksOutcome(t *testing.T) {
	failure := errors.New("CHF settlement failed")
	for _, tc := range []struct {
		name  string
		err   error
		state context.ChargingReleaseState
	}{
		{name: "success", state: context.ChargingReleaseSucceeded},
		{name: "failure", err: failure, state: context.ChargingReleaseFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			smContext := &context.SMContext{}
			state, lastErr := smContext.ChargingReleaseResult()
			require.Equal(t, context.ChargingReleaseNotStarted, state)
			require.NoError(t, lastErr)

			require.True(t, smContext.BeginChargingRelease())
			state, lastErr = smContext.ChargingReleaseResult()
			require.Equal(t, context.ChargingReleaseInProgress, state)
			require.NoError(t, lastErr, "pending state must not be encoded as a stored error")
			require.False(t, smContext.BeginChargingRelease(), "only one sender is allowed")

			smContext.FinishChargingRelease(tc.err)
			state, lastErr = smContext.ChargingReleaseResult()
			require.Equal(t, tc.state, state)
			require.ErrorIs(t, lastErr, tc.err)
			require.False(t, smContext.BeginChargingRelease(), "terminal states must not send again")
		})
	}
}

func TestPendingChargingReleaseOwnsItsSnapshot(t *testing.T) {
	smContext := &context.SMContext{}
	timestamp := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	request := &models.Chf_ConvCharging_ChargingDataRequest{
		SubscriberIdentifier:     "imsi-208930000001226",
		InvocationSequenceNumber: 3,
		InvocationTimeStamp:      &timestamp,
		MultipleUnitUsage: []models.Chf_ConvCharging_MultipleUnitUsage{{
			RatingGroup: 10, UPFID: "upf-a",
			UsedUnitContainer: []models.Chf_ConvCharging_UsedUnitContainer{{TotalVolume: 40}},
		}},
	}
	require.NoError(t, smContext.StorePendingChargingRelease("original-ref", request))
	require.Error(t, smContext.StorePendingChargingRelease("replacement-ref", request),
		"a pending settlement must not be overwritten")

	request.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume = 999
	timestamp = timestamp.Add(time.Hour)
	ref, snapshot, err := smContext.PendingChargingRelease()
	require.NoError(t, err)
	require.Equal(t, "original-ref", ref)
	require.Equal(t, int32(40), snapshot.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume)
	require.Equal(t, 12, snapshot.InvocationTimeStamp.Hour())

	snapshot.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume = 123
	snapshot.InvocationSequenceNumber = 99
	_, again, err := smContext.PendingChargingRelease()
	require.NoError(t, err)
	require.Equal(t, int32(40), again.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume)
	require.Equal(t, int32(3), again.InvocationSequenceNumber)
}
