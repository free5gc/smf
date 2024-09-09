package context_test

import (
	"context"
	"fmt"
	"net"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

var configuration = &factory.UserPlaneInformation{
	UPNodes: map[string]*factory.UPNode{
		"GNodeB": {
			Type:   "AN",
			NodeID: "192.168.179.100",
		},
		"UPF1": {
			Type:   "UPF",
			NodeID: "192.168.179.1",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 1,
						Sd:  "112232",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{
									Cidr: "10.60.0.0/27",
								},
							},
							StaticPools: []*factory.UEIPPool{
								{
									Cidr: "10.60.0.0/28",
								},
							},
						},
					},
				},
				{
					SNssai: &models.Snssai{
						Sst: 1,
						Sd:  "112235",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{
									Cidr: "10.61.0.0/16",
								},
							},
						},
					},
				},
			},
		},
		"UPF2": {
			Type:   "UPF",
			NodeID: "192.168.179.2",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 2,
						Sd:  "112233",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{
									Cidr: "10.62.0.0/16",
								},
							},
						},
					},
				},
			},
		},
		"UPF3": {
			Type:   "UPF",
			NodeID: "192.168.179.3",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 3,
						Sd:  "112234",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{
									Cidr: "10.63.0.0/16",
								},
							},
						},
					},
				},
			},
		},
		"UPF4": {
			Type:   "UPF",
			NodeID: "192.168.179.4",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 1,
						Sd:  "112235",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{
									Cidr: "10.64.0.0/16",
								},
							},
						},
					},
				},
			},
		},
	},
	Links: []*factory.UPLink{
		{
			A: "GNodeB",
			B: "UPF1",
		},
		{
			A: "UPF1",
			B: "UPF2",
		},
		{
			A: "UPF2",
			B: "UPF3",
		},
		{
			A: "UPF3",
			B: "UPF4",
		},
	},
}

func TestNewUserPlaneInformation(t *testing.T) {
	userplaneInformation := smf_context.NewUserPlaneInformation(configuration)
	for _, upf := range userplaneInformation.UPFs {
		upf.UPFStatus = smf_context.AssociatedSetUpSuccess
		upf.Association, upf.AssociationCancelFunc = context.WithCancel(context.Background())
	}

	require.NotNil(t, userplaneInformation.AccessNetwork["GNodeB"])

	// check UUIDs have been created
	// check UPFs are in other UP maps as well
	for uuid, upf := range userplaneInformation.UPFs {
		require.NotNil(t, uuid)
		require.NotNil(t, userplaneInformation.NodeIDToName[upf.GetNodeIDString()])
		require.NotNil(t, userplaneInformation.NodeIDToUPF[upf.GetNodeIDString()])
	}

	require.NotNil(t, userplaneInformation.NameToUPNode["UPF1"])
	require.NotNil(t, userplaneInformation.NameToUPNode["UPF2"])
	require.NotNil(t, userplaneInformation.NameToUPNode["UPF3"])
	require.NotNil(t, userplaneInformation.NameToUPNode["UPF4"])

	// check links
	require.Contains(t, userplaneInformation.AccessNetwork["GNodeB"].GetLinks(), userplaneInformation.NameToUPNode["UPF1"])
	require.Contains(t, userplaneInformation.NameToUPNode["UPF1"].GetLinks(), userplaneInformation.NameToUPNode["UPF2"])
	require.Contains(t, userplaneInformation.NameToUPNode["UPF2"].GetLinks(), userplaneInformation.NameToUPNode["UPF3"])
	require.Contains(t, userplaneInformation.NameToUPNode["UPF3"].GetLinks(), userplaneInformation.NameToUPNode["UPF4"])
}

func TestGenerateDefaultPath(t *testing.T) {
	config1 := *configuration
	config1.Links = []*factory.UPLink{
		{
			A: "GNodeB",
			B: "UPF1",
		},
		{
			A: "GNodeB",
			B: "UPF2",
		},
		{
			A: "GNodeB",
			B: "UPF3",
		},
		{
			A: "UPF1",
			B: "UPF4",
		},
	}

	testCases := []struct {
		name     string
		param    *smf_context.UPFSelectionParams
		expected bool
	}{
		{
			"S-NSSAI 01112232 and DNN internet ok",
			&smf_context.UPFSelectionParams{
				SNssai: &smf_context.SNssai{
					Sst: 1,
					Sd:  "112232",
				},
				Dnn: "internet",
			},
			true,
		},
		{
			"S-NSSAI 02112233 and DNN internet ok",
			&smf_context.UPFSelectionParams{
				SNssai: &smf_context.SNssai{
					Sst: 2,
					Sd:  "112233",
				},
				Dnn: "internet",
			},
			true,
		},
		{
			"S-NSSAI 03112234 and DNN internet ok",
			&smf_context.UPFSelectionParams{
				SNssai: &smf_context.SNssai{
					Sst: 3,
					Sd:  "112234",
				},
				Dnn: "internet",
			},
			true,
		},
		{
			"S-NSSAI 01112235 and DNN internet ok",
			&smf_context.UPFSelectionParams{
				SNssai: &smf_context.SNssai{
					Sst: 1,
					Sd:  "112235",
				},
				Dnn: "internet",
			},
			true,
		},
		{
			"S-NSSAI 01010203 and DNN internet fail",
			&smf_context.UPFSelectionParams{
				SNssai: &smf_context.SNssai{
					Sst: 1,
					Sd:  "010203",
				},
				Dnn: "internet",
			},
			false,
		},
	}

	userplaneInformation := smf_context.NewUserPlaneInformation(&config1)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pathExist := userplaneInformation.GenerateDefaultPath(tc.param)
			require.Equal(t, tc.expected, pathExist)
		})
	}
}

func TestGetDefaultUPFTopoByDNN(t *testing.T) {
}

func TestSelectUPFAndAllocUEIP(t *testing.T) {
	var expectedIPPool []net.IP

	for i := 16; i <= 31; i++ {
		expectedIPPool = append(expectedIPPool, net.ParseIP(fmt.Sprintf("10.60.0.%d", i)).To4())
	}

	userplaneInformation := smf_context.NewUserPlaneInformation(configuration)
	for _, upf := range userplaneInformation.UPFs {
		upf.UPFStatus = smf_context.AssociatedSetUpSuccess
		upf.Association, upf.AssociationCancelFunc = context.WithCancel(context.Background())
	}

	for i := 0; i <= 100; i++ {
		upf, allocatedIP, _, err := userplaneInformation.SelectUPFAndAllocUEIP(
			&smf_context.UPFSelectionParams{
				Dnn: "internet",
				SNssai: &smf_context.SNssai{
					Sst: 1,
					Sd:  "112232",
				},
			},
			"imsi-208930000000001")

		require.Nil(t, err)
		require.Contains(t, expectedIPPool, allocatedIP)
		userplaneInformation.ReleaseUEIP(upf, allocatedIP, false)
	}
}

var configForIPPoolAllocate = &factory.UserPlaneInformation{
	UPNodes: map[string]*factory.UPNode{
		"GNodeB": {
			Type:   "AN",
			NodeID: "192.168.179.100",
		},
		"UPF1": {
			Type:   "UPF",
			NodeID: "192.168.179.1",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 1,
						Sd:  "111111",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{
									Cidr: "10.71.0.0/16",
								},
							},
							StaticPools: []*factory.UEIPPool{
								{
									Cidr: "10.61.100.0/24",
								},
							},
						},
					},
				},
			},
		},
		"UPF2": {
			Type:   "UPF",
			NodeID: "192.168.179.2",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 2,
						Sd:  "222222",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{
									Cidr: "10.62.0.0/16",
								},
							},
							StaticPools: []*factory.UEIPPool{
								{
									Cidr: "10.62.100.0/24",
								},
							},
						},
					},
				},
			},
		},
		"UPF3": {
			Type:   "UPF",
			NodeID: "192.168.179.3",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 3,
						Sd:  "333333",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{
									Cidr: "10.63.0.0/16",
								},
							},
							StaticPools: []*factory.UEIPPool{
								{
									Cidr: "10.63.0.0/24",
								},
							},
						},
					},
				},
			},
		},
	},
	Links: []*factory.UPLink{
		{
			A: "GNodeB",
			B: "UPF1",
		},
		{
			A: "GNodeB",
			B: "UPF2",
		},
		{
			A: "GNodeB",
			B: "UPF3",
		},
	},
}

func TestGetUEIPPool(t *testing.T) {
	testCases := []struct {
		name          string
		allocateTimes int
		param         *smf_context.UPFSelectionParams
		subnet        int
		useStaticIP   bool
		expectedIP    net.IP
		expectedError error
	}{
		{
			name:          "static IP not in dynamic pool or static pool",
			allocateTimes: 1,
			param: &smf_context.UPFSelectionParams{
				Dnn: "internet",
				SNssai: &smf_context.SNssai{
					Sst: 1,
					Sd:  "111111",
				},
				PDUAddress: net.ParseIP("10.61.0.10"),
			},
			subnet:        61,
			useStaticIP:   false,
			expectedIP:    nil,
			expectedError: fmt.Errorf("all PSA UPF IP pools exhausted for selection params"),
		},
		{
			name:          "static IP not in static pool but in dynamic pool",
			allocateTimes: 1,
			param: &smf_context.UPFSelectionParams{
				Dnn: "internet",
				SNssai: &smf_context.SNssai{
					Sst: 2,
					Sd:  "222222",
				},
				PDUAddress: net.ParseIP("10.62.0.10").To4(),
			},
			subnet:        62,
			useStaticIP:   false,
			expectedIP:    net.ParseIP("10.62.0.10"),
			expectedError: nil,
		},
		{
			name:          "dynamic pool is exhausted",
			allocateTimes: 2,
			param: &smf_context.UPFSelectionParams{
				Dnn: "internet",
				SNssai: &smf_context.SNssai{
					Sst: 2,
					Sd:  "222222",
				},
				PDUAddress: net.ParseIP("10.62.0.10").To4(),
			},
			subnet:        62,
			useStaticIP:   false,
			expectedIP:    nil,
			expectedError: fmt.Errorf("all PSA UPF IP pools exhausted for selection params"),
		},
		{
			name:          "static IP is in static pool",
			allocateTimes: 1,
			param: &smf_context.UPFSelectionParams{
				Dnn: "internet",
				SNssai: &smf_context.SNssai{
					Sst: 3,
					Sd:  "333333",
				},
				PDUAddress: net.ParseIP("10.63.0.10").To4(),
			},
			subnet:        63,
			useStaticIP:   true,
			expectedIP:    net.ParseIP("10.63.0.10"),
			expectedError: nil,
		},
		{
			name:          "static pool is exhausted",
			allocateTimes: 2,
			param: &smf_context.UPFSelectionParams{
				Dnn: "internet",
				SNssai: &smf_context.SNssai{
					Sst: 3,
					Sd:  "333333",
				},
				PDUAddress: net.ParseIP("10.63.0.10").To4(),
			},
			subnet:        63,
			useStaticIP:   false,
			expectedIP:    nil,
			expectedError: fmt.Errorf("all PSA UPF IP pools exhausted for selection params"),
		},
		{
			name:          "static IP is in static pool, and dynamic pool is exhaust (allocate twice and not release)",
			allocateTimes: 2,
			param: &smf_context.UPFSelectionParams{
				Dnn: "internet",
				SNssai: &smf_context.SNssai{
					Sst: 3,
					Sd:  "333333",
				},
				PDUAddress: net.ParseIP("10.63.0.10").To4(),
			},
			subnet:        63,
			useStaticIP:   false,
			expectedIP:    nil,
			expectedError: fmt.Errorf("all PSA UPF IP pools exhausted for selection params"),
		},
	}

	userplaneInformation := smf_context.NewUserPlaneInformation(configForIPPoolAllocate)
	for _, upf := range userplaneInformation.UPFs {
		upf.UPFStatus = smf_context.AssociatedSetUpSuccess
		upf.Association, upf.AssociationCancelFunc = context.WithCancel(context.Background())
	}

	Convey("Given UE IP address to allocate and UPFSelectionParams, should return error if pool is exhausted or allocate UE IP", t, func() {
		for _, testcase := range testCases {
			Convey(testcase.name, func() {
				for times := 1; times <= testcase.allocateTimes; times++ {
					upf, allocatedIP, useStatic, err := userplaneInformation.SelectUPFAndAllocUEIP(testcase.param, "imsi-208930000000001")
					So(useStatic, ShouldEqual, testcase.useStaticIP)
					// TODO: assert selected UPF
					So(allocatedIP, ShouldEqual, testcase.expectedIP)
					if err != nil {
						So(err.Error(), ShouldEqual, testcase.expectedError.Error())
					} else {
						So(testcase.expectedError, ShouldBeNil)
					}

					// case 0 will not allocate IP
					// case 2 and 4 which allocateTimes is 2 are used to test scenario which pool IP is exhausted
					if allocatedIP != nil {
						var expectedIPPool []net.IP
						for i := 0; i <= 255; i++ {
							for j := 1; j <= 255; j++ {
								expectedIPPool = append(expectedIPPool, net.ParseIP(fmt.Sprintf("10.%d.%d.%d", testcase.subnet, i, j)).To4())
							}
						}
						So(allocatedIP, ShouldContain, expectedIPPool)
						userplaneInformation.ReleaseUEIP(upf, allocatedIP, testcase.useStaticIP)
					}
				}
			})
		}
	})
}
