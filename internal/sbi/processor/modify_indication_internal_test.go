package processor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/ngap/aper"
	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

const (
	modIndTestCPNodeID = "127.0.0.1"
	modIndTestDNN      = "internet"
	modIndTestSessRule = "SessRuleId-1"
	modIndTestQosID    = "QosId-2"
)

type modIndTestEnv struct {
	p    *Processor
	sm   *smf_context.SMContext
	upf  *smf_context.UPF
	pfcp *tiPFCPClient
}

// modIndPFCPClient captures PFCP Session Modification Requests and answers
// each with cause, e.g. a UPF that refuses the modification.
type modIndPFCPClient struct {
	fakeActivePFCPClient
	cause    uint8
	requests chan *message.SessionModificationRequest
}

func (f *modIndPFCPClient) SendSessionModificationRequest(
	_ context.Context, request *message.SessionModificationRequest, _ *net.UDPAddr, localSEID uint64,
) (*message.SessionModificationResponse, error) {
	f.requests <- request
	return message.NewSessionModificationResponse(0, 0, localSEID, request.Sequence(), 0, ie.NewCause(f.cause)), nil
}

// refuseN4 makes the UPF reject every following PFCP Session Modification.
func (e *modIndTestEnv) refuseN4() *modIndPFCPClient {
	refusing := &modIndPFCPClient{
		cause: ie.CauseRequestRejected, requests: make(chan *message.SessionModificationRequest, 4),
	}
	e.p.SetActivePFCPClient(refusing)
	return refusing
}

// newModIndTestEnv builds an Active PDU session with UP connection on one
// associated UPF; PFCP Session Modification Requests are captured.
func newModIndTestEnv(t *testing.T) *modIndTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info: &factory.Info{Description: "Modify Indication regression"},
		Configuration: &factory.Configuration{
			Sbi:  &factory.Sbi{},
			PFCP: &factory.PFCP{ExternalAddr: modIndTestCPNodeID, NodeID: modIndTestCPNodeID},
		},
	}))
	upf := smf_context.NewUPF(&pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("192.0.2.57").To4(),
	}, nil)
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)

	sm := smf_context.NewSMContext("imsi-208930000000257", 5)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000257", PduSessionId: 5,
	}
	sm.PFCPContext["192.0.2.57"] = &smf_context.PFCPSessionContext{LocalSEID: 1, RemoteSEID: 2}
	sm.LocalULTeid, sm.LocalULTeidForSplitPDUSession = 0x101, 0x102
	sm.Tunnel.DataPathPool[1] = modIndTestDataPath(upf, 1, 255, 1, smf_context.RULE_CREATE)
	sm.Tunnel.DataPathPool[1].IsDefaultPath = true
	sm.Tunnel.ANInformation.IPAddress, sm.Tunnel.ANInformation.TEID = net.ParseIP("10.100.0.5").To4(), 0x42
	sm.UpCnxState = models.Smf_PDUSess_UpCnxState_ACTIVATED
	sm.SetState(smf_context.Active)

	client := &tiPFCPClient{requests: make(chan *message.SessionModificationRequest, 4)}
	p := newEventExposureProcessor(t)
	p.SetActivePFCPClient(client)
	return &modIndTestEnv{p: p, sm: sm, upf: upf, pfcp: client}
}

// modIndTestDataPath returns an activated one-node data path with UL/DL PDRs
// (IDs firstID, firstID+1) of the given precedence and QFI.
func modIndTestDataPath(
	upf *smf_context.UPF, firstID uint16, precedence uint32, qfi uint8, state smf_context.RuleState,
) *smf_context.DataPath {
	qer := &smf_context.QER{QERID: uint32(firstID), QFI: &pfcptype.QFI{QFI: qfi}, State: state}
	newPDR := func(id uint16) *smf_context.PDR {
		return &smf_context.PDR{
			PDRID: id, Precedence: precedence, State: state, QER: []*smf_context.QER{qer},
			FAR: &smf_context.FAR{FARID: uint32(id), State: state, ForwardingParameters: &smf_context.ForwardingParameters{}},
		}
	}
	node := smf_context.NewDataPathNode()
	node.UPF = upf
	node.UpLinkTunnel.PDR, node.DownLinkTunnel.PDR = newPDR(firstID), newPDR(firstID+1)
	dataPath := smf_context.NewDataPath()
	dataPath.FirstDPNode, dataPath.Activated = node, true
	return dataPath
}

func modIndGTPTunnel(ip string, teid byte) *ngapie.UPTransportLayerInformation {
	return &ngapie.UPTransportLayerInformation{Choice: &ngapie.GTPTunnel{
		TransportLayerAddress: &ngapie.TransportLayerAddress{
			Value: aper.BitString{Bytes: net.ParseIP(ip).To4(), BitLength: 32},
		},
		GTPTEID: &ngapie.GTPTEID{Value: []byte{0, 0, 0, teid}},
	}}
}

func modIndQosFlowPerTNL(ip string, teid byte, qfi int64) *ngapie.QosFlowPerTNLInformation {
	return &ngapie.QosFlowPerTNLInformation{
		UPTransportLayerInformation: modIndGTPTunnel(ip, teid),
		AssociatedQosFlowList: &ngapie.AssociatedQosFlowList{List: []ngapie.AssociatedQosFlowItem{
			{QosFlowIdentifier: &ngapie.QosFlowIdentifier{Value: qfi}},
		}},
	}
}

func (e *modIndTestEnv) sendModifyIndication(
	t *testing.T, transfer *ngapie.PDUSessionResourceModifyIndicationTransfer,
) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := ngapie.MarshalBinary(transfer)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	require.NotPanics(t, func() {
		e.p.HandlePDUSessionSMContextUpdate(c, models.UpdateSmContextRequestBody{
			JsonData: &models.Smf_PDUSess_SmContextUpdateData{
				N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_PDU_RES_MOD_IND,
				N2SmInfo:     &models.RefToBinaryData{ContentId: "ModIndN2SmInfo"},
			},
			BinaryDataN2SmInformation: &multipart.RelatedContent{ContentID: "ModIndN2SmInfo", Content: buf},
		}, e.sm.Ref)
	})
	return rec
}

// requireModifyIndicationUnsuccessful checks the TS 29.502 5.2.2.3.7 step 2b
// answer: an error carrying a PDU Session Resource Modify Indication
// Unsuccessful Transfer, with the session back to Active.
func (e *modIndTestEnv) requireModifyIndicationUnsuccessful(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	var rsp models.UpdateSmContextResponse403
	require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
	require.NotNil(t, rsp.JsonData)
	require.Equal(t, "N2_SM_ERROR", rsp.JsonData.Error.Cause)
	require.Equal(t, models.Smf_PDUSess_N2SmInfoType_PDU_RES_MOD_IND_FAIL, rsp.JsonData.N2SmInfoType)
	require.NotNil(t, rsp.BinaryDataN2SmInformation)
	var unsuccessful ngapie.PDUSessionResourceModifyIndicationUnsuccessfulTransfer
	require.NoError(t, ngapie.UnmarshalBinary(rsp.BinaryDataN2SmInformation.Content, &unsuccessful))
	require.NotNil(t, unsuccessful.Cause)
	require.Equal(t, smf_context.Active, e.sm.State())
}

// requireModifyIndicationRejected checks the Unsuccessful Transfer answer to an
// indication the SMF does not accept, and that no N4 modification was sent.
func (e *modIndTestEnv) requireModifyIndicationRejected(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e.requireModifyIndicationUnsuccessful(t, rec)
	require.False(t, e.sm.NrdcIndicator, "the session must not be considered split")
	require.Empty(t, e.sm.DCTunnel.DataPathPool)
	require.Nil(t, e.sm.DCTunnel.ANInformation.IPAddress)
	require.Empty(t, e.pfcp.requests, "no PFCP Session Modification for a rejected indication")
	require.Equal(t, smf_context.RULE_CREATE, e.sm.Tunnel.DataPathPool[1].FirstDPNode.DownLinkTunnel.PDR.State)
}

// A Modify Indication with only the mandatory DL QoS Flow per TNL Information
// (no split, 1->1) is not supported: it must be refused with an Unsuccessful
// Transfer instead of being taken as a dual-connectivity activation.
func TestModifyIndicationWithoutSplitIsRejected(t *testing.T) {
	e := newModIndTestEnv(t)
	rec := e.sendModifyIndication(t, &ngapie.PDUSessionResourceModifyIndicationTransfer{
		DLQosFlowPerTNLInformation: modIndQosFlowPerTNL("10.100.0.6", 0x43, 1),
	})
	e.requireModifyIndicationRejected(t, rec)
}

// A split (1->2) the SMF cannot confirm, here because the session has no
// non-default QoS flow to move, must be refused and leave the session unsplit.
func TestModifyIndicationSplitWithoutQosFlowToMoveIsRejected(t *testing.T) {
	e := newModIndTestEnv(t)
	rec := e.sendModifyIndication(t, &ngapie.PDUSessionResourceModifyIndicationTransfer{
		DLQosFlowPerTNLInformation: modIndQosFlowPerTNL("10.100.0.5", 0x42, 1),
		AdditionalDLQosFlowPerTNLInformation: &ngapie.QosFlowPerTNLInformationList{
			List: []ngapie.QosFlowPerTNLInformationItem{
				{QosFlowPerTNLInformation: modIndQosFlowPerTNL("10.100.0.9", 0x77, 1)},
			},
		},
	})
	e.requireModifyIndicationRejected(t, rec)
}

// splitSession puts QFI 2 of the session on a secondary DL tunnel (split
// PDU session) and returns the indication that releases the split (2->1).
func (e *modIndTestEnv) splitSession() *ngapie.PDUSessionResourceModifyIndicationTransfer {
	e.sm.Tunnel.DataPathPool[2] = modIndTestDataPath(e.upf, 3, 100, 2, smf_context.RULE_REMOVE)
	e.sm.DCTunnel.DataPathPool[1] = modIndTestDataPath(e.upf, 5, 100, 2, smf_context.RULE_CREATE)
	e.sm.DCTunnel.ANInformation.IPAddress, e.sm.DCTunnel.ANInformation.TEID = net.ParseIP("10.100.0.9").To4(), 0x77
	e.sm.NrdcIndicator = true
	return &ngapie.PDUSessionResourceModifyIndicationTransfer{
		DLQosFlowPerTNLInformation: modIndQosFlowPerTNL("10.100.0.5", 0x42, 2),
	}
}

// Control: releasing an established split (2->1) is still confirmed and moves
// the non-default QoS flow back to the master tunnel with one N4 modification.
func TestModifyIndicationReleasingSplitIsConfirmed(t *testing.T) {
	e := newModIndTestEnv(t)
	rec := e.sendModifyIndication(t, e.splitSession())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var rsp models.UpdateSmContextResponse200
	require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
	require.Equal(t, models.Smf_PDUSess_N2SmInfoType_PDU_RES_MOD_CFM, rsp.JsonData.N2SmInfoType)
	var confirm ngapie.PDUSessionResourceModifyConfirmTransfer
	require.NoError(t, ngapie.UnmarshalBinary(rsp.BinaryDataN2SmInformation.Content, &confirm))
	require.Len(t, confirm.QosFlowModifyConfirmList.List, 1)
	require.Equal(t, int64(2), confirm.QosFlowModifyConfirmList.List[0].QosFlowIdentifier.Value)

	require.Len(t, e.pfcp.requests, 1)
	request := <-e.pfcp.requests
	require.Len(t, request.CreatePDR, 1, "master DL PDR of QFI 2 re-created")
	require.Len(t, request.RemovePDR, 2, "DC tunnel UL/DL PDRs removed")
	require.False(t, e.sm.NrdcIndicator)
	require.Equal(t, smf_context.Active, e.sm.State())
}

// newModIndSplitTestEnv builds an Active session from real PCC rules, the
// default rule and a non-default QoS flow (QFI 2), on a user-plane topology,
// installed in the UPF as establishment did.
func newModIndSplitTestEnv(t *testing.T) *modIndTestEnv {
	t.Helper()
	const upfAddr = "192.0.2.58"
	config := &factory.Config{
		Info: &factory.Info{Description: "Modify Indication split regression"},
		Configuration: &factory.Configuration{
			Sbi:  &factory.Sbi{},
			PFCP: &factory.PFCP{ExternalAddr: modIndTestCPNodeID, NodeID: modIndTestCPNodeID},
			UserPlaneInformation: factory.UserPlaneInformation{
				UPNodes: map[string]*factory.UPNode{
					"SplitGNB": {Type: "AN"},
					"SplitUPF": {
						Type: "UPF", NodeID: upfAddr, Addr: upfAddr,
						SNssaiInfos: []*factory.SnssaiUpfInfoItem{{
							SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
							DnnUpfInfoList: []*factory.DnnUpfInfoItem{{
								Dnn: modIndTestDNN, Pools: []*factory.UEIPPool{{Cidr: "10.62.0.0/16"}},
							}},
						}},
						InterfaceUpfInfoList: []*factory.InterfaceUpfInfoItem{{
							InterfaceType: "N3", Endpoints: []string{upfAddr}, NetworkInstances: []string{modIndTestDNN},
						}},
					},
				},
				Links: []*factory.UPLink{{A: "SplitGNB", B: "SplitUPF"}},
			},
		},
	}
	require.NoError(t, smf_context.InitSmfContext(config))
	oldConfig := factory.SmfConfig
	factory.SmfConfig = config
	t.Cleanup(func() { factory.SmfConfig = oldConfig })
	for _, node := range smf_context.GetUserPlaneInformation().UPFs {
		node.UPF.EstablishAssociation(context.Background())
		t.Cleanup(node.UPF.CancelAssociation)
	}

	sm := smf_context.NewSMContext("imsi-208930000000257", 6)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000257", PduSessionId: 6, Dnn: modIndTestDNN,
		SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
	}
	sm.SelectedPDUSessionType = 1 // IPv4
	sm.SessionRules[modIndTestSessRule] = &smf_context.SessionRule{
		DefQosQFI: 1,
		Pcf_SMPolCtrl_SessionRule: &models.Pcf_SMPolCtrl_SessionRule{
			SessRuleId:   modIndTestSessRule,
			AuthSessAmbr: &models.Ambr{Uplink: "2000 Kbps", Downlink: "2000 Kbps"},
			AuthDefQos:   &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8}},
		},
	}
	sm.SelectedSessionRuleID = modIndTestSessRule
	sm.DnnConfiguration = models.Udm_SDM_DnnConfiguration{
		SessionAmbr: &models.Ambr{Uplink: "2 Gbps", Downlink: "2 Gbps"},
	}
	require.NoError(t, sm.AllocUeIP())
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
			"SplitPccRule-1": {
				PccRuleId: "SplitPccRule-1", Precedence: 255,
				FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{
					FlowDescription: "permit out ip from any to assigned",
					FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				}},
			},
			"SplitPccRule-2": {
				PccRuleId: "SplitPccRule-2", Precedence: 100, RefQosData: []string{modIndTestQosID},
				FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{
					FlowDescription: "permit out 17 from 192.0.2.201 9999 to assigned",
					FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				}},
			},
		},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{modIndTestQosID: {
			QosId: modIndTestQosID, Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8},
		}},
	}))
	require.NoError(t, sm.SelectDefaultDataPath())
	sm.Tunnel.UpdateANInformation(net.ParseIP("10.100.0.5").To4(), 0x42)
	sm.PFCPContext[upfAddr].RemoteSEID = 0x258
	sm.SetState(smf_context.Active)

	client := &tiPFCPClient{requests: make(chan *message.SessionModificationRequest, 4)}
	e := &modIndTestEnv{p: newEventExposureProcessor(t), sm: sm, pfcp: client}
	e.p.SetActivePFCPClient(client)
	e.p.ActivateUPFSession(sm, nil) // install the session as establishment did
	require.Len(t, client.requests, 1)
	<-client.requests
	return e
}

// modIndSplitTransfer asks to move QFI 2 to a secondary DL tunnel (1->2).
func modIndSplitTransfer() *ngapie.PDUSessionResourceModifyIndicationTransfer {
	return &ngapie.PDUSessionResourceModifyIndicationTransfer{
		DLQosFlowPerTNLInformation: modIndQosFlowPerTNL("10.100.0.5", 0x42, 1),
		AdditionalDLQosFlowPerTNLInformation: &ngapie.QosFlowPerTNLInformationList{
			List: []ngapie.QosFlowPerTNLInformationItem{
				{QosFlowPerTNLInformation: modIndQosFlowPerTNL("10.100.0.9", 0x77, 2)},
			},
		},
	}
}

// requireSplitConfirmed checks the answer to an accepted split: a Confirm
// Transfer listing QFI 2 and one N4 modification installing the DC tunnel rules.
func (e *modIndTestEnv) requireSplitConfirmed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var rsp models.UpdateSmContextResponse200
	require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
	require.Equal(t, models.Smf_PDUSess_N2SmInfoType_PDU_RES_MOD_CFM, rsp.JsonData.N2SmInfoType)
	var confirm ngapie.PDUSessionResourceModifyConfirmTransfer
	require.NoError(t, ngapie.UnmarshalBinary(rsp.BinaryDataN2SmInformation.Content, &confirm))
	require.Len(t, confirm.QosFlowModifyConfirmList.List, 1)
	require.Equal(t, int64(2), confirm.QosFlowModifyConfirmList.List[0].QosFlowIdentifier.Value)

	require.Len(t, e.pfcp.requests, 1)
	request := <-e.pfcp.requests
	require.NotEmpty(t, request.CreatePDR, "DC tunnel rules installed")
	require.True(t, e.sm.NrdcIndicator)
	require.Equal(t, net.ParseIP("10.100.0.9").To4(), e.sm.DCTunnel.ANInformation.IPAddress.To4())
	require.Equal(t, smf_context.Active, e.sm.State())
}

// Control: a split (1->2) of a session whose PCC rules include the default
// rule and a non-default QoS flow is confirmed: the Confirm Transfer lists the
// moved QFI and one N4 modification installs the DC tunnel rules.
func TestModifyIndicationSplitIsConfirmed(t *testing.T) {
	e := newModIndSplitTestEnv(t)
	e.requireSplitConfirmed(t, e.sendModifyIndication(t, modIndSplitTransfer()))
}

// A split the SMF accepted but the UPF refused is answered with the
// Unsuccessful Transfer and leaves the session unsplit: the UPF keeps its
// existing rules (TS 29.244 6.3.3.3). The gNB's next split indication is then
// a new 1->2 activation, not the release of a split that was never installed.
func TestModifyIndicationSplitRefusedByUPFIsRejected(t *testing.T) {
	e := newModIndSplitTestEnv(t)
	refusing := e.refuseN4()

	e.requireModifyIndicationUnsuccessful(t, e.sendModifyIndication(t, modIndSplitTransfer()))
	require.Len(t, refusing.requests, 1, "the N4 modification was not attempted")
	require.False(t, e.sm.NrdcIndicator, "the session must not be considered split")
	require.Empty(t, e.sm.DCTunnel.DataPathPool)
	require.Nil(t, e.sm.DCTunnel.ANInformation.IPAddress)

	e.p.SetActivePFCPClient(e.pfcp)
	e.requireSplitConfirmed(t, e.sendModifyIndication(t, modIndSplitTransfer()))
}

// A release of the split (2->1) that the UPF refused is answered with the
// Unsuccessful Transfer and keeps the split: the UPF still has the secondary
// tunnel's rules (TS 29.244 6.3.3.3).
func TestModifyIndicationReleasingSplitRefusedByUPFKeepsSplit(t *testing.T) {
	e := newModIndTestEnv(t)
	release := e.splitSession()
	split := e.sm.DCTunnel
	refusing := e.refuseN4()

	e.requireModifyIndicationUnsuccessful(t, e.sendModifyIndication(t, release))
	require.Len(t, refusing.requests, 1, "the N4 modification was not attempted")
	require.True(t, e.sm.NrdcIndicator, "the session must stay split")
	require.Same(t, split, e.sm.DCTunnel)
	require.Equal(t, net.ParseIP("10.100.0.9").To4(), e.sm.DCTunnel.ANInformation.IPAddress.To4())
}
