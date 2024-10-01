package factory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

func baseGNB() *factory.GNBConfig {
	return &factory.GNBConfig{
		UPNodeConfig: &factory.UPNodeConfig{
			Type: "AN",
		},
	}
}

func baseUPF() *factory.UPFConfig {
	return &factory.UPFConfig{
		UPNodeConfig: &factory.UPNodeConfig{
			Type: "UPF",
		},
		NodeID: "127.0.0.8",
		SNssaiInfos: []*factory.SnssaiUpfInfoItem{
			{
				SNssai: &models.Snssai{
					Sst: int32(1),
					Sd:  "010203",
				},
				DnnUpfInfoList: []*factory.DnnUpfInfoItem{
					{
						Dnn: "internet",
						Pools: []*factory.UEIPPool{
							{
								Cidr: "10.60.0.0/16",
							},
						},
					},
				},
			},
		},
		Interfaces: []*factory.Interface{
			{
				InterfaceType: "N3",
				Endpoints: []string{
					"127.0.0.8",
				},
				NetworkInstances: []string{
					"internet",
				},
			},
		},
	}
}

func TestUPNodeConfigInterfaceValidation(t *testing.T) {
	testcase := []struct {
		Name         string
		UPNodeConfig factory.UPNodeConfigInterface
		Valid        bool
	}{
		{
			Name:         "Valid gNB",
			UPNodeConfig: baseGNB(),
			Valid:        true,
		},
		{
			Name:         "Valid UPF",
			UPNodeConfig: baseUPF(),
			Valid:        true,
		},
		{
			Name: "UPF with wrong type",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.UPNodeConfig.Type = "xxx"
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with wrong NodeID",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.NodeID = "foobar"
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with nil sNssaiUpfInfos",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.SNssaiInfos = nil
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with empty sNssaiUpfInfos",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.SNssaiInfos = []*factory.SnssaiUpfInfoItem{}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with nil interfaces",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.Interfaces = nil
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with empty interfaces",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.Interfaces = []*factory.Interface{}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with overlapping pools in DnnUpfInfoItem.Pools",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.SNssaiInfos[0].DnnUpfInfoList = []*factory.DnnUpfInfoItem{
					{
						Dnn: "internet",
						Pools: []*factory.UEIPPool{
							{
								Cidr: "10.60.0.0/16",
							},
							{
								Cidr: "10.60.10.0/16",
							},
						},
					},
				}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with overlapping pools in DnnUpfInfoItem.Pools and DnnUpfInfoItem.StaticPools",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.SNssaiInfos[0].DnnUpfInfoList = []*factory.DnnUpfInfoItem{
					{
						Dnn: "internet",
						Pools: []*factory.UEIPPool{
							{
								Cidr: "10.60.0.0/16",
							},
						},
						StaticPools: []*factory.UEIPPool{
							{
								Cidr: "10.60.10.0/16",
							},
						},
					},
				}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF without N3 interface",
			UPNodeConfig: func() *factory.UPFConfig {
				config := baseUPF()
				config.Interfaces = []*factory.Interface{}
				return config
			}(),
			Valid: false,
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			ok, err := tc.UPNodeConfig.Validate()
			require.Equal(t, tc.Valid, ok)
			require.Nil(t, err)
		})
	}
}

func TestUserplaneInformationValidation(t *testing.T) {
	testcase := []struct {
		Name  string
		Upi   *factory.UserPlaneInformation
		Valid bool
	}{
		{
			Name: "Valid userPlaneInformation",
			Upi: &factory.UserPlaneInformation{
				UPNodes: map[string]factory.UPNodeConfigInterface{
					"gNB":  baseGNB(),
					"UPF1": baseUPF(),
				},
				Links: []*factory.UPLink{
					{
						A: "gNB",
						B: "UPF1",
					},
				},
			},
			Valid: true,
		},
		{
			Name: "Link with non-existing node",
			Upi: &factory.UserPlaneInformation{
				UPNodes: map[string]factory.UPNodeConfigInterface{
					"gNB":  baseGNB(),
					"UPF1": baseUPF(),
				},
				Links: []*factory.UPLink{
					{
						A: "gNB",
						B: "UPF",
					},
				},
			},
			Valid: false,
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			ok, err := tc.Upi.Validate()
			require.Equal(t, tc.Valid, ok)
			require.Nil(t, err)
		})
	}
}

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
