package message_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	goPfcpMessage "github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/smf/internal/context"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func TestBuildSessionModificationRequestMapsUpdatesDirectly(t *testing.T) {
	initDirectBuilderTestContext(t)
	const (
		upfIP      = "10.4.0.2"
		upfUUID    = "modification-upf"
		localSEID  = uint64(101)
		remoteSEID = uint64(202)
	)
	smContext := context.NewSMContext("imsi-208930000000103", 10)
	smContext.PFCPContext[upfIP] = &context.PFCPSessionContext{
		LocalSEID: localSEID, RemoteSEID: remoteSEID,
	}

	far := &context.FAR{
		FARID:       22,
		State:       context.RULE_UPDATE,
		ApplyAction: pfcptype.ApplyAction{Forw: true},
		ForwardingParameters: &context.ForwardingParameters{
			DestinationInterface: pfcptype.DestinationInterface{
				InterfaceValue: pfcptype.DestinationInterfaceAccess,
			},
			OuterHeaderCreation: &pfcptype.OuterHeaderCreation{
				OuterHeaderCreationDescription: pfcptype.OuterHeaderCreationGtpUUdpIpv4,
				Teid:                           0x11223344,
				Ipv4Address:                    net.ParseIP("198.51.100.20").To4(),
			},
			SendEndMarker: true,
		},
	}
	bar := &context.BAR{BARID: 3, State: context.RULE_INITIAL}
	urr := &context.URR{URRID: 44}
	smContext.RegisterUrr(upfUUID, urr)
	smContext.SetUrrState(upfUUID, urr.URRID, context.RULE_QUERY)

	request, err := pfcp_message.BuildSessionModificationRequest(
		upfIP, upfUUID, smContext,
		nil, []*context.FAR{far}, []*context.BAR{bar}, nil, []*context.URR{urr},
	)
	require.NoError(t, err)
	wire, err := request.Marshal()
	require.NoError(t, err)
	parsedMessage, err := goPfcpMessage.Parse(wire)
	require.NoError(t, err)
	parsed, ok := parsedMessage.(*goPfcpMessage.SessionModificationRequest)
	require.True(t, ok, "parsed message type = %T", parsedMessage)

	require.Equal(t, remoteSEID, parsed.SEID())
	cpFSEID, err := parsed.CPFSEID.FSEID()
	require.NoError(t, err)
	require.Equal(t, localSEID, cpFSEID.SEID)
	require.Len(t, parsed.UpdateFAR, 1)
	farChildren, err := parsed.UpdateFAR[0].UpdateFAR()
	require.NoError(t, err)
	requireIEUint32(t, findIE(t, farChildren, ie.FARID), far.FARID, (*ie.IE).FARID)
	require.True(t, findIE(t, farChildren, ie.ApplyAction).HasFORW())

	forwardingChildren, err := findIE(t, farChildren, ie.UpdateForwardingParameters).UpdateForwardingParameters()
	require.NoError(t, err)
	outerHeaderIE := findIE(t, forwardingChildren, ie.OuterHeaderCreation)
	require.GreaterOrEqual(t, len(outerHeaderIE.Payload), 2)
	require.Equal(t, []byte{0x01, 0x00}, outerHeaderIE.Payload[:2],
		"Outer Header Creation description must use spec-defined network byte order")
	outerHeader, err := outerHeaderIE.OuterHeaderCreation()
	require.NoError(t, err)
	require.Equal(t, pfcptype.OuterHeaderCreationGtpUUdpIpv4, outerHeader.OuterHeaderCreationDescription)
	require.Equal(t, uint32(0x11223344), outerHeader.TEID)
	require.Equal(t, net.ParseIP("198.51.100.20").To4(), outerHeader.IPv4Address)
	flags, err := findIE(t, forwardingChildren, ie.PFCPSMReqFlags).PFCPSMReqFlags()
	require.NoError(t, err)
	require.Equal(t, uint8(0x02), flags)

	require.NotNil(t, parsed.CreateBAR)
	barChildren, err := parsed.CreateBAR.CreateBAR()
	require.NoError(t, err)
	barID, err := findIE(t, barChildren, ie.BARID).BARID()
	require.NoError(t, err)
	require.Equal(t, uint8(3), barID)
	for _, child := range barChildren {
		require.NotEqual(t, ie.DownlinkDataNotificationDelay, child.Type)
	}

	require.Len(t, parsed.QueryURR, 1)
	queryChildren, err := parsed.QueryURR[0].QueryURR()
	require.NoError(t, err)
	requireIEUint32(t, findIE(t, queryChildren, ie.URRID), urr.URRID, (*ie.IE).URRID)
	require.Equal(t, context.RULE_CREATE, far.State)
	require.Equal(t, context.RULE_CREATE, smContext.GetUrrState(upfUUID, urr.URRID))
}

func TestBuildSessionModificationUpdateURRKeepsVolumeThresholdSemantics(t *testing.T) {
	initDirectBuilderTestContext(t)
	const (
		upfIP      = "10.4.0.3"
		upfUUID    = "modification-threshold-upf"
		localSEID  = uint64(303)
		remoteSEID = uint64(404)
	)
	smContext := context.NewSMContext("imsi-208930000000104", 10)
	smContext.PFCPContext[upfIP] = &context.PFCPSessionContext{
		LocalSEID: localSEID, RemoteSEID: remoteSEID,
	}
	urr := &context.URR{
		URRID:           45,
		MeasureMethod:   context.MesureMethodVol,
		VolumeThreshold: 1_000,
	}
	smContext.RegisterUrr(upfUUID, urr)
	smContext.SetUrrState(upfUUID, urr.URRID, context.RULE_UPDATE)

	request, err := pfcp_message.BuildSessionModificationRequest(
		upfIP, upfUUID, smContext, nil, nil, nil, nil, []*context.URR{urr},
	)
	require.NoError(t, err)
	wire, err := request.Marshal()
	require.NoError(t, err)
	parsedMessage, err := goPfcpMessage.Parse(wire)
	require.NoError(t, err)
	parsed, ok := parsedMessage.(*goPfcpMessage.SessionModificationRequest)
	require.True(t, ok, "parsed message type = %T", parsedMessage)
	require.Len(t, parsed.UpdateURR, 1)

	urrChildren, err := parsed.UpdateURR[0].UpdateURR()
	require.NoError(t, err)
	threshold, err := findIE(t, urrChildren, ie.VolumeThreshold).VolumeThreshold()
	require.NoError(t, err)
	require.False(t, threshold.HasTOVOL())
	require.True(t, threshold.HasULVOL())
	require.True(t, threshold.HasDLVOL())
	require.Equal(t, uint64(1_000), threshold.UplinkVolume)
	require.Equal(t, uint64(1_000), threshold.DownlinkVolume)
}
