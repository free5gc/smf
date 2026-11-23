package processor

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/h2non/gock"
	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/app"
	"github.com/free5gc/smf/pkg/factory"
)

type aniReportApp struct {
	app.App
	consumer *consumer.Consumer
}

func (a *aniReportApp) Consumer() *consumer.Consumer { return a.consumer }

// TS 29.512 clause 4.2.4.9: requested access network information is reported asynchronously.
func TestReportAccessNetworkInfo(t *testing.T) {
	const pcfURL = "http://127.0.0.7:8000"
	smfApp := &aniReportApp{}
	c, err := consumer.NewConsumer(smfApp)
	require.NoError(t, err)
	smfApp.consumer = c
	p, err := NewProcessor(smfApp)
	require.NoError(t, err)

	openapi.InterceptInnerHttp2Client(t, false)
	// Mocks left by other tests in this package could answer first: gock matches in registration order.
	gock.Flush()
	t.Cleanup(gock.Off)
	reports := make(chan models.Pcf_SMPolCtrl_SmPolicyUpdateContextData, 1)
	gock.New(pcfURL + factory.PcfSmpolicycontrolUriPrefix).Post("/sm-policies/imsi-208930000001241-10/update").
		AddMatcher(func(req *http.Request, _ *gock.Request) (bool, error) {
			raw, errRead := io.ReadAll(req.Body)
			if errRead != nil {
				return false, errRead
			}
			var report models.Pcf_SMPolCtrl_SmPolicyUpdateContextData
			if errJSON := json.Unmarshal(raw, &report); errJSON != nil {
				return false, errJSON
			}
			reports <- report
			return true, nil
		}).Reply(http.StatusOK).JSON(models.Pcf_SMPolCtrl_SmPolicyDecision{})

	ueLocation := &models.UserLocation{NrLocation: &models.NrLocation{
		Tai: &models.Tai{PlmnId: &models.PlmnId{Mcc: "208", Mnc: "93"}, Tac: "000001"},
	}}
	smContext := &smf_context.SMContext{
		Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{
			UeLocation: ueLocation,
			UeTimeZone: "+08:00",
		},
		SMPolicyID: "imsi-208930000001241-10",
		SelectedPCFProfile: models.Nrf_NFDisc_NFProfile{
			NfServices: []models.Nrf_NFDisc_NFService{{
				ServiceName:     models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL,
				NfServiceStatus: models.Nrf_NFMgmt_NFServiceStatus_REGISTERED,
				Scheme:          models.UriScheme_HTTP,
				IpEndPoints: []models.Nrf_NFMgmt_IpEndPoint{{
					Ipv4Address: "127.0.0.7",
					Transport:   models.Nrf_NFMgmt_TransportProtocol_TCP,
					Port:        8000,
				}},
			}},
		},
	}
	decision := &models.Pcf_SMPolCtrl_SmPolicyDecision{
		PolicyCtrlReqTriggers: []models.Pcf_SMPolCtrl_PolicyControlRequestTrigger{
			models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_AN_INFO,
		},
		LastReqRuleData: []models.Pcf_SMPolCtrl_RequestedRuleData{{
			RefPccRuleIds: []string{"PccRuleId-1"},
			ReqData: []models.Pcf_SMPolCtrl_RequestedRuleDataType{
				models.Pcf_SMPolCtrl_RequestedRuleDataType_USER_LOC_INFO,
			},
		}},
	}
	smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{SuppFeat: smf_context.SmPolicySuppFeat})
	smContext.RecordSmPolicyDecision(decision)

	p.reportAccessNetworkInfo(smContext, decision)

	select {
	case report := <-reports:
		require.Equal(t, models.Pcf_SMPolCtrl_SmPolicyUpdateContextData{
			RepPolicyCtrlReqTriggers: []models.Pcf_SMPolCtrl_PolicyControlRequestTrigger{
				models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_AN_INFO,
			},
			UserLocationInfo: ueLocation,
		}, report)
	case <-time.After(time.Second):
		t.Fatal("expected an access network information report to the PCF")
	}
}
