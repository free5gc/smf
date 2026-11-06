package consumer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

func TestChargingRequestPreparationReusesOriginalRequest(t *testing.T) {
	for _, tc := range []struct {
		name        string
		requestType smf_context.RequestType
	}{
		{name: "update", requestType: smf_context.CHARGING_UPDATE},
		{name: "release", requestType: smf_context.CHARGING_RELEASE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewConsumer(&nupfTestApp{smfContext: &smf_context.SMFContext{NfInstanceID: "original-smf"}})
			require.NoError(t, err)
			sm := &smf_context.SMContext{
				ChargingDataRef: "original-ref",
				ChargingID:      42,
				Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{
					Supi: "imsi-208930000001226", SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
				},
			}
			usage := []models.Chf_ConvCharging_MultipleUnitUsage{{
				RatingGroup: 10, UPFID: "upf-a",
				UsedUnitContainer: []models.Chf_ConvCharging_UsedUnitContainer{{TotalVolume: 40}},
			}}
			request, ref, err := c.prepareConvergedChargingRequest(sm, tc.requestType, usage)
			require.NoError(t, err)
			require.Equal(t, "original-ref", ref)
			require.Equal(t, int32(1), request.InvocationSequenceNumber)
			require.False(t, request.RetransmissionIndicator)
			original, err := json.Marshal(request)
			require.NoError(t, err)

			// All of these values were inputs to the first request. Preparing the same
			// pending operation must ignore their new values and preserve its identity.
			sm.ChargingDataRef = "changed-ref"
			sm.Supi = "imsi-changed"
			sm.SNssai.Sd = "ffffff"
			usage[0].UsedUnitContainer[0].TotalVolume = 999
			request.InvocationSequenceNumber = 99
			request.NfConsumerIdentification.NFName = "changed-smf"
			request, ref, err = c.prepareConvergedChargingRequest(sm, tc.requestType, usage)
			require.NoError(t, err)
			require.Equal(t, "original-ref", ref)
			require.True(t, request.RetransmissionIndicator)
			// Only the outgoing retransmission marker may differ from the frozen identity.
			request.RetransmissionIndicator = false
			replayed, err := json.Marshal(request)
			require.NoError(t, err)
			require.JSONEq(t, string(original), string(replayed), "the full pending request must remain fixed")
			_, frozen, err := sm.PendingChargingRequest(tc.requestType)
			require.NoError(t, err)
			require.NotNil(t, frozen)
			require.False(t, frozen.RetransmissionIndicator, "retries must not mutate the stored snapshot")
			request, _, err = c.prepareConvergedChargingRequest(sm, tc.requestType, nil)
			require.NoError(t, err)
			require.True(t, request.RetransmissionIndicator, "every subsequent retry is marked")
			require.Equal(t, int32(2), sm.NextChargingSequenceNumber(), "reusing a request must not allocate another sequence")
		})
	}
}
