package processor

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

const unknownPTITestSupi = "imsi-208930000000262"

func sendCreateWithPTI(t *testing.T, p *Processor, pti uint8) *httptest.ResponseRecorder {
	t.Helper()
	n1, err := (&message.PDUSessEstReq{
		PDUSessId: 5, PTI: pti,
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
			Supi: unknownPTITestSupi, PduSessionId: 5, Dnn: psiMismatchTestDNN,
			SNssai:         &models.Snssai{Sst: 1, Sd: "010203"},
			ServingNetwork: &models.PlmnIdNid{Mcc: "208", Mnc: "93"},
			Guami:          &models.Guami{PlmnId: &models.PlmnIdNid{Mcc: "208", Mnc: "93"}, AmfId: "cafe00"},
		},
		BinaryDataN1SmMessage: &multipart.RelatedContent{ContentID: "GSM_NAS", Content: n1},
	}, nil)
	return rec
}

func sendUpdateWithN1(t *testing.T, p *Processor, sm *smf_context.SMContext, gsm message.GSMMessage,
) *httptest.ResponseRecorder {
	t.Helper()
	n1, err := gsm.MarshalBinary()
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	p.HandlePDUSessionSMContextUpdate(c, models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			N1SmMsg: &models.RefToBinaryData{ContentId: "N1SmMsg"},
		},
		BinaryDataN1SmMessage: &multipart.RelatedContent{ContentID: "N1SmMsg", Content: n1},
	}, sm.Ref)
	return rec
}

// requireStatus81 checks a 403 N1_SM_ERROR that carries a 5GSM STATUS with cause #81 for the unassigned PTI, its
// JSON reference naming the part's Content-ID (TS 29.502 6.1.2.4).
func requireStatus81(t *testing.T, rec *httptest.ResponseRecorder, ref *models.RefToBinaryData,
	part *multipart.RelatedContent,
) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.NotNil(t, part, "no N1 5GSM STATUS part")
	require.NotNil(t, ref, "no n1SmMsg reference to the N1 part")
	require.Equal(t, part.ContentID, ref.ContentId, "n1SmMsg does not name the N1 part")
	gsm, err := message.ParseGSM(part.Content)
	require.NoError(t, err)
	status, ok := gsm.(*message.Status5GSM)
	require.True(t, ok, "N1 part is %T", gsm)
	require.Equal(t, nasie.Cause5GSM_InvalidPTIValue, status.Cause5GSM.Value)
	require.Equal(t, uint8(0), status.PTI)
	require.Equal(t, uint8(5), status.PDUSessId)
}

// TS 24.501 7.3.1 d): a PDU SESSION ESTABLISHMENT REQUEST with the reserved PTI is ignored: no SM context, and no
// N1 SM message for the UE.
func TestCreateSMContextReservedPTIIsIgnored(t *testing.T) {
	p := newPSIMismatchTestProcessor(t)
	rec := sendCreateWithPTI(t, p, 255)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Header().Get("Content-Type"), "multipart", "an N1 SM message for the UE")
	require.Nil(t, smf_context.GetSMContextById(unknownPTITestSupi, 5))
}

// TS 24.501 7.3.1 c): a PDU SESSION ESTABLISHMENT REQUEST with the unassigned PTI is answered with a 5GSM STATUS,
// cause #81 "invalid PTI value".
func TestCreateSMContextUnassignedPTIGetsStatus(t *testing.T) {
	p := newPSIMismatchTestProcessor(t)
	rec := sendCreateWithPTI(t, p, 0)

	var rsp models.PostSmContextsResponse403
	require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
	requireStatus81(t, rec, rsp.JsonData.N1SmMsg, rsp.BinaryDataN1SmMessage)
	require.Nil(t, smf_context.GetSMContextById(unknownPTITestSupi, 5))
}

// TS 24.501 7.3.1 c), d): a PDU SESSION MODIFICATION REQUEST or RELEASE REQUEST with the unassigned PTI gets a
// 5GSM STATUS #81, a 5GSM message with the reserved PTI is ignored, and the session stays.
func TestUpdateSMContextUnknownPTIKeepsSession(t *testing.T) {
	p := newPSIMismatchTestProcessor(t)
	sm := smf_context.NewSMContext(unknownPTITestSupi, 5)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: unknownPTITestSupi, PduSessionId: 5, Dnn: psiMismatchTestDNN,
	}
	sm.SetState(smf_context.Active)

	for _, gsm := range []message.GSMMessage{
		&message.PDUSessModReq{PDUSessId: 5, PTI: 0},
		&message.PDUSessRelReq{PDUSessId: 5, PTI: 0},
	} {
		rec := sendUpdateWithN1(t, p, sm, gsm)
		var rsp models.UpdateSmContextResponse403
		require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
		requireStatus81(t, rec, rsp.JsonData.N1SmMsg, rsp.BinaryDataN1SmMessage)
		require.Equal(t, smf_context.Active, sm.State())
	}

	rec := sendUpdateWithN1(t, p, sm, &message.PDUSessRelReq{PDUSessId: 5, PTI: 255})
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Header().Get("Content-Type"), "multipart", "an N1 SM message for the UE")
	require.Equal(t, smf_context.Active, sm.State())
	require.NotNil(t, smf_context.GetSMContextByRef(sm.Ref))
}
