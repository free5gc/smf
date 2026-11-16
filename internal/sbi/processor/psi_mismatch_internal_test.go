package processor

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

const (
	psiMismatchTestSupi = "imsi-208930000000261"
	psiMismatchTestDNN  = "internet"
)

func newPSIMismatchTestProcessor(t *testing.T) *Processor {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info:          &factory.Info{Description: "PSI mismatch regression"},
		Configuration: &factory.Configuration{Sbi: &factory.Sbi{}},
	}))
	return newEventExposureProcessor(t)
}

// sendPSIMismatchCreate posts an SM Context Create whose UL NAS TRANSPORT PDU
// session ID (outer) differs from the 5GSM PDU session identity (inner).
func sendPSIMismatchCreate(t *testing.T, p *Processor, outer int32, inner uint8) *httptest.ResponseRecorder {
	t.Helper()
	n1, err := (&message.PDUSessEstReq{
		PDUSessId: inner, PTI: 3,
		PDUSessType: &nasie.PDUSessType{Value: nasie.PDUSessType_IPv4},
		IntegrityProtectionMaxDataRate: &nasie.IntegrityProtectionMaxDataRate{
			Uplink: 0xff, Downlink: 0xff,
		},
	}).MarshalBinary()
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	p.HandlePDUSessionSMContextCreate(c, models.PostSmContextsRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextCreateData{
			Supi: psiMismatchTestSupi, PduSessionId: outer, Dnn: psiMismatchTestDNN,
			SNssai:         &models.Snssai{Sst: 1, Sd: "010203"},
			ServingNetwork: &models.PlmnIdNid{Mcc: "208", Mnc: "93"},
			Guami:          &models.Guami{PlmnId: &models.PlmnIdNid{Mcc: "208", Mnc: "93"}, AmfId: "cafe00"},
		},
		BinaryDataN1SmMessage: &multipart.RelatedContent{ContentID: "GSM_NAS", Content: n1},
	}, nil)
	return rec
}

// The reject must reach the UE: 403 N1_SM_ERROR with the PDU SESSION
// ESTABLISHMENT REJECT (#43) as an N1 part (TS 29.502 5.2.2.2.1 step 2b).
func TestCreateSMContextPSIMismatchIsRejected(t *testing.T) {
	p := newPSIMismatchTestProcessor(t)
	rec := sendPSIMismatchCreate(t, p, 6, 7)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	var rsp models.PostSmContextsResponse403
	require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
	require.Equal(t, "N1_SM_ERROR", rsp.JsonData.Error.Cause)
	require.NotNil(t, rsp.BinaryDataN1SmMessage, "no N1 PDU SESSION ESTABLISHMENT REJECT part")
	gsm, err := message.ParseGSM(rsp.BinaryDataN1SmMessage.Content)
	require.NoError(t, err)
	reject, ok := gsm.(*message.PDUSessEstRej)
	require.True(t, ok, "N1 part is %T", gsm)
	require.Equal(t, nasie.Cause5GSM_InvalidPDUSessId, reject.Cause5GSM.Value)
	require.Equal(t, uint8(7), reject.PDUSessId, "the reject answers the 5GSM message as received")
	require.Equal(t, uint8(3), reject.PTI)

	require.Nil(t, smf_context.GetSMContextById(psiMismatchTestSupi, 6))
	require.Nil(t, smf_context.GetSMContextById(psiMismatchTestSupi, 7))
}

// A mismatching request must neither release nor unregister an existing
// session that uses the outer or the inner identity.
func TestCreateSMContextPSIMismatchKeepsExistingSessions(t *testing.T) {
	p := newPSIMismatchTestProcessor(t)
	existing := map[int32]*smf_context.SMContext{}
	for _, psi := range []int32{5, 7} {
		sm := smf_context.NewSMContext(psiMismatchTestSupi, psi)
		sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
			Supi: psiMismatchTestSupi, PduSessionId: psi, Dnn: psiMismatchTestDNN,
		}
		sm.SetState(smf_context.Active)
		existing[psi] = sm
		t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	}

	rec := sendPSIMismatchCreate(t, p, 5, 7)

	time.Sleep(100 * time.Millisecond) // context removal runs asynchronously
	for psi, sm := range existing {
		got := smf_context.GetSMContextById(psiMismatchTestSupi, psi)
		require.NotNil(t, got, "session PSI %d is no longer resolvable by (SUPI, PSI)", psi)
		require.Equal(t, sm.Ref, got.Ref, "PSI %d", psi)
		require.NotNil(t, smf_context.GetSMContextByRef(sm.Ref), "session PSI %d was removed", psi)
		require.Equal(t, smf_context.Active, sm.State(), "PSI %d", psi)
	}
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}
