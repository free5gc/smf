package util

import (
	"github.com/free5gc/openapi/models"
	"testing"
)

func newNFServiceCandidate(scheme models.UriScheme, prefix, fqdn, ip string, port int32) models.Nrf_NFDisc_NFService {
	srv := models.Nrf_NFDisc_NFService{
		ServiceName:     models.Nrf_NFMgmt_ServiceName_NAMF_COMM,
		NfServiceStatus: models.Nrf_NFMgmt_NFServiceStatus_REGISTERED,
		Scheme:          scheme,
		ApiPrefix:       prefix,
		Fqdn:            fqdn,
	}
	if ip != "" || port != 0 {
		srv.IpEndPoints = []models.Nrf_NFMgmt_IpEndPoint{{Ipv4Address: ip, Port: port}}
	}
	return srv
}

func TestConsumerURIRoot(t *testing.T) {
	cases := []struct {
		name    string
		service models.Nrf_NFDisc_NFService
		want    string
	}{
		{"HTTPIP", newNFServiceCandidate(models.UriScheme_HTTP, "", "", "10.101.200.28", 8000), "http://10.101.200.28:8000"},
		{"HTTPPath", newNFServiceCandidate(models.UriScheme_HTTP, "/operator/edge", "", "10.101.200.28", 8000), "http://10.101.200.28:8000/operator/edge"},
		{"HTTPSFQDN", newNFServiceCandidate(models.UriScheme_HTTPS, "/sbi", "amf.example.org", "10.101.200.28", 8443), "https://amf.example.org:8443/sbi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{tc.service}}
			got, err := ResolveNFServiceURI(profile, models.Nrf_NFMgmt_ServiceName_NAMF_COMM, models.Nrf_NFMgmt_NFServiceStatus_REGISTERED, ServiceURIOptions{})
			if err != nil || got != tc.want {
				t.Fatalf("got %q, err %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestConsumerURIInvalid(t *testing.T) {
	cases := []struct {
		name    string
		service models.Nrf_NFDisc_NFService
	}{
		{"missingScheme", newNFServiceCandidate("", "", "", "10.101.200.28", 8000)},
		{"badPrefix", newNFServiceCandidate(models.UriScheme_HTTP, "relative/path", "", "10.101.200.28", 8000)},
		{"invalidIPv4", newNFServiceCandidate(models.UriScheme_HTTP, "", "", "hostname.example.org", 8000)},
		{"HTTPSNoFQDN", newNFServiceCandidate(models.UriScheme_HTTPS, "", "", "10.101.200.28", 8000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{tc.service}}
			got, err := ResolveNFServiceURI(profile, models.Nrf_NFMgmt_ServiceName_NAMF_COMM, models.Nrf_NFMgmt_NFServiceStatus_REGISTERED, ServiceURIOptions{})
			if err == nil || got != "" {
				t.Fatalf("expected rejection; got %q, err %v", got, err)
			}
		})
	}
}

func TestConsumerURILegacyAndFallback(t *testing.T) {
	legacy := newNFServiceCandidate(models.UriScheme_HTTP, "http://10.101.200.18:8000", "", "10.101.200.18", 8000)
	profile := models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{legacy}}
	if _, err := ResolveNFServiceURI(profile, models.Nrf_NFMgmt_ServiceName_NAMF_COMM, models.Nrf_NFMgmt_NFServiceStatus_REGISTERED, ServiceURIOptions{}); err == nil {
		t.Fatal("strict URI resolver accepted legacy URL")
	}
	got, err := ResolveNFServiceURI(profile, models.Nrf_NFMgmt_ServiceName_NAMF_COMM, models.Nrf_NFMgmt_NFServiceStatus_REGISTERED, ServiceURIOptions{AllowLegacyAbsoluteAPIPrefix: true})
	if err != nil || got != "http://10.101.200.18:8000" {
		t.Fatalf("legacy got %q err %v", got, err)
	}

	bad := newNFServiceCandidate(models.UriScheme_HTTP, "", "", "", 0)
	good := newNFServiceCandidate(models.UriScheme_HTTP, "", "", "10.101.200.28", 8000)
	profile.NfServices = []models.Nrf_NFDisc_NFService{bad, good}
	got, err = ResolveNFServiceURI(profile, models.Nrf_NFMgmt_ServiceName_NAMF_COMM, models.Nrf_NFMgmt_NFServiceStatus_REGISTERED, ServiceURIOptions{})
	if err != nil || got != "http://10.101.200.28:8000" {
		t.Fatalf("fallback got %q err %v", got, err)
	}
	profile.NfServices = nil
	profile.NfServiceList = map[string]models.Nrf_NFDisc_NFService{"bad": bad, "good": good}
	got, err = ResolveNFServiceURI(profile, models.Nrf_NFMgmt_ServiceName_NAMF_COMM, models.Nrf_NFMgmt_NFServiceStatus_REGISTERED, ServiceURIOptions{})
	if err != nil || got != "http://10.101.200.28:8000" {
		t.Fatalf("map fallback got %q err %v", got, err)
	}
}
