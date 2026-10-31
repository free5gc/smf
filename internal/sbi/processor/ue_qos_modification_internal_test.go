package processor

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	pfcpMessage "github.com/wmnsk/go-pfcp/message"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/ngap/aper"
	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

const (
	ueQosModTestUPF    = "192.0.2.59"
	ueQosModTestDNN    = "internet"
	ueQosModTestQosID  = "QosId-2"
	ueQosModTestChgID  = "ChgId-2"
	ueQosModTestCPNode = "127.0.0.1"
	ueQosModTestRule   = "SessRuleId-1"
)

// ueQosModPFCPClient records every PFCP Session Modification Request and
// answers it with cause. onRequest, when set, runs as each request arrives.
type ueQosModPFCPClient struct {
	fakeActivePFCPClient
	cause     uint8
	onRequest func()

	mu       sync.Mutex
	requests []*pfcpMessage.SessionModificationRequest
}

func (f *ueQosModPFCPClient) SendSessionModificationRequest(
	_ context.Context, request *pfcpMessage.SessionModificationRequest, _ *net.UDPAddr, localSEID uint64,
) (*pfcpMessage.SessionModificationResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.mu.Unlock()
	if f.onRequest != nil {
		f.onRequest()
	}
	return pfcpMessage.NewSessionModificationResponse(0, 0, localSEID, request.Sequence(), 0,
		ie.NewCause(f.cause)), nil
}

func (f *ueQosModPFCPClient) sent() []*pfcpMessage.SessionModificationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*pfcpMessage.SessionModificationRequest(nil), f.requests...)
}

// newUEQosModTestSession builds an established PDU session (default data path
// installed in the UPF, AN tunnel known) whose PCF answers the UE-requested
// modification with a new PCC rule: QFI 2, two bidirectional packet filters
// and flow-level offline charging.
func newUEQosModTestSession(t *testing.T, pfcpClient ActivePFCPClient) (*Processor, *smf_context.SMContext) {
	t.Helper()
	config := &factory.Config{
		Info: &factory.Info{Description: "UE-requested QoS flow N4 regression"},
		Configuration: &factory.Configuration{
			Sbi:   &factory.Sbi{},
			PFCP:  &factory.PFCP{ExternalAddr: ueQosModTestCPNode, NodeID: ueQosModTestCPNode},
			T3591: &factory.TimerValue{Enable: false},
			UserPlaneInformation: factory.UserPlaneInformation{
				UPNodes: map[string]*factory.UPNode{
					"GNodeB": {Type: "AN"},
					"UPF1": {
						Type: "UPF", NodeID: ueQosModTestUPF, Addr: ueQosModTestUPF,
						SNssaiInfos: []*factory.SnssaiUpfInfoItem{{
							SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
							DnnUpfInfoList: []*factory.DnnUpfInfoItem{{
								Dnn: ueQosModTestDNN, Pools: []*factory.UEIPPool{{Cidr: "10.61.0.0/16"}},
							}},
						}},
						InterfaceUpfInfoList: []*factory.InterfaceUpfInfoItem{{
							InterfaceType: "N3", Endpoints: []string{ueQosModTestUPF},
							NetworkInstances: []string{ueQosModTestDNN},
						}},
					},
				},
				Links: []*factory.UPLink{{A: "GNodeB", B: "UPF1"}},
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

	decision := models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"PccRuleId-2": {
			PccRuleId: "PccRuleId-2", Precedence: 100,
			RefQosData: []string{ueQosModTestQosID}, RefChgData: []string{ueQosModTestChgID},
			FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{
				{
					FlowDescription: "permit out 17 from 192.0.2.201/32 9999 to assigned",
					FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				},
				{
					FlowDescription: "permit out 17 from 192.0.2.202/32 9999 to assigned",
					FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
				},
			},
		}},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{ueQosModTestQosID: {
			QosId: ueQosModTestQosID, Var5qi: 9,
			Arp: &models.Arp{
				PriorityLevel: 8,
				PreemptCap:    models.PreemptionCapability_NOT_PREEMPT,
				PreemptVuln:   models.PreemptionVulnerability_NOT_PREEMPTABLE,
			},
		}},
		ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{ueQosModTestChgID: {
			ChgId: ueQosModTestChgID, RatingGroup: 2, Offline: true,
		}},
	}
	// PCF stub reachable with HTTP/2 over cleartext (prior knowledge), as the
	// openapi clients use for http URIs.
	pcf := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/sm-policies/smpolicy-259/update") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(decision); err != nil {
			t.Errorf("encode SmPolicyDecision: %v", err)
		}
	}))
	pcf.Config.Protocols = new(http.Protocols)
	pcf.Config.Protocols.SetUnencryptedHTTP2(true)
	pcf.Start()
	t.Cleanup(pcf.Close)

	sm := smf_context.NewSMContext("imsi-208930000000259", 5)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000259", PduSessionId: 5, Dnn: ueQosModTestDNN,
		SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
	}
	sm.SelectedPDUSessionType = nasie.PDUSessType_IPv4
	sm.SessionRules[ueQosModTestRule] = &smf_context.SessionRule{
		DefQosQFI: 1,
		Pcf_SMPolCtrl_SessionRule: &models.Pcf_SMPolCtrl_SessionRule{
			SessRuleId:   ueQosModTestRule,
			AuthSessAmbr: &models.Ambr{Uplink: "1000 Kbps", Downlink: "1000 Kbps"},
			AuthDefQos:   &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8}},
		},
	}
	sm.SelectedSessionRuleID = ueQosModTestRule
	sm.DnnConfiguration = models.Udm_SDM_DnnConfiguration{
		SessionAmbr: &models.Ambr{Uplink: "1 Gbps", Downlink: "1 Gbps"},
	}
	sm.SMPolicyID = "smpolicy-259"
	sm.SelectedPCFProfile = models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{{
		ServiceName: models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, ApiPrefix: pcf.URL,
	}}}
	require.NoError(t, sm.AllocUeIP())
	require.NoError(t, sm.SelectDefaultDataPath())
	sm.Tunnel.UpdateANInformation(net.ParseIP("10.100.0.5").To4(), 0x42)
	sm.PFCPContext[ueQosModTestUPF].RemoteSEID = 0x259
	sm.SetState(smf_context.Active)

	// Install the default data path as PDU session establishment did.
	installed := &ueQosModPFCPClient{cause: ie.CauseRequestAccepted}
	p := newEventExposureProcessor(t)
	p.SetActivePFCPClient(installed)
	p.ActivateUPFSession(sm, nil)
	require.Len(t, installed.sent(), 1)

	p.SetActivePFCPClient(pfcpClient)
	return p, sm
}

func ueQosModTestRequest() *message.PDUSessModReq {
	return &message.PDUSessModReq{
		PDUSessId: 5, PTI: 2,
		ReqQosRules: &nasie.QosRules{Rules: []nasie.QosRule{{
			OpCode: nasie.OpCode_CreateNewQosRule, Precedence: 100, QFI: 2,
			PktFilterList: []nasie.PacketFilter{{
				Dir: nasie.PFD_BiDir, Id: 1,
				Contents: nasie.PacketFilterContents{
					HavePIorNH: true, PIorNH: 17, RemoteAddr: "192.0.2.201/32", RemotePortRange: "9999",
				},
			}},
		}}},
		ReqQosFlowDescs: &nasie.QosFlowDescs{Descs: []nasie.QosFlowDesc{{QFI: 2, OpCode: 1, FiveQI: 9}}},
	}
}

// createdQFIs returns the QFIs of the Create QER IEs of a PFCP request.
func createdQFIs(t *testing.T, request *pfcpMessage.SessionModificationRequest) []uint8 {
	t.Helper()
	qfis := []uint8{}
	for _, qer := range request.CreateQER {
		qfi, err := qer.QFI()
		require.NoError(t, err)
		qfis = append(qfis, qfi)
	}
	return qfis
}

// updateSMContextWithUEModification sends the UE's PDU SESSION MODIFICATION
// REQUEST through Nsmf_PDUSession_UpdateSMContext, as the AMF forwards it.
// It returns the HTTP status, the N1 SM message and the QFIs the N2 PDU
// Session Resource Modify Request Transfer asks NG-RAN to add or modify.
func updateSMContextWithUEModification(
	t *testing.T, p *Processor, sm *smf_context.SMContext, rec *httptest.ResponseRecorder,
) (int, message.GSMMessage, []int64) {
	t.Helper()
	// The 5GSM payload of the OxiRusher scenario: PSI 5, PTI 2, a new QoS rule
	// (QFI 2, one UDP/9999 packet filter) and QoS flow description (QFI 2, 5QI 9).
	n1, err := hex.DecodeString("2e0502c97a000d05000a21c105301150270f02027900" +
		"1a0220450101090203010064030301006404030100640503010064")
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	p.HandlePDUSessionSMContextUpdate(c, models.UpdateSmContextRequestBody{
		JsonData:              &models.Smf_PDUSess_SmContextUpdateData{N1SmMsg: &models.RefToBinaryData{ContentId: "N1"}},
		BinaryDataN1SmMessage: &multipart.RelatedContent{ContentID: "N1", Content: n1},
	}, sm.Ref)

	var rsp models.UpdateSmContextResponse200
	require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
	require.NotNil(t, rsp.BinaryDataN1SmMessage)
	gsm, err := message.ParseGSM(rsp.BinaryDataN1SmMessage.Content)
	require.NoError(t, err)
	require.Equal(t, models.Smf_PDUSess_N2SmInfoType_PDU_RES_MOD_REQ, rsp.JsonData.N2SmInfoType)
	require.NotNil(t, rsp.BinaryDataN2SmInformation)
	var transfer ngapie.PDUSessionResourceModifyRequestTransfer
	require.NoError(t, ngapie.UnmarshalBinary(rsp.BinaryDataN2SmInformation.Content, &transfer))
	qfis := []int64{}
	for _, item := range transfer.ProtocolIEs.List {
		if item.QosFlowAddOrModifyRequestList == nil {
			continue
		}
		for _, flow := range item.QosFlowAddOrModifyRequestList.List {
			qfis = append(qfis, flow.QosFlowIdentifier.Value)
		}
	}
	return rec.Code, gsm, qfis
}

// The QoS flow authorized for a UE-requested PDU session modification must be
// installed in the UPF (TS 23.502 4.3.3.2 step 2a): one N4 Session
// Modification creating the PDRs of both packet filters in each direction,
// their FARs, the QFI 2 QER and the flow-level charging URR.
func TestUERequestedQosFlowIsInstalledInUPF(t *testing.T) {
	client := &ueQosModPFCPClient{cause: ie.CauseRequestAccepted}
	p, sm := newUEQosModTestSession(t, client)

	rsp, err := p.HandlePDUSessionModificationRequest(sm, ueQosModTestRequest())
	require.NoError(t, err)
	require.NotNil(t, rsp.AuthoQosRules)

	requests := client.sent()
	require.Len(t, requests, 1, "no N4 Session Modification for the new QoS flow")
	request := requests[0]
	require.Len(t, request.CreatePDR, 4, "UL+DL PDR for each of the two packet filters")
	require.Len(t, request.CreateFAR, 2)
	require.Len(t, request.CreateURR, 1, "flow-level charging URR")
	require.Contains(t, createdQFIs(t, request), uint8(2))
	require.Empty(t, request.UpdatePDR)
	require.Empty(t, request.RemovePDR)
}

// Through Nsmf_PDUSession_UpdateSMContext: the N4 rules of QFI 2 reach the UPF
// before the response that carries the PDU Session Modification Command and
// the N2 PDU Session Resource Modify Request Transfer is written.
func TestUERequestedQosFlowInstalledBeforeModificationCommand(t *testing.T) {
	rec := httptest.NewRecorder()
	responseWrittenAtN4 := -1
	client := &ueQosModPFCPClient{cause: ie.CauseRequestAccepted}
	client.onRequest = func() { responseWrittenAtN4 = rec.Body.Len() }
	p, sm := newUEQosModTestSession(t, client)

	status, gsm, n2QFIs := updateSMContextWithUEModification(t, p, sm, rec)
	require.Equal(t, http.StatusOK, status)
	cmd, ok := gsm.(*message.PDUSessModCmd)
	require.True(t, ok, "N1 SM message is %T, want a PDU Session Modification Command", gsm)
	require.NotNil(t, cmd.AuthoQosRules)
	require.Equal(t, []int64{2}, n2QFIs)

	requests := client.sent()
	require.Len(t, requests, 1, "no N4 Session Modification for the new QoS flow")
	require.Contains(t, createdQFIs(t, requests[0]), uint8(2))
	require.Zero(t, responseWrittenAtN4, "the N4 update must precede the N1/N2 response")
}

// A UPF refusal does not reject the UE's request, as on the PCF-initiated
// path: the PCF decision is already applied to the SM context, so the UE and
// NG-RAN get the QoS flow the SMF now holds. A Reject would leave the UE
// without a QoS rule the SMF keeps, and the next Command for that rule would
// modify a rule the UE does not have (TS 24.501 6.3.2.4 a) 8).
func TestUERequestedQosFlowUPFRefusalKeepsUEAndSMFInStep(t *testing.T) {
	client := &ueQosModPFCPClient{cause: ie.CauseRequestRejected}
	p, sm := newUEQosModTestSession(t, client)

	status, gsm, n2QFIs := updateSMContextWithUEModification(t, p, sm, httptest.NewRecorder())
	require.Len(t, client.sent(), 1, "the N4 Session Modification was not attempted")
	require.Equal(t, http.StatusOK, status)
	cmd, ok := gsm.(*message.PDUSessModCmd)
	require.True(t, ok, "N1 SM message is %T, want a PDU Session Modification Command", gsm)
	require.NotNil(t, cmd.AuthoQosRules)
	require.Equal(t, []int64{2}, n2QFIs)
	require.Contains(t, sm.PCCRules, "PccRuleId-2")
	require.Contains(t, sm.AdditionalQosFlows, uint8(2))
}

// ueQosModUpdateSMContext sends one Nsmf_PDUSession_UpdateSMContext request
// without N1 SM information, as the AMF does for UP connection changes.
func ueQosModUpdateSMContext(
	t *testing.T, p *Processor, sm *smf_context.SMContext, body models.UpdateSmContextRequestBody,
) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	p.HandlePDUSessionSMContextUpdate(c, body, sm.Ref)
	return rec
}

// ueQosModAccessFAR returns the ID and IE of the one Create or Update FAR that
// forwards towards the access side (the AN tunnel).
func ueQosModAccessFAR(t *testing.T, fars []*ie.IE) (uint32, *ie.IE) {
	t.Helper()
	var found []*ie.IE
	for _, far := range fars {
		children, err := far.ValueAsGrouped()
		require.NoError(t, err)
		for _, child := range children {
			if child.Type != ie.ForwardingParameters && child.Type != ie.UpdateForwardingParameters {
				continue
			}
			if iface, ifaceErr := child.DestinationInterface(); ifaceErr == nil && iface == ie.DstInterfaceAccess {
				found = append(found, far)
			}
		}
	}
	require.Len(t, found, 1, "want exactly one FAR towards the access side")
	farID, err := found[0].FARID()
	require.NoError(t, err)
	return farID, found[0]
}

// While the user plane is deactivated, the downlink FAR of the new QoS flow is
// created to buffer and notify, like the session's existing DL FARs (TS 23.501
// 5.8.3.2), not to forward to the released AN tunnel. The activation that
// follows the Service Request forwards it to the new AN tunnel.
func TestUERequestedQosFlowBuffersWhileUserPlaneDeactivated(t *testing.T) {
	client := &ueQosModPFCPClient{cause: ie.CauseRequestAccepted}
	p, sm := newUEQosModTestSession(t, client)

	// UE to CM-IDLE: the AMF deactivates the user plane, the existing DL FAR
	// is set to buffer/notify.
	rec := ueQosModUpdateSMContext(t, p, sm, models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{UpCnxState: models.Smf_PDUSess_UpCnxState_DEACTIVATED},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, client.sent(), 1)
	_, deactivated := ueQosModAccessFAR(t, client.sent()[0].UpdateFAR)
	require.True(t, deactivated.HasBUFF())

	status, _, n2QFIs := updateSMContextWithUEModification(t, p, sm, httptest.NewRecorder())
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []int64{2}, n2QFIs)
	requests := client.sent()
	require.Len(t, requests, 2, "no N4 Session Modification for the new QoS flow")
	require.Contains(t, createdQFIs(t, requests[1]), uint8(2))
	newDLFARID, created := ueQosModAccessFAR(t, requests[1].CreateFAR)
	require.True(t, created.HasBUFF(), "new DL FAR must buffer while the user plane is deactivated")
	require.True(t, created.HasNOCP(), "new DL FAR must notify the SMF of the first buffered packet")
	require.False(t, created.HasFORW(), "new DL FAR forwards to the released AN tunnel")
	createdChildren, err := created.CreateFAR()
	require.NoError(t, err)
	for _, child := range createdChildren {
		if child.Type == ie.ApplyAction {
			require.NoError(t, child.ValidateApplyAction(), "TS 29.244 8.2.26 flag combination")
		}
	}

	// Service Request: the user plane is activated on a new AN tunnel.
	rec = ueQosModUpdateSMContext(t, p, sm, models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{UpCnxState: models.Smf_PDUSess_UpCnxState_ACTIVATING},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	transfer, err := ngapie.MarshalBinary(&ngapie.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation: &ngapie.QosFlowPerTNLInformation{
			UPTransportLayerInformation: &ngapie.UPTransportLayerInformation{Choice: &ngapie.GTPTunnel{
				TransportLayerAddress: &ngapie.TransportLayerAddress{
					Value: aper.BitString{Bytes: net.ParseIP("10.100.0.6").To4(), BitLength: 32},
				},
				GTPTEID: &ngapie.GTPTEID{Value: []byte{0, 0, 0, 0x43}},
			}},
			AssociatedQosFlowList: &ngapie.AssociatedQosFlowList{List: []ngapie.AssociatedQosFlowItem{
				{QosFlowIdentifier: &ngapie.QosFlowIdentifier{Value: 1}},
				{QosFlowIdentifier: &ngapie.QosFlowIdentifier{Value: 2}},
			}},
		},
	})
	require.NoError(t, err)
	rec = ueQosModUpdateSMContext(t, p, sm, models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_PDU_RES_SETUP_RSP,
			N2SmInfo:     &models.RefToBinaryData{ContentId: "N2"},
		},
		BinaryDataN2SmInformation: &multipart.RelatedContent{ContentID: "N2", Content: transfer},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	requests = client.sent()
	require.Len(t, requests, 3)
	var activated *ie.IE
	for _, far := range requests[2].UpdateFAR {
		if id, idErr := far.FARID(); idErr == nil && id == newDLFARID {
			activated = far
		}
	}
	require.NotNil(t, activated, "activation does not update the new DL FAR")
	require.True(t, activated.HasFORW())
	require.False(t, activated.HasBUFF())
	params, err := activated.ValueAsGrouped()
	require.NoError(t, err)
	var header *ie.OuterHeaderCreationFields
	for _, child := range params {
		if child.Type == ie.UpdateForwardingParameters {
			header, err = child.OuterHeaderCreation()
			require.NoError(t, err)
		}
	}
	require.NotNil(t, header)
	require.Equal(t, uint32(0x43), header.TEID)
	require.Equal(t, net.ParseIP("10.100.0.6").To4(), header.IPv4Address.To4())
}
