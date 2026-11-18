package processor

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

// TS 24.501 6.4.1.4.1: a request for an Ethernet PDU session that the DNN does not allow is rejected with #28
// "unknown PDU session type", not #50.
func TestEstablishmentRequestOfUnsupportedTypeIsRejectedWith28(t *testing.T) {
	smContext := &smf_context.SMContext{
		Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{Dnn: "internet"},
	}
	smContext.DnnConfiguration.PduSessionTypes = &models.Udm_SDM_PduSessionTypes{
		AllowedSessionTypes: []models.PduSessionType{models.PduSessionType_IPV4},
	}

	err := HandlePDUSessionEstablishmentRequest(smContext, &message.PDUSessEstReq{
		PDUSessId: 5, PTI: 1,
		PDUSessType: &nasie.PDUSessType{Value: nasie.PDUSessType_Ethernet},
	})

	var gsmError *GSMError
	require.True(t, errors.As(err, &gsmError), "the request is not rejected: %v", err)
	require.Equal(t, nasie.Cause5GSM_UnknownPDUSessType, gsmError.GSMCause)
}
