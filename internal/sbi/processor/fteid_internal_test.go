package processor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

// The UPF of the session, its AN UPF, and the anchor of a chain.
const (
	fteidTestUPF = "198.51.100.8"
	fteidTestPSA = "198.51.100.9"
)

// choosingUPF answers PFCP session requests as UPFs that allocate the F-TEIDs
// (TS 29.244 5.5.3), and keeps the requests in the order that they came. The
// PDRs that ask with a CHOOSE ID, and the Traffic Endpoints that ask, get the
// F-TEID upfTEID(addr, ID) at addr; with chooseNothing, the UPF returns none.
type choosingUPF struct {
	fakeActivePFCPClient
	chooseNothing bool
	mu            sync.Mutex
	requests      []upfRequest
}

type upfRequest struct {
	upf          string
	establishing bool
	createPDR    []*ie.IE
	createFAR    []*ie.IE
	updateFAR    []*ie.IE
	endpoints    []*ie.IE
}

func upfTEID(addr string, id uint8) uint32 {
	if addr == fteidTestUPF {
		return 0x1000 + uint32(id)
	}
	return 0x2000 + uint32(id)
}

func newChoosingUPF() *choosingUPF {
	upf := &choosingUPF{}
	upf.deleteCtx = func(
		_ context.Context, request *message.SessionDeletionRequest, _ *net.UDPAddr, localSEID uint64,
	) (*message.SessionDeletionResponse, error) {
		return message.NewSessionDeletionResponse(0, 0, localSEID, request.Sequence(), 0,
			ie.NewCause(ie.CauseRequestAccepted)), nil
	}
	return upf
}

func (c *choosingUPF) created(addr string, request upfRequest) []*ie.IE {
	c.mu.Lock()
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	if c.chooseNothing {
		return nil
	}
	var created []*ie.IE
	for _, createPDR := range request.createPDR {
		id, err := createPDR.PDRID()
		if err != nil {
			panic(err)
		}
		fteid, err := localFTEID(createPDR)
		if err != nil || !fteid.HasCh() {
			continue
		}
		created = append(created, ie.NewCreatedPDR(ie.NewPDRID(id),
			ie.NewFTEID(0x01, upfTEID(addr, fteid.ChooseID), net.ParseIP(addr).To4(), nil, 0)))
	}
	for _, endpoint := range request.endpoints {
		id, err := endpoint.TrafficEndpointID()
		if err != nil {
			panic(err)
		}
		created = append(created, ie.NewCreatedTrafficEndpoint(ie.NewTrafficEndpointID(id),
			ie.NewFTEID(0x01, upfTEID(addr, id), net.ParseIP(addr).To4(), nil, 0)))
	}
	return created
}

func (c *choosingUPF) SendSessionEstablishmentRequest(
	_ context.Context, request *message.SessionEstablishmentRequest, addr *net.UDPAddr, localSEID uint64,
) (*message.SessionEstablishmentResponse, error) {
	ies := append(c.created(addr.IP.String(), upfRequest{
		upf: addr.IP.String(), establishing: true, createPDR: request.CreatePDR, createFAR: request.CreateFAR,
		endpoints: request.CreateTrafficEndpoint,
	}), ie.NewCause(ie.CauseRequestAccepted), ie.NewFSEID(localSEID+1000, addr.IP.To4(), nil))
	return message.NewSessionEstablishmentResponse(0, 0, localSEID, request.Sequence(), 0, ies...), nil
}

func (c *choosingUPF) SendSessionModificationRequest(
	_ context.Context, request *message.SessionModificationRequest, addr *net.UDPAddr, localSEID uint64,
) (*message.SessionModificationResponse, error) {
	ies := append(c.created(addr.IP.String(), upfRequest{
		upf: addr.IP.String(), createPDR: request.CreatePDR, createFAR: request.CreateFAR, updateFAR: request.UpdateFAR,
		endpoints: request.CreateTrafficEndpoint,
	}), ie.NewCause(ie.CauseRequestAccepted))
	return message.NewSessionModificationResponse(0, 0, localSEID, request.Sequence(), 0, ies...), nil
}

// localFTEID is the Local F-TEID in the PDI of a Create PDR.
func localFTEID(createPDR *ie.IE) (*ie.FTEIDFields, error) {
	children, err := createPDR.CreatePDR()
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		if child.Type == ie.PDI {
			return child.FTEID()
		}
	}
	return nil, ie.ErrIENotFound
}

// newFTEIDTestEnv builds a session of the default rule and a QoS flow of QFI
// 2 on the path gNB - UPF, or gNB - I-UPF - PSA with chain, of UPFs that
// advertise features, with a choosingUPF for its PFCP requests. It is not
// activated yet.
func newFTEIDTestEnv(t *testing.T, features []byte, chain bool) (*modIndTestEnv, *choosingUPF) {
	t.Helper()
	upNode := func(addr string, kinds ...models.Nrf_NFMgmt_UPInterfaceType) *factory.UPNode {
		node := &factory.UPNode{Type: "UPF", NodeID: addr, Addr: addr}
		for _, kind := range kinds {
			node.InterfaceUpfInfoList = append(node.InterfaceUpfInfoList, &factory.InterfaceUpfInfoItem{
				InterfaceType: kind, Endpoints: []string{addr}, NetworkInstances: []string{modIndTestDNN},
			})
		}
		return node
	}
	anchor := func(node *factory.UPNode) *factory.UPNode {
		node.SNssaiInfos = []*factory.SnssaiUpfInfoItem{{
			SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
			DnnUpfInfoList: []*factory.DnnUpfInfoItem{{
				Dnn: modIndTestDNN, Pools: []*factory.UEIPPool{{Cidr: "10.63.0.0/16"}},
			}},
		}}
		return node
	}
	upInfo := factory.UserPlaneInformation{
		UPNodes: map[string]*factory.UPNode{
			"FteidGNB": {Type: "AN"}, "FteidUPF": anchor(upNode(fteidTestUPF, models.Nrf_NFMgmt_UPInterfaceType_N3)),
		},
		Links: []*factory.UPLink{{A: "FteidGNB", B: "FteidUPF"}},
	}
	if chain {
		iupf := upNode(fteidTestUPF, models.Nrf_NFMgmt_UPInterfaceType_N3, models.Nrf_NFMgmt_UPInterfaceType_N9)
		// The I-UPF serves the DNN only at a DNAI that no rule names: it is no
		// anchor, and every path of the session goes on to the PSA.
		iupf.SNssaiInfos = []*factory.SnssaiUpfInfoItem{{
			SNssai:         &models.Snssai{Sst: 1, Sd: "010203"},
			DnnUpfInfoList: []*factory.DnnUpfInfoItem{{Dnn: modIndTestDNN, DnaiList: []string{"transit"}}},
		}}
		upInfo.UPNodes["FteidUPF"] = iupf
		upInfo.UPNodes["FteidPSA"] = anchor(upNode(fteidTestPSA, models.Nrf_NFMgmt_UPInterfaceType_N9))
		upInfo.Links = append(upInfo.Links, &factory.UPLink{A: "FteidUPF", B: "FteidPSA"})
	}
	config := &factory.Config{
		Info: &factory.Info{Version: "1.0.0", Description: "F-TEIDs allocated by the UPF"},
		Configuration: &factory.Configuration{
			Sbi:                  &factory.Sbi{},
			PFCP:                 &factory.PFCP{ExternalAddr: modIndTestCPNodeID, NodeID: modIndTestCPNodeID},
			UserPlaneInformation: upInfo,
		},
	}
	require.NoError(t, smf_context.InitSmfContext(config))
	oldConfig := factory.SmfConfig
	factory.SmfConfig = config
	t.Cleanup(func() { factory.SmfConfig = oldConfig })
	for _, node := range smf_context.GetUserPlaneInformation().UPFs {
		node.UPF.SetUPFunctionFeatures(features)
		node.UPF.EstablishAssociation(context.Background())
		t.Cleanup(node.UPF.CancelAssociation)
	}

	sm := smf_context.NewSMContext("imsi-208930000000371", 7)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000371", PduSessionId: 7, Dnn: modIndTestDNN,
		SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
	}
	sm.SelectedPDUSessionType = 1 // IPv4
	sm.SessionRules[modIndTestSessRule] = &smf_context.SessionRule{
		DefQosQFI: 1,
		Pcf_SMPolCtrl_SessionRule: &models.Pcf_SMPolCtrl_SessionRule{
			SessRuleId:   modIndTestSessRule,
			AuthSessAmbr: &models.Ambr{Uplink: "3000 Kbps", Downlink: "3000 Kbps"},
			AuthDefQos:   &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8}},
		},
	}
	sm.SelectedSessionRuleID = modIndTestSessRule
	sm.DnnConfiguration = models.Udm_SDM_DnnConfiguration{
		SessionAmbr: &models.Ambr{Uplink: "4 Gbps", Downlink: "4 Gbps"},
	}
	require.NoError(t, sm.AllocUeIP())
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
			"FteidPccRule-1": {
				PccRuleId: "FteidPccRule-1", Precedence: 255,
				FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{
					FlowDescription: "permit out ip from any to assigned",
					FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				}},
			},
			"FteidPccRule-2": fteidTestPccRule("FteidPccRule-2", 100, "192.0.2.201"),
		},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{modIndTestQosID: {
			QosId: modIndTestQosID, Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8},
		}},
	}))
	require.NoError(t, sm.SelectDefaultDataPath())
	sm.Tunnel.UpdateANInformation(net.ParseIP("10.100.0.5").To4(), 0x42)

	client := newChoosingUPF()
	e := &modIndTestEnv{p: newEventExposureProcessor(t), sm: sm}
	e.p.SetActivePFCPClient(client)
	return e, client
}

// fteidTestPccRule is a PCC rule of the QoS flow of QFI 2, for the UDP flow
// from server.
func fteidTestPccRule(id string, precedence int32, server string) *models.Pcf_SMPolCtrl_PccRule {
	return &models.Pcf_SMPolCtrl_PccRule{
		PccRuleId: id, Precedence: precedence, RefQosData: []string{modIndTestQosID},
		FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{
			FlowDescription: "permit out 17 from " + server + " 9999 to assigned",
			FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
		}},
	}
}

// activate sends the session's rules to its UPFs, as an establishment does.
func (e *modIndTestEnv) activate(t *testing.T) bool {
	t.Helper()
	done := make(chan bool, 1)
	e.p.ActivateUPFSession(e.sm, func(_ *smf_context.SMContext, success bool) { done <- success })
	select {
	case success := <-done:
		return success
	case <-time.After(5 * time.Second):
		t.Fatal("ActivateUPFSession did not finish")
		return false
	}
}

// requireGTPTunnel checks the TEID and the address of a GTP tunnel.
func requireGTPTunnel(t *testing.T, tnl *ngapie.UPTransportLayerInformation, teid uint32, address string) {
	t.Helper()
	require.NotNil(t, tnl)
	require.Equal(t, teid, modIndTEID(t, tnl))
	gtpTunnel, ok := tnl.Choice.(*ngapie.GTPTunnel)
	require.True(t, ok)
	require.Equal(t, net.ParseIP(address).To4(), net.IP(gtpTunnel.TransportLayerAddress.Value.Bytes))
}

// A session at a UPF that sets FTUP: every uplink PDR of the N3 tunnel asks
// the UPF to choose the F-TEID, by one CHOOSE ID, and the N2 transfers carry
// the F-TEID that it chose (TS 29.244 5.5.1, 5.5.3).
func TestSessionTakesTheFTEIDThatTheUPFChose(t *testing.T) {
	e, upf := newFTEIDTestEnv(t, []byte{0x10, 0x00}, false)
	require.True(t, e.activate(t))
	require.Len(t, upf.requests, 1)
	asked := 0
	for _, createPDR := range upf.requests[0].createPDR {
		fteid, err := localFTEID(createPDR)
		if err != nil {
			continue // the downlink PDRs, from N6
		}
		require.True(t, fteid.HasCh())
		require.True(t, fteid.HasChID())
		require.Equal(t, smf_context.UplinkChooseID, fteid.ChooseID)
		require.Zero(t, fteid.TEID)
		asked++
	}
	require.Equal(t, 2, asked, "the uplink PDRs of both QoS flows")

	chosen := upfTEID(fteidTestUPF, smf_context.UplinkChooseID)
	buf, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(e.sm)
	require.NoError(t, err)
	var setup ngapie.PDUSessionResourceSetupRequestTransfer
	require.NoError(t, ngapie.UnmarshalBinary(buf, &setup))
	found := false
	for _, item := range setup.ProtocolIEs.List {
		if item.ULNGUUPTNLInformation != nil {
			requireGTPTunnel(t, item.ULNGUUPTNLInformation, chosen, fteidTestUPF)
			found = true
		}
		// No TEID of the SMF's for a split: the UPF allocates them.
		require.Nil(t, item.AdditionalULNGUUPTNLInformation)
	}
	require.True(t, found)

	e.sm.UpSecurityFromPathSwitchRequestSameAsLocalStored = true
	buf, err = smf_context.BuildPathSwitchRequestAcknowledgeTransfer(e.sm)
	require.NoError(t, err)
	var ack ngapie.PathSwitchRequestAcknowledgeTransfer
	require.NoError(t, ngapie.UnmarshalBinary(buf, &ack))
	requireGTPTunnel(t, ack.ULNGUUPTNLInformation, chosen, fteidTestUPF)

	// The uplink PDR of a PCC rule added later names the F-TEID that the UPF chose.
	upf.requests = nil
	require.NoError(t, e.sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
			"FteidPccRule-3": fteidTestPccRule("FteidPccRule-3", 90, "192.0.2.202"),
		},
	}))
	require.True(t, e.activate(t))
	require.Len(t, upf.requests, 1)
	require.False(t, upf.requests[0].establishing)
	named := 0
	for _, createPDR := range upf.requests[0].createPDR {
		if fteid, fteidErr := localFTEID(createPDR); fteidErr == nil {
			require.False(t, fteid.HasCh())
			require.Equal(t, chosen, fteid.TEID)
			require.Equal(t, fteidTestUPF, fteid.IPv4Address.String())
			named++
		}
	}
	require.Equal(t, 1, named)

	// A deletion of the session at the UPF forgets what the UPF chose.
	e.sm.PFCPContext[fteidTestUPF].ForgetRemoteSession()
	require.Nil(t, e.sm.ChosenFTEID(e.sm.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode.UPF, smf_context.UplinkChooseID))
}

// An establishment whose response lacks the F-TEID that a PDR asked for fails:
// the session would have no N3 tunnel.
func TestEstablishmentFailsWithoutTheFTEIDThatTheUPFWasAskedToChoose(t *testing.T) {
	e, upf := newFTEIDTestEnv(t, []byte{0x10, 0x00}, false)
	upf.chooseNothing = true
	require.False(t, e.activate(t))
}

// Only the PDRs that a request creates wait for an F-TEID: those of a rule
// removed before the establishment are not in its request.
func TestEstablishmentWithARuleRemovedBefore(t *testing.T) {
	e, upf := newFTEIDTestEnv(t, []byte{0x10, 0x00}, false)
	require.NoError(t, e.sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"FteidPccRule-2": nil},
	}))
	require.True(t, e.activate(t))
	require.Len(t, upf.requests, 1)
}

// The UP Function Features of an Association Setup Response are kept, as those
// of an Association Update are (TS 29.244 6.2.6.2).
func TestAssociationSetupRecordsTheUPFunctionFeatures(t *testing.T) {
	upf := smf_context.NewUPF(&pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("198.51.100.10").To4(),
	}, nil)
	for _, features := range [][]byte{{0x10, 0x00}, nil} {
		ies := []*ie.IE{ie.NewCause(ie.CauseRequestAccepted), ie.NewRecoveryTimeStamp(time.Now())}
		if features != nil {
			ies = append(ies, ie.NewUPFunctionFeatures(features...))
		}
		processor := &Processor{}
		processor.SetActivePFCPClient(&fakeActivePFCPClient{
			setup: func(*net.UDPAddr) (*message.AssociationSetupResponse, error) {
				return message.NewAssociationSetupResponse(1, ies...), nil
			},
		})
		require.NoError(t, processor.setupPfcpAssociation(context.Background(), upf, "[198.51.100.10]"))
		require.Equal(t, features, upf.UPFunctionFeatures())
	}
}

// outerHeaderCreation is the Outer Header Creation of a Create FAR or an
// Update FAR, or nil.
func outerHeaderCreation(t *testing.T, far *ie.IE) *ie.OuterHeaderCreationFields {
	t.Helper()
	children, err := far.CreateFAR()
	if far.Type == ie.UpdateFAR {
		children, err = far.UpdateFAR()
	}
	require.NoError(t, err)
	for _, child := range children {
		if child.Type == ie.ForwardingParameters || child.Type == ie.UpdateForwardingParameters {
			if ohc, ohcErr := child.OuterHeaderCreation(); ohcErr == nil {
				return ohc
			}
		}
	}
	return nil
}

// On the path gNB - I-UPF - PSA of UPFs that allocate the F-TEIDs, each UPF
// needs the F-TEID that the other one chooses: the PSA is established first,
// its downlink buffering; then the I-UPF, its uplink to the PSA's F-TEID; then
// the PSA's downlink is updated to the I-UPF's F-TEID. So with Traffic
// Endpoints.
func TestN9TunnelsAreEstablishedInSequence(t *testing.T) {
	for _, features := range [][]byte{{0x10, 0x00}, {0x10, 0x02}} {
		t.Run(fmt.Sprintf("features %x", features), func(t *testing.T) {
			testN9TunnelsAreEstablishedInSequence(t, features)
		})
	}
}

func testN9TunnelsAreEstablishedInSequence(t *testing.T, features []byte) {
	e, upf := newFTEIDTestEnv(t, features, true)
	require.True(t, e.activate(t))
	require.Len(t, upf.requests, 3)
	first, second, third := upf.requests[0], upf.requests[1], upf.requests[2]

	require.Equal(t, fteidTestPSA, first.upf)
	require.True(t, first.establishing)
	buffering := 0
	for _, far := range first.createFAR {
		if far.HasBUFF() {
			require.Nil(t, outerHeaderCreation(t, far))
			buffering++
		}
	}
	require.Equal(t, 2, buffering, "the downlink of both QoS flows waits for the I-UPF's F-TEID")

	require.Equal(t, fteidTestUPF, second.upf)
	require.True(t, second.establishing)
	toPSA := 0
	for _, far := range second.createFAR {
		if ohc := outerHeaderCreation(t, far); ohc != nil && ohc.IPv4Address.String() == fteidTestPSA {
			require.Equal(t, upfTEID(fteidTestPSA, smf_context.UplinkChooseID), ohc.TEID)
			toPSA++
		}
	}
	require.Equal(t, 2, toPSA)
	if features[1] != 0 {
		// The I-UPF's Traffic Endpoints: the uplink N3 one and the downlink N9 one.
		require.Len(t, second.endpoints, 2)
		require.Len(t, first.endpoints, 1)
	} else {
		for _, createPDR := range second.createPDR {
			fteid, err := localFTEID(createPDR)
			require.NoError(t, err, "every PDR of the I-UPF has a tunnel, N3 or N9")
			require.True(t, fteid.HasCh())
		}
	}

	require.Equal(t, fteidTestPSA, third.upf)
	require.False(t, third.establishing)
	require.Len(t, third.updateFAR, 2)
	for _, far := range third.updateFAR {
		require.True(t, far.HasFORW())
		ohc := outerHeaderCreation(t, far)
		require.NotNil(t, ohc)
		require.Equal(t, upfTEID(fteidTestUPF, smf_context.DownlinkChooseID), ohc.TEID)
		require.Equal(t, fteidTestUPF, ohc.IPv4Address.String())
	}
}

// pdiChildren are the IEs of the PDI of a Create PDR.
func pdiChildren(t *testing.T, createPDR *ie.IE) []*ie.IE {
	t.Helper()
	children, err := createPDR.CreatePDR()
	require.NoError(t, err)
	for _, child := range children {
		if child.Type == ie.PDI {
			pdi, pdiErr := child.PDI()
			require.NoError(t, pdiErr)
			return pdi
		}
	}
	t.Fatal("no PDI")
	return nil
}

// ieTypes are the types of ies.
func ieTypes(ies []*ie.IE) []ie.IEType {
	types := make([]ie.IEType, 0, len(ies))
	for _, i := range ies {
		types = append(types, i.Type)
	}
	return types
}

// At a UPF that supports PDI optimization (PDIU), the uplink PDRs of the N3
// tunnel refer to one Traffic Endpoint, which has the Local F-TEID that the
// UPF chooses, the Network Instance and the UE IP address: the PDI then has
// none of them (TS 29.244 Table 7.5.2.2-2). A PDR that a later request adds
// refers to it too, where it would otherwise name an F-TEID as the CP
// function (5.5.1).
func TestPDRsOfATunnelShareATrafficEndpoint(t *testing.T) {
	e, upf := newFTEIDTestEnv(t, []byte{0x10, 0x02}, false)
	require.True(t, e.activate(t))
	require.Len(t, upf.requests, 1)
	request := upf.requests[0]
	require.Len(t, request.endpoints, 1)
	endpoint, err := request.endpoints[0].CreateTrafficEndpoint()
	require.NoError(t, err)
	require.ElementsMatch(t, []ie.IEType{ie.TrafficEndpointID, ie.FTEID, ie.NetworkInstance, ie.UEIPAddress},
		ieTypes(endpoint))
	fteid, err := request.endpoints[0].FTEID()
	require.NoError(t, err)
	require.True(t, fteid.HasCh())
	require.False(t, fteid.HasChID())
	ueIP, err := request.endpoints[0].UEIPAddress()
	require.NoError(t, err)
	require.Equal(t, e.sm.PDUAddress.To4(), ueIP.IPv4Address.To4())

	uplink := 0
	for _, createPDR := range request.createPDR {
		pdi := pdiChildren(t, createPDR)
		if source, sourceErr := pdi[0].SourceInterface(); sourceErr != nil || source != ie.SrcInterfaceAccess {
			continue
		}
		require.Equal(t, []ie.IEType{ie.SourceInterface, ie.TrafficEndpointID}, ieTypes(pdi)[:2])
		require.NotContains(t, ieTypes(pdi), ie.FTEID)
		require.NotContains(t, ieTypes(pdi), ie.NetworkInstance)
		require.NotContains(t, ieTypes(pdi), ie.UEIPAddress)
		id, idErr := pdi[1].TrafficEndpointID()
		require.NoError(t, idErr)
		require.Equal(t, smf_context.UplinkChooseID, id)
		uplink++
	}
	require.Equal(t, 2, uplink)
	buf, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(e.sm)
	require.NoError(t, err)
	var setup ngapie.PDUSessionResourceSetupRequestTransfer
	require.NoError(t, ngapie.UnmarshalBinary(buf, &setup))
	for _, item := range setup.ProtocolIEs.List {
		if item.ULNGUUPTNLInformation != nil {
			requireGTPTunnel(t, item.ULNGUUPTNLInformation, upfTEID(fteidTestUPF, smf_context.UplinkChooseID),
				fteidTestUPF)
		}
	}

	upf.requests = nil
	require.NoError(t, e.sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
			"FteidPccRule-3": fteidTestPccRule("FteidPccRule-3", 90, "192.0.2.202"),
		},
	}))
	require.True(t, e.activate(t))
	require.Len(t, upf.requests, 1)
	require.Empty(t, upf.requests[0].endpoints)
	added := 0
	for _, createPDR := range upf.requests[0].createPDR {
		pdi := pdiChildren(t, createPDR)
		if source, sourceErr := pdi[0].SourceInterface(); sourceErr == nil && source == ie.SrcInterfaceAccess {
			id, idErr := pdi[1].TrafficEndpointID()
			require.NoError(t, idErr)
			require.Equal(t, smf_context.UplinkChooseID, id)
			require.NotContains(t, ieTypes(pdi), ie.FTEID)
			added++
		}
	}
	require.Equal(t, 1, added)
}

// A modification that refers a new PDR to a Traffic Endpoint that the UPF has
// not created creates it, and keeps the F-TEID that the UPF chose for it.
func TestModificationCreatesATrafficEndpoint(t *testing.T) {
	e, upf := newFTEIDTestEnv(t, []byte{0x10, 0x02}, false)
	require.True(t, e.activate(t))
	sessionContext := e.sm.PFCPContext[fteidTestUPF]
	sessionContext.ForgetRemoteSession()
	require.False(t, sessionContext.HasTrafficEndpoint(smf_context.UplinkChooseID))
	sessionContext.RemoteSEID = 0x99
	node := e.sm.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode
	pdr := node.UpLinkTunnel.PDR
	pdr.State = smf_context.RULE_INITIAL
	_, err := e.p.sendSessionModificationRequest(e.sm, &PFCPState{upf: node.UPF, pdrList: []*smf_context.PDR{pdr}})
	require.NoError(t, err)
	require.Len(t, upf.requests[len(upf.requests)-1].endpoints, 1)
	require.True(t, sessionContext.HasTrafficEndpoint(smf_context.UplinkChooseID))
	require.Equal(t, upfTEID(fteidTestUPF, smf_context.UplinkChooseID),
		e.sm.ChosenFTEID(node.UPF, smf_context.UplinkChooseID).Teid)
}

// An establishment whose response lacks the F-TEID of a Traffic Endpoint fails.
func TestEstablishmentFailsWithoutTheFTEIDOfATrafficEndpoint(t *testing.T) {
	e, upf := newFTEIDTestEnv(t, []byte{0x10, 0x02}, false)
	upf.chooseNothing = true
	require.False(t, e.activate(t))
}

// sendHandoverRequestAcknowledge sends the target's acknowledge of an N2
// handover that needs indirect forwarding: the NG-RAN's DL tunnel, and its
// DL forwarding tunnel for QFI 1.
func (e *modIndTestEnv) sendHandoverRequestAcknowledge(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	e.sm.DLForwardingType = smf_context.IndirectForwarding // no direct path, in the Handover Required
	buf, err := ngapie.MarshalBinary(&ngapie.HandoverRequestAcknowledgeTransfer{
		DLNGUUPTNLInformation:        modIndGTPTunnel("10.100.0.7", 0x51),
		DLForwardingUPTNLInformation: modIndGTPTunnel("10.100.0.7", 0x52),
		QosFlowSetupResponseList: &ngapie.QosFlowListWithDataForwarding{List: []ngapie.QosFlowItemWithDataForwarding{{
			QosFlowIdentifier: &ngapie.QosFlowIdentifier{Value: 1},
			DataForwardingAccepted: &ngapie.DataForwardingAccepted{
				Value: ngapie.DataForwardingAcceptedPresentDataForwardingAccepted,
			},
		}}},
	})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	e.p.HandlePDUSessionSMContextUpdate(c, models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			HoState:      models.Smf_PDUSess_HoState_PREPARED,
			N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_HANDOVER_REQ_ACK,
			N2SmInfo:     &models.RefToBinaryData{ContentId: "HoAck"},
		},
		BinaryDataN2SmInformation: &multipart.RelatedContent{ContentID: "HoAck", Content: buf},
	}, e.sm.Ref)
	return rec
}

// The indirect forwarding tunnel of an N2 handover, at a UPF that allocates
// the F-TEIDs: the SMF asks the UPF to allocate its F-TEID, one of its own
// (no CHOOSE ID), and the Handover Command carries the F-TEID that the UPF
// chose (TS 23.502 4.9.1.3.2 steps 11d-11f).
func TestIndirectForwardingTunnelOfAnFTUPUPF(t *testing.T) {
	e, upf := newFTEIDTestEnv(t, []byte{0x10, 0x00}, false)
	require.True(t, e.activate(t))
	// Twice: the second forwarding tunnel asks for an F-TEID of its own too.
	for range 2 {
		e.sm.SetState(smf_context.Active)
		upf.requests = nil
		rec := e.sendHandoverRequestAcknowledge(t)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Len(t, upf.requests, 1)
		require.Len(t, upf.requests[0].createPDR, 1)
		fteid, err := localFTEID(upf.requests[0].createPDR[0])
		require.NoError(t, err)
		require.True(t, fteid.HasCh())
		require.False(t, fteid.HasChID())

		var rsp models.UpdateSmContextResponse200
		require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
		require.Equal(t, models.Smf_PDUSess_N2SmInfoType_HANDOVER_CMD, rsp.JsonData.N2SmInfoType)
		var command ngapie.HandoverCommandTransfer
		require.NoError(t, ngapie.UnmarshalBinary(rsp.BinaryDataN2SmInformation.Content, &command))
		requireGTPTunnel(t, command.DLForwardingUPTNLInformation, upfTEID(fteidTestUPF, 0), fteidTestUPF)
	}
	// The forwarding tunnel's F-TEID is not the session's uplink.
	node := e.sm.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode
	require.Equal(t, upfTEID(fteidTestUPF, smf_context.UplinkChooseID),
		e.sm.ChosenFTEID(node.UPF, smf_context.UplinkChooseID).Teid)
}

// A split of a session at a UPF that allocates the F-TEIDs, and its end. The
// split tunnel's PDRs ask the UPF to choose its F-TEID, by a CHOOSE ID of
// their own or a Traffic Endpoint, and the confirm of the split, built after
// the UPF's answer, carries it (TS 23.502 4.14.1 steps 3-5) in the Additional
// NG-U UP TNL Information, with the session's own in the UL NG-U UP TNL
// Information (TS 38.413 9.3.4.7). The confirm of the end carries the
// session's.
func TestSplitAtAnFTUPUPF(t *testing.T) {
	for _, features := range [][]byte{{0x10, 0x00}, {0x10, 0x02}} {
		t.Run(fmt.Sprintf("features %x", features), func(t *testing.T) {
			testSplitAtAnFTUPUPF(t, features)
		})
	}
}

func testSplitAtAnFTUPUPF(t *testing.T, features []byte) {
	e, upf := newFTEIDTestEnv(t, features, false)
	require.True(t, e.activate(t))
	e.sm.SetState(smf_context.Active)
	upf.requests = nil
	session := upfTEID(fteidTestUPF, smf_context.UplinkChooseID)
	split := upfTEID(fteidTestUPF, smf_context.SplitUplinkChooseID)
	readConfirm := func(rec *httptest.ResponseRecorder) *ngapie.PDUSessionResourceModifyConfirmTransfer {
		t.Helper()
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var rsp models.UpdateSmContextResponse200
		require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
		require.Equal(t, models.Smf_PDUSess_N2SmInfoType_PDU_RES_MOD_CFM, rsp.JsonData.N2SmInfoType)
		var confirm ngapie.PDUSessionResourceModifyConfirmTransfer
		require.NoError(t, ngapie.UnmarshalBinary(rsp.BinaryDataN2SmInformation.Content, &confirm))
		return &confirm
	}

	confirm := readConfirm(e.sendModifyIndication(t, modIndSplitTransfer()))
	requireGTPTunnel(t, confirm.ULNGUUPTNLInformation, session, fteidTestUPF)
	require.NotNil(t, confirm.AdditionalNGUUPTNLInformation)
	require.Len(t, confirm.AdditionalNGUUPTNLInformation.List, 1)
	pair := confirm.AdditionalNGUUPTNLInformation.List[0]
	requireGTPTunnel(t, pair.ULNGUUPTNLInformation, split, fteidTestUPF)
	require.Equal(t, modIndGTPTunnel("10.100.0.9", 0x77), pair.DLNGUUPTNLInformation)

	require.Len(t, upf.requests, 1)
	asked := 0
	if features[1] != 0 {
		asked = len(upf.requests[0].endpoints)
	}
	for _, createPDR := range upf.requests[0].createPDR {
		if fteid, err := localFTEID(createPDR); err == nil && fteid.HasCh() {
			require.Equal(t, smf_context.SplitUplinkChooseID, fteid.ChooseID)
			asked++
		}
	}
	require.Equal(t, 1, asked, "the split tunnel's uplink asks the UPF to choose its F-TEID")

	// A path switch of the split session gives the split tunnel's F-TEID too.
	e.sm.UpSecurityFromPathSwitchRequestSameAsLocalStored = true
	buf, err := smf_context.BuildPathSwitchRequestAcknowledgeTransfer(e.sm)
	require.NoError(t, err)
	var ack ngapie.PathSwitchRequestAcknowledgeTransfer
	require.NoError(t, ngapie.UnmarshalBinary(buf, &ack))
	require.NotNil(t, ack.IEExtensions)
	requireGTPTunnel(t, ack.IEExtensions.List[0].AdditionalNGUUPTNLInformation.List[0].ULNGUUPTNLInformation,
		split, fteidTestUPF)

	confirm = readConfirm(e.sendModifyIndication(t, &ngapie.PDUSessionResourceModifyIndicationTransfer{
		DLQosFlowPerTNLInformation: modIndQosFlowPerTNL("10.100.0.5", 0x42, 2),
	}))
	requireGTPTunnel(t, confirm.ULNGUUPTNLInformation, session, fteidTestUPF)
	require.False(t, e.sm.NrdcIndicator)
	// The UPF freed the split tunnel's F-TEID with its PDRs; a Traffic Endpoint keeps it.
	kept := e.sm.ChosenFTEID(e.sm.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode.UPF, smf_context.SplitUplinkChooseID)
	require.Equal(t, features[1] != 0, kept != nil)
}

// A split that the SMF cannot confirm, here of a session with no QoS flow to
// move, is refused before any PFCP request, also at a UPF that chooses the
// F-TEIDs, where the confirm is built after the UPF's answer.
func TestSplitWithoutQosFlowToMoveAtAnFTUPUPFIsRejected(t *testing.T) {
	e, upf := newFTEIDTestEnv(t, []byte{0x10, 0x00}, false)
	require.NoError(t, e.sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"FteidPccRule-2": nil},
	}))
	require.True(t, e.activate(t))
	e.sm.SetState(smf_context.Active)
	upf.requests = nil
	e.requireModifyIndicationUnsuccessful(t, e.sendModifyIndication(t, modIndSplitTransfer()))
	require.Empty(t, upf.requests)
}
