package processor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	nasie "github.com/free5gc/nas/ie"
	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

const (
	ruleReportTestUPF      = "192.0.2.71"
	ruleReportTestDNN      = "internet"
	ruleReportTestPolicyID = "smpolicy-1241"
	ruleReportTestPccRule  = "PccRuleId-2"
	ruleReportTestQosID    = "QosId-2"
)

// ruleReportPCF is a PCF stub recording the SM policy updates it receives.
type ruleReportPCF struct {
	mu      sync.Mutex
	updates []models.Pcf_SMPolCtrl_SmPolicyUpdateContextData
}

func (f *ruleReportPCF) received() []models.Pcf_SMPolCtrl_SmPolicyUpdateContextData {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]models.Pcf_SMPolCtrl_SmPolicyUpdateContextData(nil), f.updates...)
}

// newRuleReportTestSession builds an established PDU session with a PCC rule bound to QFI 2,
// whose PCF is the returned stub.
func newRuleReportTestSession(t *testing.T) (*Processor, *smf_context.SMContext, *ruleReportPCF) {
	t.Helper()
	config := &factory.Config{
		Info: &factory.Info{Description: "PCC rule error report"},
		Configuration: &factory.Configuration{
			Sbi:   &factory.Sbi{},
			PFCP:  &factory.PFCP{ExternalAddr: "127.0.0.1", NodeID: "127.0.0.1"},
			T3591: &factory.TimerValue{Enable: false},
			UserPlaneInformation: factory.UserPlaneInformation{
				UPNodes: map[string]*factory.UPNode{
					"GNodeB": {Type: "AN"},
					"UPF1": {
						Type: "UPF", NodeID: ruleReportTestUPF, Addr: ruleReportTestUPF,
						SNssaiInfos: []*factory.SnssaiUpfInfoItem{{
							SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
							DnnUpfInfoList: []*factory.DnnUpfInfoItem{{
								Dnn: ruleReportTestDNN, Pools: []*factory.UEIPPool{{Cidr: "10.62.0.0/16"}},
							}},
						}},
						InterfaceUpfInfoList: []*factory.InterfaceUpfInfoItem{{
							InterfaceType: "N3", Endpoints: []string{ruleReportTestUPF},
							NetworkInstances: []string{ruleReportTestDNN},
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

	pcf := &ruleReportPCF{}
	// PCF stub reachable with HTTP/2 over cleartext (prior knowledge), as the
	// openapi clients use for http URIs.
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/sm-policies/"+ruleReportTestPolicyID+"/update") {
			http.NotFound(w, r)
			return
		}
		var update models.Pcf_SMPolCtrl_SmPolicyUpdateContextData
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			t.Errorf("decode SmPolicyUpdateContextData: %v", err)
		}
		pcf.mu.Lock()
		pcf.updates = append(pcf.updates, update)
		pcf.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("{}")); err != nil {
			t.Errorf("write SmPolicyDecision: %v", err)
		}
	}))
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	t.Cleanup(server.Close)

	sm := smf_context.NewSMContext("imsi-208930000001241", 6)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000001241", PduSessionId: 6, Dnn: ruleReportTestDNN,
		SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
	}
	sm.SelectedPDUSessionType = nasie.PDUSessType_IPv4
	sm.SessionRules["SessRuleId-1"] = &smf_context.SessionRule{
		DefQosQFI: 1,
		Pcf_SMPolCtrl_SessionRule: &models.Pcf_SMPolCtrl_SessionRule{
			SessRuleId:   "SessRuleId-1",
			AuthSessAmbr: &models.Ambr{Uplink: "1000 Kbps", Downlink: "1000 Kbps"},
			AuthDefQos:   &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8}},
		},
	}
	sm.SelectedSessionRuleID = "SessRuleId-1"
	sm.DnnConfiguration = models.Udm_SDM_DnnConfiguration{
		SessionAmbr: &models.Ambr{Uplink: "1 Gbps", Downlink: "1 Gbps"},
	}
	sm.SMPolicyID = ruleReportTestPolicyID
	pcfAddr := server.Listener.Addr().(*net.TCPAddr)
	sm.SelectedPCFProfile = models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{{
		ServiceName:     models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL,
		NfServiceStatus: models.Nrf_NFMgmt_NFServiceStatus_REGISTERED,
		Scheme:          models.UriScheme_HTTP,
		IpEndPoints: []models.Nrf_NFMgmt_IpEndPoint{{
			Ipv4Address: pcfAddr.IP.String(),
			Transport:   models.Nrf_NFMgmt_TransportProtocol_TCP,
			Port:        int32(pcfAddr.Port),
		}},
	}}}
	require.NoError(t, sm.AllocUeIP())
	require.NoError(t, sm.SelectDefaultDataPath())
	require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{ruleReportTestPccRule: {
			PccRuleId: ruleReportTestPccRule, Precedence: 100,
			RefQosData: []string{ruleReportTestQosID},
			FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{
				FlowDescription: "permit out 17 from 192.0.2.201/32 9999 to assigned",
				FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
			}},
		}},
		QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{ruleReportTestQosID: {
			QosId: ruleReportTestQosID, Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8},
		}},
	}))
	require.Equal(t, uint8(2), sm.PCCRules[ruleReportTestPccRule].QFI)
	sm.Tunnel.UpdateANInformation(net.ParseIP("10.100.0.5").To4(), 0x42)
	sm.PFCPContext[ruleReportTestUPF].RemoteSEID = 0x1241
	sm.SetState(smf_context.Active)

	p := newEventExposureProcessor(t)
	p.SetActivePFCPClient(&ueQosModPFCPClient{cause: 1})
	return p, sm, pcf
}

// An SM policy update whose PCC rules cannot be installed is rejected with an ErrorReport
// reporting them (TS 29.512 clause 4.2.3.2): the PCF removes them and can tell the AF.
func TestPolicyUpdateNotifyReportsUninstalledPccRules(t *testing.T) {
	p, sm, _ := newRuleReportTestSession(t)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	p.HandleSMPolicyUpdateNotify(c, models.Pcf_SMPolCtrl_SmPolicyNotification{
		SmPolicyDecision: &models.Pcf_SMPolCtrl_SmPolicyDecision{
			PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
				"PccRuleId-3": {
					PccRuleId: "PccRuleId-3", Precedence: 101,
					RefQosData: []string{"QosId-unknown"},
					FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{
						FlowDescription: "permit out 17 from 192.0.2.203/32 9999 to assigned",
					}},
				},
			},
		},
	}, sm.Ref)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var report models.Pcf_SMPolCtrl_ErrorReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	require.NotNil(t, report.Error)
	require.Equal(t, "PCC_RULE_EVENT", report.Error.Cause)
	require.Equal(t, []models.Pcf_SMPolCtrl_RuleReport{{
		PccRuleIds:  []string{"PccRuleId-3"},
		RuleStatus:  models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
		FailureCode: models.Pcf_SMPolCtrl_FailureCode_UNKNOWN_REF_ID,
	}}, report.RuleReports)
	require.NotContains(t, sm.PCCRules, "PccRuleId-3")
	require.Contains(t, sm.PCCRules, ruleReportTestPccRule)
}

// The PCC rules bound to a QoS flow the AN failed to add or modify are reported to the PCF
// as no longer enforced (TS 29.512 clause 4.2.4.15).
func TestQosFlowFailedToModifyIsReportedToPCF(t *testing.T) {
	p, sm, pcf := newRuleReportTestSession(t)

	transfer, err := ngapie.MarshalBinary(&ngapie.PDUSessionResourceModifyResponseTransfer{
		QosFlowFailedToAddOrModifyList: &ngapie.QosFlowListWithCause{List: []ngapie.QosFlowWithCauseItem{{
			QosFlowIdentifier: &ngapie.QosFlowIdentifier{Value: 2},
			Cause: &ngapie.Cause{Choice: &ngapie.CauseRadioNetwork{
				Value: ngapie.CauseRadioNetworkPresentUnspecified,
			}},
		}}},
	})
	require.NoError(t, err)
	rec := ueQosModUpdateSMContext(t, p, sm, models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_PDU_RES_MOD_RSP,
			N2SmInfo:     &models.RefToBinaryData{ContentId: "N2"},
		},
		BinaryDataN2SmInformation: &multipart.RelatedContent{ContentID: "N2", Content: transfer},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.Eventually(t, func() bool { return len(pcf.received()) == 1 }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, []models.Pcf_SMPolCtrl_RuleReport{{
		PccRuleIds:  []string{ruleReportTestPccRule},
		RuleStatus:  models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
		FailureCode: models.Pcf_SMPolCtrl_FailureCode_RES_ALLO_FAIL,
	}}, pcf.received()[0].RuleReports)

	// Reported once.
	rec = ueQosModUpdateSMContext(t, p, sm, models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_PDU_RES_MOD_RSP,
			N2SmInfo:     &models.RefToBinaryData{ContentId: "N2"},
		},
		BinaryDataN2SmInformation: &multipart.RelatedContent{ContentID: "N2", Content: mustModifyResponse(t)},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	time.Sleep(100 * time.Millisecond)
	require.Len(t, pcf.received(), 1)
}

// mustModifyResponse is a PDU Session Resource Modify Response Transfer with no failed QoS flow.
func mustModifyResponse(t *testing.T) []byte {
	t.Helper()
	transfer, err := ngapie.MarshalBinary(&ngapie.PDUSessionResourceModifyResponseTransfer{
		QosFlowAddOrModifyResponseList: &ngapie.QosFlowAddOrModifyResponseList{
			List: []ngapie.QosFlowAddOrModifyResponseItem{{QosFlowIdentifier: &ngapie.QosFlowIdentifier{Value: 2}}},
		},
	})
	require.NoError(t, err)
	return transfer
}
