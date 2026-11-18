package processor_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/processor"
	"github.com/free5gc/util/idgenerator"
)

// contents builds an ie.PacketFilterContents with the "absent" sentinels the
// SMF packet filter builder always sets ("any"/"assigned").
func contents(remoteAddr, remotePorts, localAddr, localPorts string) nasie.PacketFilterContents {
	if remoteAddr == "" {
		remoteAddr = "any"
	}
	if localAddr == "" {
		localAddr = "assigned"
	}
	return nasie.PacketFilterContents{
		RemoteAddr:      remoteAddr,
		RemotePortRange: remotePorts,
		LocalAddr:       localAddr,
		LocalPortRange:  localPorts,
	}
}

func TestBuildNASPacketFilterFromPacketFilterInfo(t *testing.T) {
	testCases := []struct {
		name         string
		packetFilter []nasie.PacketFilter
		flowInfo     models.Pcf_SMPolCtrl_FlowInformation
	}{
		{
			name: "MatchAll",
			packetFilter: []nasie.PacketFilter{
				{
					Dir:      nasie.PFD_BiDir,
					Contents: contents("", "", "", ""),
				},
			},
			flowInfo: models.Pcf_SMPolCtrl_FlowInformation{
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				FlowDescription: "permit out ip from any to assigned",
			},
		},
		{
			name: "MatchIPNet1",
			packetFilter: []nasie.PacketFilter{
				{
					Dir:      nasie.PFD_Uplink,
					Contents: contents("", "", "192.168.0.0/16", ""),
				},
			},
			flowInfo: models.Pcf_SMPolCtrl_FlowInformation{
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_UPLINK,
				FlowDescription: "permit out ip from any to 192.168.0.0/16",
			},
		},
		{
			name: "MatchIPNet2",
			packetFilter: []nasie.PacketFilter{
				{
					Dir:      nasie.PFD_BiDir,
					Contents: contents("10.160.20.0/24", "", "192.168.0.0/16", ""),
				},
			},
			flowInfo: models.Pcf_SMPolCtrl_FlowInformation{
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				FlowDescription: "permit out ip from 10.160.20.0/24 to 192.168.0.0/16",
			},
		},
		{
			name: "MatchIPNetPort",
			packetFilter: []nasie.PacketFilter{
				{
					Dir:      nasie.PFD_BiDir,
					Contents: contents("10.160.20.0/24", "", "192.168.0.0/16", "8000"),
				},
			},
			flowInfo: models.Pcf_SMPolCtrl_FlowInformation{
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				FlowDescription: "permit out ip from 10.160.20.0/24 to 192.168.0.0/16 8000",
			},
		},
		{
			name: "MatchIPNetPortRanges",
			packetFilter: []nasie.PacketFilter{
				{
					Dir:      nasie.PFD_Downlink,
					Contents: contents("10.160.20.0/24", "", "192.168.0.0/16", "3000-8000"),
				},
			},
			flowInfo: models.Pcf_SMPolCtrl_FlowInformation{
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_DOWNLINK,
				FlowDescription: "permit out ip from 10.160.20.0/24 to 192.168.0.0/16 3000-8000",
			},
		},
		{
			name: "MatchIPNetPortRanges2",
			packetFilter: []nasie.PacketFilter{
				{
					Dir:      nasie.PFD_Downlink,
					Contents: contents("10.160.20.0/24", "3000-4000", "192.168.0.0/16", "6000-8000"),
				},
			},
			flowInfo: models.Pcf_SMPolCtrl_FlowInformation{
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_DOWNLINK,
				FlowDescription: "permit out ip from 10.160.20.0/24 3000-4000 to 192.168.0.0/16 6000-8000",
			},
		},
		{
			name: "MatchIPNetPortRanges3",
			packetFilter: []nasie.PacketFilter{
				{
					Dir:      nasie.PFD_Downlink,
					Contents: contents("10.160.20.0/24", "3000-4000", "192.168.0.0/16", "6000-7000"),
				},
				{
					Dir:      nasie.PFD_Downlink,
					Contents: contents("10.160.20.0/24", "3000-4000", "192.168.0.0/16", "8000"),
				},
			},
			flowInfo: models.Pcf_SMPolCtrl_FlowInformation{
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_DOWNLINK,
				FlowDescription: "permit out ip from 10.160.20.0/24 3000-4000 to 192.168.0.0/16 6000-7000,8000",
			},
		},
		{
			name: "MatchIPNetPortRanges4",
			packetFilter: []nasie.PacketFilter{
				{
					Dir:      nasie.PFD_Downlink,
					Contents: contents("10.160.20.0/24", "3000-4000", "192.168.0.0/16", "6000-7000"),
				},
				{
					Dir:      nasie.PFD_Downlink,
					Contents: contents("10.160.20.0/24", "5000", "192.168.0.0/16", "6000-7000"),
				},
			},
			flowInfo: models.Pcf_SMPolCtrl_FlowInformation{
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_DOWNLINK,
				FlowDescription: "permit out ip from 10.160.20.0/24 3000-4000,5000 to 192.168.0.0/16 6000-7000",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			smCtx := &context.SMContext{
				PacketFilterIDGenerator: idgenerator.NewGenerator(1, 255),
				PacketFilterIDToNASPFID: make(map[string]uint8),
			}
			packetFilters, err := context.BuildNASPacketFiltersFromFlowInformation(&tc.flowInfo, smCtx)
			require.NoError(t, err)

			require.Len(t, packetFilters, len(tc.packetFilter))
			for i, pf := range packetFilters {
				require.Equal(t, tc.packetFilter[i].Dir, pf.Dir)
				require.Equal(t, tc.packetFilter[i].Contents, pf.Contents)
			}
		})
	}
}

func TestHandlePDUSessionEstablishmentRequestAlwaysOnRequested(t *testing.T) {
	testCases := []struct {
		name     string
		req      *nasie.AlwaysonPDUSessReq
		included bool
	}{
		{name: "Absent", req: nil, included: false},
		{name: "Requested", req: &nasie.AlwaysonPDUSessReq{APSR: true}, included: true},
		{name: "IncludedNotRequested", req: &nasie.AlwaysonPDUSessReq{APSR: false}, included: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			smCtx := &context.SMContext{}
			req := &message.PDUSessEstReq{
				PDUSessId:          5,
				PTI:                1,
				AlwaysonPDUSessReq: tc.req,
			}
			require.NoError(t, processor.HandlePDUSessionEstablishmentRequest(smCtx, req))
			require.Equal(t, tc.included, smCtx.AlwaysOnPDUSessionRequested)
		})
	}
}
