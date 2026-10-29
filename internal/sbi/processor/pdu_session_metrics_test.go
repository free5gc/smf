package processor_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	business_metrics "github.com/free5gc/smf/internal/metrics/business"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/internal/sbi/processor"
	PDUSession_errors "github.com/free5gc/smf/pkg/errors"
	"github.com/free5gc/smf/pkg/service"
	"github.com/free5gc/util/metrics/utils"
)

func TestHandlePDUSessionSMContextCreateCountsFailureCause(t *testing.T) {
	openapi.InterceptInnerHttp2Client(t, false)
	initConfig()
	initStubPFCP()

	business_metrics.GetPduSessionHandlerMetrics("free5gc", smf_context.CountSMContextsByState)
	business_metrics.EnablePduSessionMetrics()
	utils.EnableBusinessMetrics()

	mockSmf := service.NewMockSmfAppInterface(gomock.NewController(t))
	smfConsumer, err := consumer.NewConsumer(mockSmf)
	require.NoError(t, err)
	smfProcessor, err := processor.NewProcessor(mockSmf)
	require.NoError(t, err)
	service.SMF = mockSmf
	mockSmf.EXPECT().Context().Return(smf_context.GetSelf()).AnyTimes()
	mockSmf.EXPECT().Consumer().Return(smfConsumer).AnyTimes()

	validCreateData := func() *models.Smf_PDUSess_SmContextCreateData {
		return &models.Smf_PDUSess_SmContextCreateData{
			Supi:         "imsi-208930000000051",
			PduSessionId: 10,
			Dnn:          "internet-metrics",
			SNssai:       &models.Snssai{Sst: 1, Sd: "112232"},
			Guami: &models.Guami{
				PlmnId: &models.PlmnIdNid{Mcc: "208", Mnc: "93"},
				AmfId:  "cafe01",
			},
			AnType:         models.AccessType_3_GPP_ACCESS,
			ServingNetwork: &models.PlmnIdNid{Mcc: "208", Mnc: "93"},
		}
	}
	estReq := func(pti uint8) *multipart.RelatedContent {
		return &multipart.RelatedContent{
			ContentID: "N1_SM", Content: buildPDUSessionEstablishmentRequest(10, pti, nasie.PDUSessType_IPv4),
		}
	}

	noServingNetwork := validCreateData()
	noServingNetwork.ServingNetwork = nil
	unknownDnn := validCreateData()
	unknownDnn.Dnn = "not-configured"

	testCases := []struct {
		name    string
		request models.PostSmContextsRequestBody
		cause   string
	}{
		{
			name: "N1 message is not an establishment request",
			request: models.PostSmContextsRequestBody{
				BinaryDataN1SmMessage: &multipart.RelatedContent{
					ContentID: "N1_SM", Content: buildPDUSessionModificationRequest(10, 1),
				},
			},
			cause: PDUSession_errors.N1SmError.Cause,
		},
		{
			name:    "ServingNetwork is missing",
			request: models.PostSmContextsRequestBody{JsonData: noServingNetwork, BinaryDataN1SmMessage: estReq(4)},
			cause:   "MANDATORY_IE_MISSING",
		},
		{
			name:    "DNN is not configured",
			request: models.PostSmContextsRequestBody{JsonData: unknownDnn, BinaryDataN1SmMessage: estReq(5)},
			cause:   PDUSession_errors.DnnNotSupported.Cause,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			counter := business_metrics.PduSessionEstablishmentCounter.WithLabelValues(utils.FailureMetric, tc.cause)
			before := testutil.ToFloat64(counter)

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			smfProcessor.HandlePDUSessionSMContextCreate(c, tc.request, nil)

			require.InDelta(t, before+1, testutil.ToFloat64(counter), 0)
			// Let the asynchronous SM context removal of the reject path finish.
			time.Sleep(100 * time.Millisecond)
		})
	}
}
