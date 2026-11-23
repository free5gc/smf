package consumer_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/h2non/gock"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/smf/pkg/service"
)

const (
	aniTestPcfURL     = "http://127.0.0.7:8000"
	aniTestSmPolicyID = "imsi-208930000001241-10"
)

var aniTestPcfProfile = models.Nrf_NFDisc_NFProfile{
	NfInstanceId: "pcf-unit-testing",
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
}

// interceptPcf captures the body of one request to path of the PCF SM policy control service.
func interceptPcf(t *testing.T, path string, status int, body any) {
	t.Helper()
	openapi.InterceptInnerHttp2Client(t, false)
	// Mocks left by other tests in this package could answer first: gock matches in registration order.
	gock.Flush()
	t.Cleanup(gock.Off)
	mock := gock.New(aniTestPcfURL + factory.PcfSmpolicycontrolUriPrefix).Post(path).
		AddMatcher(func(req *http.Request, _ *gock.Request) (bool, error) {
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				return false, err
			}
			return true, json.Unmarshal(raw, body)
		}).Reply(status)
	if status == http.StatusOK {
		mock.JSON(models.Pcf_SMPolCtrl_SmPolicyDecision{})
	}
}

func newAniTestConsumer(t *testing.T) *consumer.Consumer {
	t.Helper()
	require.NoError(t, smf_context.InitSmfContext(&testConfig))
	c, err := consumer.NewConsumer(service.NewMockSmfAppInterface(gomock.NewController(t)))
	require.NoError(t, err)
	return c
}

// TS 29.512 clause 4.2.4.9: the access network information is reported in an SM policy update.
func TestSendSMPolicyAssociationUpdateByAccessNetworkInfo(t *testing.T) {
	c := newAniTestConsumer(t)
	var got models.Pcf_SMPolCtrl_SmPolicyUpdateContextData
	interceptPcf(t, "/sm-policies/"+aniTestSmPolicyID+"/update", http.StatusOK, &got)

	report := &models.Pcf_SMPolCtrl_SmPolicyUpdateContextData{
		RepPolicyCtrlReqTriggers: []models.Pcf_SMPolCtrl_PolicyControlRequestTrigger{
			models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_AN_INFO,
		},
		ServingNetwork: &models.PlmnIdNid{Mcc: "208", Mnc: "93"},
		UeTimeZone:     "+08:00",
	}
	require.NoError(t, c.SendSMPolicyAssociationUpdateByAccessNetworkInfo(&aniTestPcfProfile, aniTestSmPolicyID, report))
	require.True(t, gock.IsDone())
	require.Equal(t, *report, got)

	require.Error(t, c.SendSMPolicyAssociationUpdateByAccessNetworkInfo(
		&models.Nrf_NFDisc_NFProfile{}, aniTestSmPolicyID, report), "no PCF selected")
}

// TS 29.512 clause 4.2.5.2: the access network information is provided at termination with NetLoc.
func TestSendSMPolicyAssociationTerminationWithAccessNetworkInfo(t *testing.T) {
	c := newAniTestConsumer(t)
	var got models.Pcf_SMPolCtrl_SmPolicyDeleteData
	interceptPcf(t, "/sm-policies/"+aniTestSmPolicyID+"/delete", http.StatusNoContent, &got)

	ueLocation := &models.UserLocation{NrLocation: &models.NrLocation{
		Tai: &models.Tai{PlmnId: &models.PlmnId{Mcc: "208", Mnc: "93"}, Tac: "000001"},
	}}
	smContext := smf_context.NewSMContext("imsi-208930000001241", 10)
	t.Cleanup(func() { smf_context.RemoveSMContext(smContext.Ref) })
	smContext.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		UeLocation: ueLocation,
		UeTimeZone: "+08:00",
	}
	smContext.SelectedPCFProfile = aniTestPcfProfile
	smContext.SMPolicyID = aniTestSmPolicyID
	smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{SuppFeat: smf_context.SmPolicySuppFeat})

	require.NoError(t, c.SendSMPolicyAssociationTermination(smContext))
	require.True(t, gock.IsDone())
	require.Equal(t, models.Pcf_SMPolCtrl_SmPolicyDeleteData{
		UserLocationInfo: ueLocation,
		UeTimeZone:       "+08:00",
	}, got)
}
