package context_test

import (
	"testing"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/serviceuri"
)

// The registered NFService must follow TS 29.510 (no URL in apiPrefix, a
// hostname as fqdn, an IP literal in ipEndPoints) and resolve back to the SMF
// API root.
func TestSetupNFProfileAddressing(t *testing.T) {
	self := smf_context.GetSelf()
	origAddr, origScheme, origPort, origProfile := self.RegisterIPv4, self.URIScheme, self.SBIPort, self.NfProfile
	t.Cleanup(func() {
		self.RegisterIPv4, self.URIScheme, self.SBIPort, self.NfProfile = origAddr, origScheme, origPort, origProfile
	})

	cfg := &factory.Config{
		Info:          &factory.Info{Version: "1.0.7"},
		Configuration: &factory.Configuration{ServiceNameList: []string{"nsmf-pdusession"}},
	}
	tests := []struct {
		registerAddr string
		wantFQDN     string
		wantIPv4     string
		wantAPIRoot  string
	}{
		{"10.0.0.2", "", "10.0.0.2", "http://10.0.0.2:8000"},
		{"smf.free5gc.org", "smf.free5gc.org", "", "http://smf.free5gc.org:8000"},
	}

	for _, tt := range tests {
		t.Run(tt.registerAddr, func(t *testing.T) {
			self.RegisterIPv4, self.URIScheme, self.SBIPort = tt.registerAddr, models.UriScheme_HTTP, 8000
			self.SetupNFProfile(cfg)
			s := (*self.NfProfile.NFServices)[0]

			if s.ApiPrefix != "" || s.Fqdn != tt.wantFQDN ||
				len(s.IpEndPoints) != 1 || s.IpEndPoints[0].Ipv4Address != tt.wantIPv4 {
				t.Fatalf("unexpected NFService addressing: %+v", s)
			}

			profile := &models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{{
				ServiceName:     s.ServiceName,
				NfServiceStatus: s.NfServiceStatus,
				Scheme:          s.Scheme,
				Fqdn:            s.Fqdn,
				IpEndPoints:     s.IpEndPoints,
			}}}
			got, err := serviceuri.ResolveNFServiceURI(profile, s.ServiceName, serviceuri.Options{})
			if err != nil || got.APIRoot != tt.wantAPIRoot {
				t.Fatalf("resolved %q, error %v; want %q", got.APIRoot, err, tt.wantAPIRoot)
			}
		})
	}
}
