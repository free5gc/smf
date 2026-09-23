package message_test

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	goPfcpMessage "github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/smf/internal/context"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

var testNodeID = &pfcptype.NodeID{
	NodeIdType: pfcptype.NodeIdTypeIpv4Address,
	IP:         net.ParseIP("10.4.0.1").To4(),
}

func TestBuildSessionEstablishmentRequestMapsRulesWithGoPFCP(t *testing.T) {
	initDirectBuilderTestContext(t)
	const (
		upfIP     = "10.4.0.1"
		upfUUID   = "direct-builder-upf"
		localSEID = uint64(0x0102030405060708)
	)
	smContext := context.NewSMContext("imsi-208930000000101", 10)
	smContext.PFCPContext[upfIP] = &context.PFCPSessionContext{LocalSEID: localSEID}

	bar := &context.BAR{
		BARID: 7,
		DownlinkDataNotificationDelay: pfcptype.DownlinkDataNotificationDelay{
			DelayValue: 3,
		},
		State: context.RULE_INITIAL,
	}
	qer := &context.QER{
		QERID:      300,
		QFI:        pfcptype.QFI{QFI: 9},
		GateStatus: &pfcptype.GateStatus{ULGate: pfcptype.GateOpen, DLGate: pfcptype.GateClose},
		MBR:        &pfcptype.MBR{ULMBR: 100_000, DLMBR: 200_000},
		GBR:        &pfcptype.GBR{ULGBR: 10_000, DLGBR: 20_000},
		State:      context.RULE_INITIAL,
	}
	urr := &context.URR{
		URRID:             400,
		MeasureMethod:     context.MesureMethodVol,
		MeasurementPeriod: 30 * time.Second,
		QuotaValidityTime: time.Date(1900, time.January, 1, 0, 2, 0, 0, time.UTC),
		VolumeThreshold:   1_000,
		VolumeQuota:       2_000,
		ReportingTrigger: pfcptype.ReportingTrigger{
			Flags: pfcptype.RPT_TRIG_PERIO | pfcptype.RPT_TRIG_VOLTH | pfcptype.RPT_TRIG_VOLQU,
		},
		MeasurementInformation: pfcptype.MeasurementInformation{
			Flags: pfcptype.MeasureInfoMNOP | pfcptype.MeasureInfoMBQE,
		},
	}
	far := &context.FAR{
		FARID: 200,
		ForwardingParameters: &context.ForwardingParameters{
			DestinationInterface: pfcptype.DestinationInterface{
				InterfaceValue: pfcptype.DestinationInterfaceCore,
			},
			NetworkInstance: &pfcptype.NetworkInstance{
				NetworkInstance: "internet.example",
				FQDNEncoding:    true,
			},
			OuterHeaderCreation: &pfcptype.OuterHeaderCreation{
				OuterHeaderCreationDescription: pfcptype.OuterHeaderCreationGtpUUdpIpv4,
				Teid:                           0x11223344,
				Ipv4Address:                    net.ParseIP("198.51.100.10").To4(),
			},
			ForwardingPolicyID: "route-blue",
		},
		BAR:   bar,
		State: context.RULE_INITIAL,
	}
	pdr := &context.PDR{
		PDRID:      100,
		Precedence: 0x01020304,
		PDI: context.PDI{
			SourceInterface: pfcptype.SourceInterface{InterfaceValue: pfcptype.SourceInterfaceAccess},
			LocalFTeid: &pfcptype.FTEID{
				V4:          true,
				Teid:        0xaabbccdd,
				Ipv4Address: net.ParseIP("192.0.2.10").To4(),
			},
			NetworkInstance: &pfcptype.NetworkInstance{NetworkInstance: "internet"},
			UEIPAddress: &pfcptype.UEIPAddress{
				Sd:          true,
				V4:          true,
				Ipv4Address: net.ParseIP("10.60.0.1").To4(),
			},
			SDFFilter: &pfcptype.SDFFilter{
				Fd:                      true,
				LengthOfFlowDescription: uint16(len("permit out ip from any to assigned")),
				FlowDescription:         []byte("permit out ip from any to assigned"),
			},
			ApplicationID: "video-app",
		},
		OuterHeaderRemoval: &pfcptype.OuterHeaderRemoval{
			OuterHeaderRemovalDescription: pfcptype.OuterHeaderRemovalGtpUUdpIpv4,
		},
		FAR:   far,
		URR:   []*context.URR{urr},
		QER:   []*context.QER{qer},
		State: context.RULE_INITIAL,
	}
	smContext.RegisterUrr(upfUUID, urr)

	request, err := pfcp_message.BuildSessionEstablishmentRequest(
		*testNodeID,
		upfIP,
		upfUUID,
		smContext,
		[]*context.PDR{pdr},
		[]*context.FAR{far},
		[]*context.BAR{bar},
		[]*context.QER{qer},
		[]*context.URR{urr},
	)
	require.NoError(t, err)

	wire, err := request.Marshal()
	require.NoError(t, err)
	parsedMessage, err := goPfcpMessage.Parse(wire)
	require.NoError(t, err)
	parsed, ok := parsedMessage.(*goPfcpMessage.SessionEstablishmentRequest)
	require.True(t, ok, "parsed message type = %T", parsedMessage)

	nodeID, err := parsed.NodeID.NodeID()
	require.NoError(t, err)
	require.Equal(t, "10.4.0.1", nodeID)
	fseid, err := parsed.CPFSEID.FSEID()
	require.NoError(t, err)
	require.Equal(t, localSEID, fseid.SEID)
	require.Equal(t, net.ParseIP("10.4.0.1").To4(), fseid.IPv4Address)
	pdnType, err := parsed.PDNType.PDNType()
	require.NoError(t, err)
	require.Equal(t, ie.PDNTypeIPv4, pdnType)
	require.Len(t, parsed.CreatePDR, 1)
	require.Len(t, parsed.CreateFAR, 1)
	require.Len(t, parsed.CreateURR, 1)
	require.Len(t, parsed.CreateQER, 1)
	require.NotNil(t, parsed.CreateBAR)

	pdrChildren, err := parsed.CreatePDR[0].CreatePDR()
	require.NoError(t, err)
	requireIEUint16(t, findIE(t, pdrChildren, ie.PDRID), 100, (*ie.IE).PDRID)
	requireIEUint32(t, findIE(t, pdrChildren, ie.Precedence), 0x01020304, (*ie.IE).Precedence)
	requireIEUint32(t, findIE(t, pdrChildren, ie.FARID), 200, (*ie.IE).FARID)
	requireIEUint32(t, findIE(t, pdrChildren, ie.URRID), 400, (*ie.IE).URRID)
	requireIEUint32(t, findIE(t, pdrChildren, ie.QERID), 300, (*ie.IE).QERID)
	removalDescription, err := findIE(t, pdrChildren, ie.OuterHeaderRemoval).OuterHeaderRemovalDescription()
	require.NoError(t, err)
	require.Equal(t, pfcptype.OuterHeaderRemovalGtpUUdpIpv4, removalDescription)

	pdiChildren, err := findIE(t, pdrChildren, ie.PDI).PDI()
	require.NoError(t, err)
	sourceInterface, err := findIE(t, pdiChildren, ie.SourceInterface).SourceInterface()
	require.NoError(t, err)
	require.Equal(t, pfcptype.SourceInterfaceAccess, sourceInterface)
	fteid, err := findIE(t, pdiChildren, ie.FTEID).FTEID()
	require.NoError(t, err)
	require.True(t, fteid.HasIPv4())
	require.Equal(t, uint32(0xaabbccdd), fteid.TEID)
	require.Equal(t, net.ParseIP("192.0.2.10").To4(), fteid.IPv4Address)
	networkInstance, err := findIE(t, pdiChildren, ie.NetworkInstance).NetworkInstance()
	require.NoError(t, err)
	require.Equal(t, "internet", networkInstance)
	ueIP, err := findIE(t, pdiChildren, ie.UEIPAddress).UEIPAddress()
	require.NoError(t, err)
	require.Equal(t, uint8(0x06), ueIP.Flags)
	require.Equal(t, net.ParseIP("10.60.0.1").To4(), ueIP.IPv4Address)
	sdf, err := findIE(t, pdiChildren, ie.SDFFilter).SDFFilter()
	require.NoError(t, err)
	require.Equal(t, "permit out ip from any to assigned", sdf.FlowDescription)
	applicationID, err := findIE(t, pdiChildren, ie.ApplicationID).ApplicationID()
	require.NoError(t, err)
	require.Equal(t, "video-app", applicationID)

	farChildren, err := parsed.CreateFAR[0].CreateFAR()
	require.NoError(t, err)
	applyAction := findIE(t, farChildren, ie.ApplyAction)
	require.True(t, applyAction.HasFORW())
	require.False(t, applyAction.HasDROP())
	forwardingChildren, err := findIE(t, farChildren, ie.ForwardingParameters).ForwardingParameters()
	require.NoError(t, err)
	destinationInterface, err := findIE(t, forwardingChildren, ie.DestinationInterface).DestinationInterface()
	require.NoError(t, err)
	require.Equal(t, pfcptype.DestinationInterfaceCore, destinationInterface)
	forwardingNetworkInstance, err := findIE(t, forwardingChildren, ie.NetworkInstance).NetworkInstanceFQDN()
	require.NoError(t, err)
	require.Equal(t, "internet.example", forwardingNetworkInstance)
	outerHeaderIE := findIE(t, forwardingChildren, ie.OuterHeaderCreation)
	require.GreaterOrEqual(t, len(outerHeaderIE.Payload), 2)
	require.Equal(t, []byte{0x01, 0x00}, outerHeaderIE.Payload[:2],
		"Outer Header Creation description must use spec-defined network byte order")
	outerHeader, err := outerHeaderIE.OuterHeaderCreation()
	require.NoError(t, err)
	require.Equal(t, uint16(0x0100), outerHeader.OuterHeaderCreationDescription)
	require.Equal(t, uint32(0x11223344), outerHeader.TEID)
	require.Equal(t, net.ParseIP("198.51.100.10").To4(), outerHeader.IPv4Address)
	policy, err := findIE(t, forwardingChildren, ie.ForwardingPolicy).ForwardingPolicy()
	require.NoError(t, err)
	require.Equal(t, byte(len("route-blue")), policy[0])
	require.Equal(t, "route-blue", string(policy[1:]))

	qerChildren, err := parsed.CreateQER[0].CreateQER()
	require.NoError(t, err)
	ulGate, dlGate, err := findIE(t, qerChildren, ie.GateStatus).GateStatusULDL()
	require.NoError(t, err)
	require.Equal(t, uint8(pfcptype.GateOpen), ulGate)
	require.Equal(t, uint8(pfcptype.GateClose), dlGate)
	requireIEUint64(t, findIE(t, qerChildren, ie.MBR), 100_000, (*ie.IE).MBRUL)
	requireIEUint64(t, findIE(t, qerChildren, ie.MBR), 200_000, (*ie.IE).MBRDL)
	requireIEUint64(t, findIE(t, qerChildren, ie.GBR), 10_000, (*ie.IE).GBRUL)
	requireIEUint64(t, findIE(t, qerChildren, ie.GBR), 20_000, (*ie.IE).GBRDL)
	qfi, err := findIE(t, qerChildren, ie.QFI).QFI()
	require.NoError(t, err)
	require.Equal(t, uint8(9), qfi)

	urrChildren, err := parsed.CreateURR[0].CreateURR()
	require.NoError(t, err)
	measurementMethod := findIE(t, urrChildren, ie.MeasurementMethod)
	require.True(t, measurementMethod.HasVOLUM())
	require.False(t, measurementMethod.HasDURAT())
	reportingTriggers := findIE(t, urrChildren, ie.ReportingTriggers)
	require.True(t, reportingTriggers.HasPERIO())
	require.True(t, reportingTriggers.HasVOLTH())
	require.True(t, reportingTriggers.HasVOLQU())
	measurementPeriod, err := findIE(t, urrChildren, ie.MeasurementPeriod).MeasurementPeriod()
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, measurementPeriod)
	threshold, err := findIE(t, urrChildren, ie.VolumeThreshold).VolumeThreshold()
	require.NoError(t, err)
	require.False(t, threshold.HasTOVOL())
	require.True(t, threshold.HasULVOL())
	require.True(t, threshold.HasDLVOL())
	require.Equal(t, uint64(1_000), threshold.UplinkVolume)
	require.Equal(t, uint64(1_000), threshold.DownlinkVolume)
	quota, err := findIE(t, urrChildren, ie.VolumeQuota).VolumeQuota()
	require.NoError(t, err)
	require.True(t, quota.HasTOVOL())
	require.True(t, quota.HasULVOL())
	require.True(t, quota.HasDLVOL())
	require.Equal(t, uint64(2_000), quota.TotalVolume)
	measurementInformation, err := findIE(t, urrChildren, ie.MeasurementInformation).MeasurementInformation()
	require.NoError(t, err)
	require.Equal(t, uint8(0x11), measurementInformation)
	quotaValidity, err := findIE(t, urrChildren, ie.QuotaValidityTime).QuotaValidityTime()
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, quotaValidity)

	barChildren, err := parsed.CreateBAR.CreateBAR()
	require.NoError(t, err)
	barID, err := findIE(t, barChildren, ie.BARID).BARID()
	require.NoError(t, err)
	require.Equal(t, uint8(7), barID)
	for _, child := range barChildren {
		require.NotEqual(t, ie.DownlinkDataNotificationDelay, child.Type,
			"Downlink Data Notification Delay must be omitted from N4 Create BAR")
	}

	require.Equal(t, context.RULE_CREATE, pdr.State)
	require.Equal(t, context.RULE_CREATE, far.State)
	require.Equal(t, context.RULE_CREATE, bar.State)
	require.Equal(t, context.RULE_CREATE, qer.State)
	require.Equal(t, context.RULE_CREATE, smContext.GetUrrState(upfUUID, urr.URRID))
}

func TestBuildSessionEstablishmentRequestRejectsMultipleCreateBARs(t *testing.T) {
	initDirectBuilderTestContext(t)
	smContext := context.NewSMContext("imsi-208930000000102", 10)
	smContext.PFCPContext["10.4.0.1"] = &context.PFCPSessionContext{LocalSEID: 102}

	_, err := pfcp_message.BuildSessionEstablishmentRequest(
		*testNodeID,
		"10.4.0.1",
		"test-upf",
		smContext,
		nil,
		nil,
		[]*context.BAR{
			{BARID: 1, State: context.RULE_INITIAL},
			{BARID: 2, State: context.RULE_INITIAL},
		},
		nil,
		nil,
	)
	require.ErrorContains(t, err, "2 Create BAR IEs, want at most 1")
}

func initDirectBuilderTestContext(t *testing.T) {
	t.Helper()
	config := &factory.Config{
		Info: &factory.Info{Version: "1.0.0", Description: "direct go-pfcp builder test"},
		Configuration: &factory.Configuration{
			Sbi: &factory.Sbi{
				Scheme: "http", RegisterIPv4: "127.0.0.1", BindingIPv4: "127.0.0.1", Port: 8000,
			},
			PFCP: &factory.PFCP{
				ListenAddr: "127.0.0.1", ExternalAddr: "10.4.0.1", NodeID: "10.4.0.1",
			},
		},
	}
	require.NoError(t, context.InitSmfContext(config))
}

func findIE(t *testing.T, ies []*ie.IE, ieType ie.IEType) *ie.IE {
	t.Helper()
	for _, candidate := range ies {
		if candidate != nil && candidate.Type == ieType {
			return candidate
		}
	}
	t.Fatalf("IE type %d not found in %v", ieType, ies)
	return nil
}

func requireIEUint16(
	t *testing.T,
	candidate *ie.IE,
	want uint16,
	getter func(*ie.IE) (uint16, error),
) {
	t.Helper()
	got, err := getter(candidate)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func requireIEUint32(
	t *testing.T,
	candidate *ie.IE,
	want uint32,
	getter func(*ie.IE) (uint32, error),
) {
	t.Helper()
	got, err := getter(candidate)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func requireIEUint64(
	t *testing.T,
	candidate *ie.IE,
	want uint64,
	getter func(*ie.IE) (uint64, error),
) {
	t.Helper()
	got, err := getter(candidate)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
