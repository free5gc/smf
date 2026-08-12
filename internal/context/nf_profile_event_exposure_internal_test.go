package context

import (
	"testing"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

func TestSetupNFProfileEventExposureServiceNameListGating(t *testing.T) {
	tests := []struct {
		name      string
		services  []string
		wantFound bool
	}{
		{
			name:      "present advertises",
			services:  []string{"nsmf-pdusession", "nsmf-event-exposure"},
			wantFound: true,
		},
		{
			name:      "absent disables advertisement",
			services:  []string{"nsmf-pdusession"},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &SMFContext{
				NfInstanceID: "00000000-0000-4000-8000-000000000001",
				URIScheme:    models.UriScheme_HTTP,
				RegisterIPv4: "127.0.0.1",
				SBIPort:      8000,
			}
			ctx.SetupNFProfile(&factory.Config{
				Info: &factory.Info{Version: "1.0.7"},
				Configuration: &factory.Configuration{
					ServiceNameList: tt.services,
				},
			})

			found := false
			for _, service := range *ctx.NfProfile.NFServices {
				if service.ServiceName == models.ServiceName_NSMF_EVENT_EXPOSURE {
					found = true
				}
			}
			if found != tt.wantFound {
				t.Fatalf("advertisement mismatch: got %v want %v", found, tt.wantFound)
			}
		})
	}
}
