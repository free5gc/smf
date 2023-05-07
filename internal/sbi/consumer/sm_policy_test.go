package consumer

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/nas/nasType"
	"github.com/free5gc/openapi/models"
)

func TestBuildPacketFilterInfoFromNASPacketFilter(t *testing.T) {
	testCases := []struct {
		name             string
		packetFilter     nasType.PacketFilter
		packetFilterInfo models.PacketFilterInfo
	}{
		{
			name: "MatchAll",
			packetFilter: nasType.PacketFilter{
				Direction: nasType.PacketFilterDirectionBidirectional,
				Components: nasType.PacketFilterComponentList{
					&nasType.PacketFilterMatchAll{},
				},
			},
			packetFilterInfo: models.PacketFilterInfo{
				FlowDirection: models.FlowDirection_BIDIRECTIONAL,
				PackFiltCont:  "permit out ip from any to any",
			},
		},
		{
			name: "MatchIPNet1",
			packetFilter: nasType.PacketFilter{
				Direction: nasType.PacketFilterDirectionUplink,
				Components: nasType.PacketFilterComponentList{
					&nasType.PacketFilterIPv4LocalAddress{
						Address: net.ParseIP("192.168.0.0").To4(),
						Mask:    net.IPv4Mask(255, 255, 0, 0),
					},
				},
			},
			packetFilterInfo: models.PacketFilterInfo{
				FlowDirection: models.FlowDirection_UPLINK,
				PackFiltCont:  "permit out ip from any to 192.168.0.0/16",
			},
		},
		{
			name: "MatchIPNet2",
			packetFilter: nasType.PacketFilter{
				Direction: nasType.PacketFilterDirectionBidirectional,
				Components: nasType.PacketFilterComponentList{
					&nasType.PacketFilterIPv4LocalAddress{
						Address: net.ParseIP("192.168.0.0").To4(),
						Mask:    net.IPv4Mask(255, 255, 0, 0),
					},
					&nasType.PacketFilterIPv4RemoteAddress{
						Address: net.ParseIP("10.160.20.0").To4(),
						Mask:    net.IPv4Mask(255, 255, 255, 0),
					},
				},
			},
			packetFilterInfo: models.PacketFilterInfo{
				FlowDirection: models.FlowDirection_BIDIRECTIONAL,
				PackFiltCont:  "permit out ip from 10.160.20.0/24 to 192.168.0.0/16",
			},
		},
		{
			name: "MatchIPNetPort",
			packetFilter: nasType.PacketFilter{
				Direction: nasType.PacketFilterDirectionBidirectional,
				Components: nasType.PacketFilterComponentList{
					&nasType.PacketFilterIPv4LocalAddress{
						Address: net.ParseIP("192.168.0.0").To4(),
						Mask:    net.IPv4Mask(255, 255, 0, 0),
					},
					&nasType.PacketFilterSingleLocalPort{
						Value: 8000,
					},
					&nasType.PacketFilterIPv4RemoteAddress{
						Address: net.ParseIP("10.160.20.0").To4(),
						Mask:    net.IPv4Mask(255, 255, 255, 0),
					},
				},
			},
			packetFilterInfo: models.PacketFilterInfo{
				FlowDirection: models.FlowDirection_BIDIRECTIONAL,
				PackFiltCont:  "permit out ip from 10.160.20.0/24 to 192.168.0.0/16 8000",
			},
		},
		{
			name: "MatchIPNetPortRanges",
			packetFilter: nasType.PacketFilter{
				Direction: nasType.PacketFilterDirectionDownlink,
				Components: nasType.PacketFilterComponentList{
					&nasType.PacketFilterIPv4LocalAddress{
						Address: net.ParseIP("192.168.0.0").To4(),
						Mask:    net.IPv4Mask(255, 255, 0, 0),
					},
					&nasType.PacketFilterLocalPortRange{
						LowLimit:  3000,
						HighLimit: 8000,
					},
					&nasType.PacketFilterIPv4RemoteAddress{
						Address: net.ParseIP("10.160.20.0").To4(),
						Mask:    net.IPv4Mask(255, 255, 255, 0),
					},
					&nasType.PacketFilterRemotePortRange{
						LowLimit:  10000,
						HighLimit: 30000,
					},
				},
			},
			packetFilterInfo: models.PacketFilterInfo{
				FlowDirection: models.FlowDirection_DOWNLINK,
				PackFiltCont:  "permit out ip from 10.160.20.0/24 10000-30000 to 192.168.0.0/16 3000-8000",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			packetFilter, err := buildPktFilterInfo(tc.packetFilter)
			require.NoError(t, err)
			require.Equal(t, tc.packetFilterInfo.FlowLabel, packetFilter.FlowLabel)
			require.Equal(t, tc.packetFilterInfo.Spi, packetFilter.Spi)
			require.Equal(t, tc.packetFilterInfo.TosTrafficClass, packetFilter.TosTrafficClass)
			require.Equal(t, tc.packetFilterInfo.FlowDirection, packetFilter.FlowDirection)
			require.Equal(t, tc.packetFilterInfo.PackFiltCont, packetFilter.PackFiltCont)
		})
	}
}
