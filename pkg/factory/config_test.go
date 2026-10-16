package factory_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

func TestSnssaiInfoItem(t *testing.T) {
	testcase := []struct {
		Name     string
		Snssai   *models.Snssai
		DnnInfos []*factory.SnssaiDnnInfoItem
	}{
		{
			Name: "Default",
			Snssai: &models.Snssai{
				Sst: int32(1),
				Sd:  "010203",
			},
			DnnInfos: []*factory.SnssaiDnnInfoItem{
				{
					Dnn: "internet",
					DNS: &factory.DNS{
						IPv4Addr: "8.8.8.8",
					},
				},
			},
		},
		{
			Name: "Empty SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
			},
			DnnInfos: []*factory.SnssaiDnnInfoItem{
				{
					Dnn: "internet2",
					DNS: &factory.DNS{
						IPv4Addr: "1.1.1.1",
					},
				},
			},
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			snssaiInfoItem := factory.SnssaiInfoItem{
				SNssai:   tc.Snssai,
				DnnInfos: tc.DnnInfos,
			}

			ok, err := snssaiInfoItem.Validate()
			require.True(t, ok)
			require.Nil(t, err)
		})
	}
}

func TestSnssaiUpfInfoItem(t *testing.T) {
	testcase := []struct {
		Name     string
		Snssai   *models.Snssai
		DnnInfos []*factory.DnnUpfInfoItem
	}{
		{
			Name: "Default",
			Snssai: &models.Snssai{
				Sst: int32(1),
				Sd:  "010203",
			},
			DnnInfos: []*factory.DnnUpfInfoItem{
				{
					Dnn: "internet",
				},
			},
		},
		{
			Name: "Empty SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
			},
			DnnInfos: []*factory.DnnUpfInfoItem{
				{
					Dnn: "internet2",
				},
			},
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			snssaiInfoItem := factory.SnssaiUpfInfoItem{
				SNssai:         tc.Snssai,
				DnnUpfInfoList: tc.DnnInfos,
			}

			ok, err := snssaiInfoItem.Validate()
			require.True(t, ok)
			require.Nil(t, err)
		})
	}
}

func TestSmfEventExposureResourcePrefix(t *testing.T) {
	require.Equal(t, "/nsmf-event-exposure/v1", factory.SmfEventExposureResUriPrefix)
}

func TestUPNodeNupfEeApiRootValidation(t *testing.T) {
	testCases := []struct {
		name       string
		nodeType   string
		apiRoot    string
		wantValid  bool
		wantStored string
	}{
		{name: "omitted", nodeType: "UPF", wantValid: true},
		{
			name:       "http deployment prefix",
			nodeType:   "UPF",
			apiRoot:    "http://upf.example.com:8080/deployment",
			wantValid:  true,
			wantStored: "http://upf.example.com:8080/deployment",
		},
		{
			name:       "remove trailing slashes only",
			nodeType:   "UPF",
			apiRoot:    "https://upf.example.com/prefix///",
			wantValid:  true,
			wantStored: "https://upf.example.com/prefix",
		},
		{name: "whitespace", nodeType: "UPF", apiRoot: " ", wantValid: false},
		{name: "relative", nodeType: "UPF", apiRoot: "/upf", wantValid: false},
		{name: "unsupported scheme", nodeType: "UPF", apiRoot: "ftp://upf.example.com", wantValid: false},
		{name: "hostless", nodeType: "UPF", apiRoot: "https:///upf", wantValid: false},
		{name: "userinfo", nodeType: "UPF", apiRoot: "https://user@upf.example.com", wantValid: false},
		{name: "query", nodeType: "UPF", apiRoot: "https://upf.example.com?x=1", wantValid: false},
		{name: "empty query", nodeType: "UPF", apiRoot: "https://upf.example.com?", wantValid: false},
		{name: "fragment", nodeType: "UPF", apiRoot: "https://upf.example.com#part", wantValid: false},
		{
			name:      "service path",
			nodeType:  "UPF",
			apiRoot:   "https://upf.example.com/nupf-ee/v1",
			wantValid: false,
		},
		{
			name:      "resource path",
			nodeType:  "UPF",
			apiRoot:   "https://upf.example.com/ee-subscriptions/subscription-1",
			wantValid: false,
		},
		{name: "AN endpoint", nodeType: "AN", apiRoot: "https://upf.example.com", wantValid: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := validConfigWithNupfEeApiRoot(tc.nodeType, tc.apiRoot)
			valid, err := config.Validate()
			if tc.wantValid {
				require.NoError(t, err)
				require.True(t, valid)
				require.Equal(t, tc.wantStored, config.Configuration.UserPlaneInformation.UPNodes["node"].NupfEeApiRoot)
				return
			}
			require.Error(t, err)
			require.False(t, valid)
		})
	}
}

func validConfigWithNupfEeApiRoot(nodeType, apiRoot string) *factory.Config {
	node := &factory.UPNode{
		Type:          nodeType,
		NodeID:        "127.0.0.8",
		ANIP:          "127.0.0.8",
		NupfEeApiRoot: apiRoot,
	}

	return &factory.Config{
		Info: &factory.Info{Version: "1.0.7"},
		Configuration: &factory.Configuration{
			SmfName: "smf",
			Sbi: &factory.Sbi{
				Scheme:       "http",
				BindingIPv4:  "127.0.0.2",
				RegisterIPv4: "127.0.0.2",
			},
			PFCP: &factory.PFCP{
				ListenAddr:   "127.0.0.1",
				ExternalAddr: "127.0.0.1",
				NodeID:       "127.0.0.1",
			},
			NrfUri: "http://127.0.0.10:8000",
			UserPlaneInformation: factory.UserPlaneInformation{
				UPNodes: map[string]*factory.UPNode{"node": node},
				Links: []*factory.UPLink{
					{A: "node", B: "node"},
				},
			},
			ServiceNameList: []string{"nsmf-pdusession"},
			SNssaiInfo: []*factory.SnssaiInfoItem{
				{
					SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
					DnnInfos: []*factory.SnssaiDnnInfoItem{
						{Dnn: "internet", DNS: &factory.DNS{IPv4Addr: "8.8.8.8"}},
					},
				},
			},
			T3591: &factory.TimerValue{Enable: true, ExpireTime: time.Second, MaxRetryTimes: 1},
			T3592: &factory.TimerValue{Enable: true, ExpireTime: time.Second, MaxRetryTimes: 1},
		},
		Logger: &factory.Logger{Level: "info"},
	}
}
