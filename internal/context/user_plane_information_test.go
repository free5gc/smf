package context_test

import (
	"context"
	"fmt"
	"net"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

var configuration = &factory.UserPlaneInformation{
	UPNodes: map[string]factory.UPNodeConfigInterface{
		"GNodeB": &factory.GNBConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "AN",
			},
		},
		"UPF1": &factory.UPFConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "UPF",
			},
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
		"UPF2": &factory.UPFConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "UPF",
			},
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
		"UPF3": &factory.UPFConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "UPF",
			},
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
		"UPF4": &factory.UPFConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "UPF",
			},
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

	require.NotNil(t, userplaneInformation.AccessNetwork["GNodeB"])

	require.NotNil(t, userplaneInformation.UPFs["UPF1"])
	require.NotNil(t, userplaneInformation.UPFs["UPF2"])
	require.NotNil(t, userplaneInformation.UPFs["UPF3"])
	require.NotNil(t, userplaneInformation.UPFs["UPF4"])

	// check links
	require.Contains(t, userplaneInformation.AccessNetwork["GNodeB"].Links, userplaneInformation.UPFs["UPF1"])
	require.Contains(t, userplaneInformation.UPFs["UPF1"].Links, userplaneInformation.UPFs["UPF2"])
	require.Contains(t, userplaneInformation.UPFs["UPF2"].Links, userplaneInformation.UPFs["UPF3"])
	require.Contains(t, userplaneInformation.UPFs["UPF3"].Links, userplaneInformation.UPFs["UPF4"])
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
		upf.AssociationContext = context.Background()
	}

	for i := 0; i <= 100; i++ {
		upf, allocatedIP, _ := userplaneInformation.SelectUPFAndAllocUEIP(&smf_context.UPFSelectionParams{
			Dnn: "internet",
			SNssai: &smf_context.SNssai{
				Sst: 1,
				Sd:  "112232",
			},
		})

		require.Contains(t, expectedIPPool, allocatedIP)
		userplaneInformation.ReleaseUEIP(upf, allocatedIP, false)
	}
}

var configForIPPoolAllocate = &factory.UserPlaneInformation{
	UPNodes: map[string]factory.UPNodeConfigInterface{
		"GNodeB": &factory.GNBConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "AN",
			},
		},
		"UPF1": &factory.UPFConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "UPF",
			},
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
		"UPF2": &factory.UPFConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "UPF",
			},
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
		"UPF3": &factory.UPFConfig{
			UPNodeConfig: &factory.UPNodeConfig{
				Type: "UPF",
			},
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

var testCasesOfGetUEIPPool = []struct {
	name          string
	allocateTimes int
	param         *smf_context.UPFSelectionParams
	subnet        uint8
	useStaticIP   bool
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
		subnet:      61,
		useStaticIP: false,
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
		subnet:      62,
		useStaticIP: false,
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
		subnet:      62,
		useStaticIP: false,
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
		subnet:      63,
		useStaticIP: true,
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
		subnet:      63,
		useStaticIP: false,
	},
	{
		name:          "static IP is in static pool, and dynamic pool is exhaust(allocate twice and not release)",
		allocateTimes: 2,
		param: &smf_context.UPFSelectionParams{
			Dnn: "internet",
			SNssai: &smf_context.SNssai{
				Sst: 3,
				Sd:  "333333",
			},
			PDUAddress: net.ParseIP("10.63.0.10").To4(),
		},
		subnet:      63,
		useStaticIP: false,
	},
}

func TestGetUEIPPool(t *testing.T) {
	userplaneInformation := smf_context.NewUserPlaneInformation(configForIPPoolAllocate)
	for _, upf := range userplaneInformation.UPFs {
		upf.AssociationContext = context.Background()
	}

	for ci, tc := range testCasesOfGetUEIPPool {
		t.Run(tc.name, func(t *testing.T) {
			var expectedIPPool []net.IP
			for i := 0; i <= 255; i++ {
				for j := 1; j <= 255; j++ {
					expectedIPPool = append(expectedIPPool, net.ParseIP(fmt.Sprintf("10.%d.%d.%d", tc.subnet, i, j)).To4())
				}
			}

			var upf *smf_context.UPF
			var allocatedIP net.IP
			var useStatic bool
			for times := 1; times <= tc.allocateTimes; times++ {
				upf, allocatedIP, useStatic = userplaneInformation.SelectUPFAndAllocUEIP(tc.param)
			}

			require.Equal(t, tc.useStaticIP, useStatic)
			// case 0 will not allocate IP
			// case 2 and 4 which allocateTimes is 2 are used to test scenario which pool IP is exhausted
			if ci == 0 || tc.allocateTimes > 1 {
				require.Nil(t, allocatedIP)
			} else {
				require.Contains(t, expectedIPPool, allocatedIP)
				userplaneInformation.ReleaseUEIP(upf, allocatedIP, tc.useStaticIP)
			}
		})
	}
}

func TestConfigToNodeID(t *testing.T) {
	testCases := []struct {
		name           string
		configNodeID   string
		expectedNodeID pfcpType.NodeID
		expectedError  error
	}{
		{
			name:         "IPv4",
			configNodeID: "192.168.179.100",
			expectedNodeID: pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeIpv4Address,
				IP:         net.ParseIP("192.168.179.100").To4(),
			},
			expectedError: nil,
		},
		{
			name:         "IPv4 CIDR",
			configNodeID: "192.168.179.100/24",
			expectedNodeID: pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeIpv4Address,
				IP:         net.ParseIP("192.168.179.100").To4(),
			},
			expectedError: nil,
		},
		{
			name:           "IPv4 error",
			configNodeID:   "192.168.179.1111",
			expectedNodeID: pfcpType.NodeID{},
			expectedError:  fmt.Errorf("input %s is not a valid IP address or resolvable FQDN", "192.168.179.1111"),
		},
		{
			name:         "IPv6",
			configNodeID: "2001:41b8:810:20:df55:785b:e4ed:15b8",
			expectedNodeID: pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeIpv6Address,
				IP:         net.ParseIP("2001:41b8:810:20:df55:785b:e4ed:15b8"),
			},
			expectedError: nil,
		},
		{
			name:         "IPv6 CIDR",
			configNodeID: "2001:41b8:810:20:df55:785b:e4ed:15b8/64",
			expectedNodeID: pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeIpv6Address,
				IP:         net.ParseIP("2001:41b8:810:20:df55:785b:e4ed:15b8"),
			},
			expectedError: nil,
		},
		{
			name:           "IPv6 error",
			configNodeID:   "2001:810:20:df55:785b:e4ed:15b8",
			expectedNodeID: pfcpType.NodeID{},
			expectedError: fmt.Errorf("input %s is not a valid IP address or resolvable FQDN",
				"2001:810:20:df55:785b:e4ed:15b8"),
		},
		{
			name:         "IPv6 short",
			configNodeID: "::1",
			expectedNodeID: pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeIpv6Address,
				IP:         net.ParseIP("::1"),
			},
			expectedError: nil,
		},
		{
			name:         "FQDN",
			configNodeID: "example.com",
			expectedNodeID: pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeFqdn,
				FQDN:       "example.com",
			},
			expectedError: nil,
		},
		{
			name:           "FQDN error",
			configNodeID:   "notresolving.example.com",
			expectedNodeID: pfcpType.NodeID{},
			expectedError:  fmt.Errorf("input %s is not a valid IP address or resolvable FQDN", "notresolving.example.com"),
		},
	}

	Convey("Should convert config input string to valid NodeID or throw error", t, func() {
		for i, testcase := range testCases {
			infoStr := fmt.Sprintf("testcase[%d]: %s", i, testcase.name)

			Convey(infoStr, func() {
				nodeID, err := smf_context.ConfigToNodeID(testcase.configNodeID)

				if testcase.expectedError == nil {
					So(err, ShouldBeNil)
					So(nodeID.NodeIdType, ShouldEqual, testcase.expectedNodeID.NodeIdType)
					So(nodeID.IP, ShouldEqual, testcase.expectedNodeID.IP)
					So(nodeID.FQDN, ShouldEqual, testcase.expectedNodeID.FQDN)
				} else {
					So(err, ShouldNotBeNil)
					if err != nil {
						So(err.Error(), ShouldEqual, testcase.expectedError.Error())
					}
				}
			})
		}
	})
}
