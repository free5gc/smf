package context_test

import (
	"reflect"
	"testing"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

func TestAllowedNfTypesForService(t *testing.T) {
	tests := []struct {
		name        string
		serviceName models.ServiceName
		want        []models.NrfNfManagementNfType
	}{
		{
			name:        "nsmf-pdusession allows AMF and SMF",
			serviceName: models.ServiceName_NSMF_PDUSESSION,
			want: []models.NrfNfManagementNfType{
				models.NrfNfManagementNfType_AMF,
				models.NrfNfManagementNfType_SMF,
			},
		},
		{
			name:        "other services remain unrestricted",
			serviceName: models.ServiceName_NSMF_EVENT_EXPOSURE,
			want:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := smf_context.AllowedNfTypesForService(tt.serviceName)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("AllowedNfTypesForService(%q) = %v, want %v", tt.serviceName, got, tt.want)
			}
		})
	}
}
