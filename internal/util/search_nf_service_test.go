package util_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/util"
)

const (
	testUECM = models.Nrf_NFMgmt_ServiceName_NUDM_UECM
	testSDM  = models.Nrf_NFMgmt_ServiceName_NUDM_SDM
)

func testUDM(id string, priority int32, ip string, names ...models.Nrf_NFMgmt_ServiceName) models.Nrf_NFDisc_NFProfile {
	profile := models.Nrf_NFDisc_NFProfile{NfInstanceId: id, Priority: priority}
	for _, name := range names {
		profile.NfServices = append(profile.NfServices, models.Nrf_NFDisc_NFService{
			ServiceName:     name,
			Scheme:          models.UriScheme_HTTP,
			NfServiceStatus: models.Nrf_NFMgmt_NFServiceStatus_REGISTERED,
			IpEndPoints:     []models.Nrf_NFMgmt_IpEndPoint{{Ipv4Address: ip, Port: 8000}},
		})
	}
	return profile
}

// A UDM that lacks one of the services must not be selected, even with a
// higher priority; among complete UDMs the higher priority wins.
func TestSelectNFServices(t *testing.T) {
	uecmOnly := testUDM("udm-a", 1, "10.0.0.1", testUECM)
	low := testUDM("udm-b", 3, "10.0.0.2", testUECM, testSDM)
	high := testUDM("udm-c", 2, "10.0.0.3", testUECM, testSDM)

	tests := []struct {
		name      string
		instances []models.Nrf_NFDisc_NFProfile
		wantID    string
		wantURI   string
	}{
		{"Skip_UDM_without_SDM", []models.Nrf_NFDisc_NFProfile{uecmOnly, low}, "udm-b", "http://10.0.0.2:8000"},
		{"Prefer_higher_priority", []models.Nrf_NFDisc_NFProfile{uecmOnly, low, high}, "udm-c", "http://10.0.0.3:8000"},
		{"No_complete_UDM", []models.Nrf_NFDisc_NFProfile{uecmOnly}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			udm, uris := util.SelectNFServices(tt.instances, testUECM, testSDM)
			if tt.wantID == "" {
				require.Nil(t, udm)
				require.Nil(t, uris)
				return
			}
			require.NotNil(t, udm)
			require.Equal(t, tt.wantID, udm.NfInstanceId)
			require.Equal(t, []string{tt.wantURI, tt.wantURI}, uris)
		})
	}
}
