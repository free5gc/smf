package factory_test

import (
	"testing"

	"github.com/asaskevich/govalidator"
	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/nfheartbeat"
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

func TestGetNfHeartBeatTimer(t *testing.T) {
	tests := []struct {
		name string
		cfg  *factory.Config
		want int32
	}{
		{
			name: "no configuration section",
			cfg:  &factory.Config{},
			want: nfheartbeat.DefaultTimer,
		},
		{
			name: "option absent",
			cfg:  &factory.Config{Configuration: &factory.Configuration{}},
			want: nfheartbeat.DefaultTimer,
		},
		{
			name: "option set",
			cfg:  &factory.Config{Configuration: &factory.Configuration{NfHeartBeatTimer: 45}},
			want: 45,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.GetNfHeartBeatTimer(); got != tt.want {
				t.Errorf("GetNfHeartBeatTimer() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNfHeartBeatTimerRange(t *testing.T) {
	// The range(1|3600) struct tag cannot reference constants; keep it aligned
	// with the bounds the NRF profile validator enforces.
	if nfheartbeat.MinTimer != 1 || nfheartbeat.MaxTimer != 3600 {
		t.Fatalf("range(1|3600) tag out of sync with nfheartbeat bounds [%d, %d]",
			nfheartbeat.MinTimer, nfheartbeat.MaxTimer)
	}

	tests := []struct {
		name    string
		timer   int32
		wantErr bool
	}{
		{name: "absent is optional", timer: 0},
		{name: "lower bound", timer: nfheartbeat.MinTimer},
		{name: "upper bound of 1 hour", timer: nfheartbeat.MaxTimer},
		{name: "above the upper bound", timer: nfheartbeat.MaxTimer + 1, wantErr: true},
		{name: "negative", timer: -1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := govalidator.ValidateStruct(&factory.Configuration{NfHeartBeatTimer: tt.timer})

			fieldErr := govalidator.ErrorByField(err, "NfHeartBeatTimer")
			if gotErr := fieldErr != ""; gotErr != tt.wantErr {
				t.Errorf("nfHeartBeatTimer %d: field error = %q, want error %v", tt.timer, fieldErr, tt.wantErr)
			}
		})
	}
}
