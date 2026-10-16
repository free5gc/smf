package context_test

import (
	"testing"

	"github.com/stretchr/testify/require"

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
