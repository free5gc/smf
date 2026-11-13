package context

import (
	"testing"

	"github.com/stretchr/testify/require"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/openapi/models"
)

// TS 24.501 6.4.1.4.1: the 5GSM cause of a PDU session type that the DNN does not allow.
func TestIsAllowedPDUSessionTypeCause(t *testing.T) {
	for _, tc := range []struct {
		name      string
		supported string                // the SMF's SupportedPDUSessionType
		allowed   models.PduSessionType // "": the DNN has no PDU session types
		requested uint8
		cause     uint8
	}{
		{
			name: "IPv6 where IPv4 only", allowed: models.PduSessionType_IPV4,
			requested: nasie.PDUSessType_IPv6, cause: nasie.Cause5GSM_PDUSessTypeIpv4OnlyAllowed,
		},
		{
			name: "IPv6 where Ethernet only", allowed: models.PduSessionType_ETHERNET,
			requested: nasie.PDUSessType_IPv6, cause: nasie.Cause5GSM_UnknownPDUSessType,
		},
		{
			name: "IPv4 where IPv6 only", allowed: models.PduSessionType_IPV6,
			requested: nasie.PDUSessType_IPv4, cause: nasie.Cause5GSM_PDUSessTypeIpv6OnlyAllowed,
		},
		{
			name: "IPv4 where Ethernet only", allowed: models.PduSessionType_ETHERNET,
			requested: nasie.PDUSessType_IPv4, cause: nasie.Cause5GSM_UnknownPDUSessType,
		},
		{
			name: "IPv4v6 where Ethernet only", allowed: models.PduSessionType_ETHERNET,
			requested: nasie.PDUSessType_IPv4v6, cause: nasie.Cause5GSM_UnknownPDUSessType,
		},
		{
			name: "Ethernet where IPv4 only", allowed: models.PduSessionType_IPV4,
			requested: nasie.PDUSessType_Ethernet, cause: nasie.Cause5GSM_UnknownPDUSessType,
		},
		{
			name: "Unstructured where IPv4v6", allowed: models.PduSessionType_IPV4_V6,
			requested: nasie.PDUSessType_Unstructured, cause: nasie.Cause5GSM_UnknownPDUSessType,
		},
		{
			name:      "IPv4 where the DNN has no PDU session types",
			requested: nasie.PDUSessType_IPv4, cause: nasie.Cause5GSM_UnknownPDUSessType,
		},
		{
			name: "IPv4 where IPv6 only and the SMF supports IPv4", supported: "IPv4", allowed: models.PduSessionType_IPV6,
			requested: nasie.PDUSessType_IPv4, cause: nasie.Cause5GSM_UnknownPDUSessType,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supported := GetSelf().SupportedPDUSessionType
			GetSelf().SupportedPDUSessionType = tc.supported
			t.Cleanup(func() { GetSelf().SupportedPDUSessionType = supported })

			smContext := &SMContext{
				Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{Dnn: "internet"},
			}
			if tc.allowed != "" {
				smContext.DnnConfiguration.PduSessionTypes = &models.Udm_SDM_PduSessionTypes{
					AllowedSessionTypes: []models.PduSessionType{tc.allowed},
				}
			}

			cause, err := smContext.IsAllowedPDUSessionType(tc.requested)
			require.Error(t, err)
			require.Equal(t, tc.cause, cause)
		})
	}
}

// TS 24.501 Table 9.11.4.11.1: the unused PDU session type values 000 and 110 are interpreted as "IPv4v6".
func TestIsAllowedPDUSessionTypeUnusedValue(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowed   models.PduSessionType
		requested uint8
		selected  uint8
		cause     uint8 // the 5GSM cause of the PDU SESSION ESTABLISHMENT ACCEPT
	}{
		{"000 where IPv4v6", models.PduSessionType_IPV4_V6, 0, nasie.PDUSessType_IPv4v6, 0},
		{"110 where IPv4v6", models.PduSessionType_IPV4_V6, 6, nasie.PDUSessType_IPv4v6, 0},
		{
			"110 where IPv4 only", models.PduSessionType_IPV4, 6,
			nasie.PDUSessType_IPv4, nasie.Cause5GSM_PDUSessTypeIpv4OnlyAllowed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supported := GetSelf().SupportedPDUSessionType
			GetSelf().SupportedPDUSessionType = "IPv4"
			t.Cleanup(func() { GetSelf().SupportedPDUSessionType = supported })

			smContext := &SMContext{
				Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{Dnn: "internet"},
			}
			smContext.DnnConfiguration.PduSessionTypes = &models.Udm_SDM_PduSessionTypes{
				AllowedSessionTypes: []models.PduSessionType{tc.allowed},
			}

			cause, err := smContext.IsAllowedPDUSessionType(tc.requested)
			require.NoError(t, err)
			require.Zero(t, cause)
			require.Equal(t, tc.selected, smContext.SelectedPDUSessionType)
			require.Equal(t, tc.cause, smContext.EstAcceptCause5gSMValue)
		})
	}
}
