package processor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
)

const policyBatchFlow = "permit out ip from any to assigned"

type policyBatchApp struct {
	ProcessorSmf
	client *consumer.Consumer
}

func (a *policyBatchApp) Consumer() *consumer.Consumer { return a.client }

func newPolicyBatchContext(t *testing.T) *smf_context.SMContext {
	t.Helper()
	config := &factory.Config{Info: &factory.Info{Version: "1.0.0"}, Configuration: &factory.Configuration{
		Sbi:  &factory.Sbi{RegisterIPv4: "127.0.0.1", BindingIPv4: "127.0.0.1"},
		PFCP: &factory.PFCP{ExternalAddr: "127.0.0.1", NodeID: "127.0.0.1"},
	}}
	require.NoError(t, smf_context.InitSmfContext(config))
	oldConfig := factory.SmfConfig
	factory.SmfConfig = config
	t.Cleanup(func() { factory.SmfConfig = oldConfig })
	upi, err := smf_context.NewUserPlaneInformation(&factory.UserPlaneInformation{
		UPNodes: map[string]*factory.UPNode{
			"AN": {Type: "AN"},
			"UPF": {
				Type: "UPF", NodeID: "127.0.0.119",
				SNssaiInfos: []*factory.SnssaiUpfInfoItem{{
					SNssai:         &models.Snssai{Sst: 1, Sd: "010203"},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{{Dnn: "internet", Pools: []*factory.UEIPPool{{Cidr: "10.99.0.0/24"}}}},
				}},
				InterfaceUpfInfoList: []*factory.InterfaceUpfInfoItem{{
					InterfaceType: "N3", Endpoints: []string{"127.0.0.119"}, NetworkInstances: []string{"internet"},
				}},
			},
		}, Links: []*factory.UPLink{{A: "AN", B: "UPF"}},
	})
	require.NoError(t, err)
	smf_context.GetSelf().UserPlaneInformation = upi
	for _, node := range upi.UPFs {
		node.UPF.EstablishAssociation(context.Background())
		t.Cleanup(node.UPF.CancelAssociation)
	}
	sm := smf_context.NewSMContext("imsi-208930000000198", 10)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000198", Dnn: "internet", SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
	}
	sm.SelectedPDUSessionType = 1
	sm.DnnConfiguration = models.Udm_SDM_DnnConfiguration{SessionAmbr: &models.Ambr{Uplink: "1 Gbps", Downlink: "1 Gbps"}}
	sm.SessionRules["session"] = smf_context.NewSessionRule(&models.Pcf_SMPolCtrl_SessionRule{
		SessRuleId: "session", AuthSessAmbr: &models.Ambr{Uplink: "1 Mbps", Downlink: "1 Mbps"},
		AuthDefQos: &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8}},
	})
	sm.SelectedSessionRuleID = "session"
	sm.UrrReportThreshold = 3000
	require.NoError(t, sm.AllocUeIP())
	sm.PFCPContext["127.0.0.119"] = &smf_context.PFCPSessionContext{
		LocalSEID: 123, RemoteSEID: 456, PDRs: map[uint16]*smf_context.PDR{},
	}
	sm.SetState(smf_context.Active)
	return sm
}

func policyBatchDecision() *models.Pcf_SMPolCtrl_SmPolicyDecision {
	return &models.Pcf_SMPolCtrl_SmPolicyDecision{
		PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
			"good": {
				PccRuleId: "good", Precedence: 255,
				FlowInfos:  []models.Pcf_SMPolCtrl_FlowInformation{{FlowDescription: policyBatchFlow}},
				RefQosData: []string{"qos"}, RefTcData: []string{"traffic"}, RefChgData: []string{"charging"},
			},
			"bad": {PccRuleId: "bad", Precedence: 10, RefQosData: []string{"qos"}, RefTcData: []string{"traffic"}},
		},
		QosDecs:       map[string]*models.Pcf_SMPolCtrl_QosData{"qos": {QosId: "qos", DefQosFlowIndication: true}},
		TraffContDecs: map[string]*models.Pcf_SMPolCtrl_TrafficControlData{"traffic": {TcId: "traffic"}},
		ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{"charging": {
			ChgId: "charging", RatingGroup: 42, Offline: true,
			MeteringMethod: models.Pcf_SMPolCtrl_MeteringMethod_VOLUME,
			ReportingLevel: models.Pcf_SMPolCtrl_ReportingLevel_RAT_GR_LEVEL,
		}},
	}
}

// TS 29.512 V17.11.0 clauses 4.2.3.2/4.2.3.16, table 5.5.2.2-2:
// decode the actual HTTP body and verify valid charging rules reach PFCP.
func TestPolicyBatchNotification(t *testing.T) {
	for _, kind := range []string{"partial", "all invalid", "success"} {
		t.Run(kind, func(t *testing.T) {
			sm := newPolicyBatchContext(t)
			oldSession := sm.SelectedSessionRule()
			decision := policyBatchDecision()
			if kind == "all invalid" {
				delete(decision.PccRules, "good")
			}
			if kind == "success" {
				delete(decision.PccRules, "bad")
			}
			decision.SessRules = map[string]*models.Pcf_SMPolCtrl_SessionRule{"session": {
				SessRuleId: "session", AuthSessAmbr: &models.Ambr{Uplink: "2 Mbps", Downlink: "2 Mbps"},
				AuthDefQos: oldSession.AuthDefQos,
			}}
			client := &tiPFCPClient{requests: make(chan *message.SessionModificationRequest, 1)}
			p := &Processor{}
			p.SetActivePFCPClient(client)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			p.HandleSMPolicyUpdateNotify(c, models.Pcf_SMPolCtrl_SmPolicyNotification{SmPolicyDecision: decision}, sm.Ref)
			c.Writer.WriteHeaderNow()
			require.Equal(t, smf_context.Active, sm.State())
			if kind == "all invalid" {
				require.Equal(t, http.StatusBadRequest, w.Code)
				var report models.Pcf_SMPolCtrl_ErrorReport
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &report))
				require.Equal(t, "PCC_RULE_EVENT", report.Error.Cause)
				require.EqualValues(t, http.StatusBadRequest, report.Error.Status)
				assertPolicyBatchReport(t, report.RuleReports)
				require.Empty(t, sm.PCCRules)
				require.Empty(t, sm.QosDatas)
				require.Empty(t, sm.Tunnel.DataPathPool)
				require.Empty(t, client.requests)
				require.Same(t, oldSession, sm.SelectedSessionRule(), "400 rejects session-rule changes too")
				require.Equal(t, "1 Mbps", oldSession.AuthSessAmbr.Uplink)
				return
			}
			if kind == "partial" {
				require.Equal(t, http.StatusOK, w.Code)
				var reports []models.Pcf_SMPolCtrl_PartialSuccessReport
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &reports))
				require.Len(t, reports, 1)
				require.Equal(t, models.Pcf_SMPolCtrl_FailureCause_PCC_RULE_EVENT, reports[0].FailureCause)
				assertPolicyBatchReport(t, reports[0].RuleReports)
			} else {
				require.Equal(t, http.StatusNoContent, w.Code)
				require.Empty(t, w.Body.Bytes())
			}
			require.Equal(t, "2 Mbps", sm.SelectedSessionRule().AuthSessAmbr.Uplink)
			require.Contains(t, sm.PCCRules, "good")
			require.NotContains(t, sm.PCCRules, "bad")
			select {
			case request := <-client.requests:
				require.NotEmpty(t, request.CreatePDR)
				require.NotEmpty(t, request.CreateURR)
			case <-time.After(time.Second):
				t.Fatal("valid charging rule did not reach the PFCP mock")
			}
			require.NotEmpty(t, sm.PCCRules["good"].Datapath.GetChargingUrr(sm))
		})
	}
}

func assertPolicyBatchReport(t *testing.T, reports []models.Pcf_SMPolCtrl_RuleReport) {
	t.Helper()
	require.Equal(t, []models.Pcf_SMPolCtrl_RuleReport{{
		PccRuleIds: []string{"bad"}, FailureCode: models.Pcf_SMPolCtrl_FailureCode_MISS_FLOW_INFO,
		RuleStatus: models.Pcf_SMPolCtrl_RuleStatus_INACTIVE,
	}}, reports)
}

func TestPolicyBatchPCFResponseReport(t *testing.T) {
	for _, corrective := range []bool{false, true} {
		t.Run(map[bool]string{false: "reported", true: "corrective response enforced"}[corrective], func(t *testing.T) {
			sm := newPolicyBatchContext(t)
			received := make(chan models.Pcf_SMPolCtrl_SmPolicyUpdateContextData, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/npcf-smpolicycontrol/v1/sm-policies/policy/update", r.URL.Path)
				var update models.Pcf_SMPolCtrl_SmPolicyUpdateContextData
				require.NoError(t, json.NewDecoder(r.Body).Decode(&update))
				received <- update
				w.Header().Set("Content-Type", "application/json")
				if corrective {
					response := policyBatchDecision()
					fixed := *response.PccRules["good"]
					fixed.PccRuleId, fixed.Precedence = "bad", 20
					fixed.FlowInfos = []models.Pcf_SMPolCtrl_FlowInformation{{
						FlowDescription: "permit out ip from 10.99.0.0/24 to assigned",
					}}
					response.PccRules = map[string]*models.Pcf_SMPolCtrl_PccRule{"bad": &fixed}
					require.NoError(t, json.NewEncoder(w).Encode(response))
				} else {
					_, err := w.Write([]byte("{}"))
					require.NoError(t, err)
				}
			}))
			server.Config.Protocols = new(http.Protocols)
			server.Config.Protocols.SetUnencryptedHTTP2(true)
			server.Start()
			t.Cleanup(server.Close)
			sm.SMPolicyID = "policy"
			sm.SelectedPCFProfile = models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{{
				ServiceName:     models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL,
				ApiPrefix:       server.URL,
				NfServiceStatus: models.Nrf_NFMgmt_NFServiceStatus_REGISTERED,
				Scheme:          models.UriScheme_HTTP,
			}}}
			c, err := consumer.NewConsumer(nil)
			require.NoError(t, err)
			p := &Processor{ProcessorSmf: &policyBatchApp{client: c}}
			decision := policyBatchDecision()
			require.NoError(t, p.applyPccRulesAndReport(sm, decision))
			select {
			case report := <-received:
				assertPolicyBatchReport(t, report.RuleReports)
			case <-time.After(time.Second):
				t.Fatal("missing PCF rule report")
			}
			require.Contains(t, sm.PCCRules, "good")
			require.NotEmpty(t, sm.PCCRules["good"].Datapath.GetChargingUrr(sm))
			if corrective {
				require.Contains(t, sm.PCCRules, "bad")
				require.NotEmpty(t, decision.PccRules["bad"].FlowInfos)
			} else {
				require.NotContains(t, sm.PCCRules, "bad")
			}
		})
	}
}

func TestPolicyBatchReportDeliveryFailureKeepsValidRule(t *testing.T) {
	sm := newPolicyBatchContext(t)
	sm.SelectedPCFProfile = models.Nrf_NFDisc_NFProfile{}
	c, err := consumer.NewConsumer(nil)
	require.NoError(t, err)
	p := &Processor{ProcessorSmf: &policyBatchApp{client: c}}
	require.Error(t, p.applyPccRulesAndReport(sm, policyBatchDecision()))
	require.Contains(t, sm.PCCRules, "good")
	require.NotContains(t, sm.PCCRules, "bad")
	require.NotEmpty(t, sm.PCCRules["good"].Datapath.GetChargingUrr(sm))
	require.Empty(t, sm.DataPathToBeRemoved)
}
