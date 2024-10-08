package factory_test

import (
	"fmt"
	"testing"

	"github.com/asaskevich/govalidator"
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

func baseUPI() *factory.UserPlaneInformation {
	return &factory.UserPlaneInformation{
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
	}
}

func TestUserplaneInformationValidation(t *testing.T) {
	testcase := []struct {
		Name  string
		Upi   *factory.UserPlaneInformation
		Valid bool
	}{
		{
			Name:  "Valid userPlaneInformation",
			Upi:   baseUPI(),
			Valid: true,
		},
		{
			Name: "gNB with wrong type",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["gNB"].(*factory.GNBConfig).Type = "xxx"
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with wrong type",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).Type = "xxx"
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with wrong NodeID",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).NodeID = "127.0.0.1/24"
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with nil sNssaiUpfInfos",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).SNssaiInfos = nil
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with empty sNssaiUpfInfos",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).SNssaiInfos = []*factory.SnssaiUpfInfoItem{}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with invalid pool cidr",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).SNssaiInfos[0].DnnUpfInfoList[0].Pools = []*factory.UEIPPool{
					{
						Cidr: "10.60.0.0",
					},
				}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with overlapping dynamic pools in DnnUpfInfoItem.Pools",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).SNssaiInfos[0].DnnUpfInfoList = []*factory.DnnUpfInfoItem{
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
			Name: "UPF with overlapping static pools in DnnUpfInfoItem.Pools",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).SNssaiInfos[0].DnnUpfInfoList = []*factory.DnnUpfInfoItem{
					{
						Dnn: "internet",
						Pools: []*factory.UEIPPool{
							{
								Cidr: "10.80.0.0/16",
							},
						},
						StaticPools: []*factory.UEIPPool{
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
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).SNssaiInfos[0].DnnUpfInfoList = []*factory.DnnUpfInfoItem{
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
			Name: "UPF with nil interfaces",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).Interfaces = nil
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with empty interfaces",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).Interfaces = []*factory.Interface{}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with invalid interface",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).Interfaces = []*factory.Interface{
					{
						InterfaceType: "N3",
						Endpoints: []string{
							"127.0.0.8",
						},
					},
					{
						InterfaceType: "N4",
						Endpoints: []string{
							"127.0.0.89",
						},
					},
				}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "UPF with only N9 interface",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).Interfaces = []*factory.Interface{
					{
						InterfaceType: "N9",
						Endpoints: []string{
							"127.0.0.8",
						},
						NetworkInstances: []string{
							"internet",
						},
					},
				}
				return config
			}(),
			Valid: true,
		},
		{
			Name: "UPF with two N3 interfaces",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).Interfaces = []*factory.Interface{
					{
						InterfaceType: "N3",
						Endpoints: []string{
							"127.0.0.8",
						},
						NetworkInstances: []string{
							"internet",
						},
					},
					{
						InterfaceType: "N3",
						Endpoints: []string{
							"127.0.0.88",
						},
						NetworkInstances: []string{
							"internet",
						},
					},
				}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "Link with non-existing node",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.Links = []*factory.UPLink{
					{
						A: "gNB",
						B: "UPF",
					},
				}
				return config
			}(),
			Valid: false,
		},
		{
			Name: "Invalid SnssaiUpfInfoItem",
			Upi: func() *factory.UserPlaneInformation {
				config := baseUPI()
				config.UPNodes["UPF1"].(*factory.UPFConfig).SNssaiInfos[0].SNssai = &models.Snssai{
					Sst: int32(256),
				}
				return config
			}(),
			Valid: false,
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			// register custom semantic validators
			factory.RegisterSNssaiValidator()
			factory.RegisterUPNodeValidator()
			factory.RegisterLinkValidator()
			factory.RegisterPoolValidator()
			factory.RegisterDnnUpfInfoItemValidator()

			ok, err := govalidator.ValidateStruct(tc.Upi)
			if err != nil {
				errs := err.(govalidator.Errors).Errors()
				for _, e := range errs {
					fmt.Println(e.Error())
				}
			}
			require.Equal(t, tc.Valid, ok)
			if !ok {
				require.Error(t, err)
			} else {
				require.Nil(t, err)
			}
		})
	}
}

func TestSnssaiValidator_SnssaiInfoItem(t *testing.T) {
	testcase := []struct {
		Name   string
		Snssai *models.Snssai
		Valid  bool
	}{
		{
			Name: "Valid SNssai with SST and SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
				Sd:  "010203",
			},
			Valid: true,
		},
		{
			Name: "Valid SNssai without SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
			},
			Valid: true,
		},
		{
			Name: "Invalid SNssai: invalid SST",
			Snssai: &models.Snssai{
				Sst: int32(256),
			},
			Valid: false,
		},
		{
			Name: "Invalid SNssai: invalid SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
				Sd:  "32",
			},
			Valid: false,
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			factory.RegisterSNssaiValidator()
			snssaiInfoItem := factory.SnssaiInfoItem{
				SNssai: tc.Snssai,
				DnnInfos: []*factory.SnssaiDnnInfoItem{
					{
						Dnn: "internet",
						DNS: &factory.DNS{},
					},
				},
			}
			ok, err := govalidator.ValidateStruct(snssaiInfoItem)
			// if err != nil {
			// 	errs := err.(govalidator.Errors).Errors()
			// 	for _, e := range errs {
			// 		fmt.Println(e.Error())
			// 	}
			// }
			require.Equal(t, tc.Valid, ok)
			if !ok {
				require.Error(t, err)
			} else {
				require.Nil(t, err)
			}
		})
	}
}

func TestSnssaiValidator_SnssaiUpfInfoItem(t *testing.T) {
	testcase := []struct {
		Name   string
		Snssai *models.Snssai
		Valid  bool
	}{
		{
			Name: "Valid SNssai with SST and SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
				Sd:  "010203",
			},
			Valid: true,
		},
		{
			Name: "Valid SNssai without SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
			},
			Valid: true,
		},
		{
			Name: "Invalid SNssai: invalid SST",
			Snssai: &models.Snssai{
				Sst: int32(256),
			},
			Valid: false,
		},
		{
			Name: "Invalid SNssai: invalid SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
				Sd:  "32",
			},
			Valid: false,
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			factory.RegisterDnnUpfInfoItemValidator()
			factory.RegisterSNssaiValidator()
			snssaiUpfInfoItem := factory.SnssaiUpfInfoItem{
				SNssai: tc.Snssai,
				DnnUpfInfoList: []*factory.DnnUpfInfoItem{
					{
						Dnn: "internet",
					},
				},
			}
			ok, err := govalidator.ValidateStruct(snssaiUpfInfoItem)
			if err != nil {
				errs := err.(govalidator.Errors).Errors()
				for _, e := range errs {
					fmt.Println(e.Error())
				}
			}
			require.Equal(t, tc.Valid, ok)
			if !ok {
				require.Error(t, err)
			} else {
				require.Nil(t, err)
			}
		})
	}
}
