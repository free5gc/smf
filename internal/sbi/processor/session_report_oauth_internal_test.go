package processor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/app"
)

type reportOAuthApp struct {
	app.App
	consumer *consumer.Consumer
}

func (a *reportOAuthApp) Consumer() *consumer.Consumer { return a.consumer }

func TestDownlinkNotificationBindsAMFInstance(t *testing.T) {
	self := smf_context.GetSelf()
	previousID, previousURI, previousOAuth := self.NfInstanceID, self.NrfUri, self.OAuth2Required
	t.Cleanup(func() {
		self.NfInstanceID, self.NrfUri, self.OAuth2Required = previousID, previousURI, previousOAuth
	})

	tokenRequests := make(chan url.Values, 1)
	nrf := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		tokenRequests <- r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"access_token":"report-token","token_type":"Bearer","expires_in":60}`))
		if err != nil {
			t.Errorf("write token response: %v", err)
		}
	}))
	nrf.EnableHTTP2 = true
	nrf.StartTLS()
	t.Cleanup(nrf.Close)

	transferTokens := make(chan string, 1)
	amf := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transferTokens <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, err := w.Write([]byte(`{"cause":"ATTEMPTING_TO_REACH_UE"}`))
		if err != nil {
			t.Errorf("write AMF response: %v", err)
		}
	}))
	amf.EnableHTTP2 = true
	amf.StartTLS()
	t.Cleanup(amf.Close)

	self.NfInstanceID, self.NrfUri, self.OAuth2Required = uuid.NewString(), nrf.URL, true
	amfID := uuid.NewString()
	smContext := &smf_context.SMContext{
		Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{Supi: "imsi-208930000000001"},
		PDUSessionID:                    1,
		AMFProfile:                      models.Nrf_NFDisc_NFProfile{NfInstanceId: amfID},
		CommunicationClientApiPrefix:    amf.URL,
		SelectedPDUSessionType:          nasie.PDUSessType_IPv4,
		SelectedSessionRuleID:           "default",
		SessionRules: map[string]*smf_context.SessionRule{
			"default": smf_context.NewSessionRule(&models.Pcf_SMPolCtrl_SessionRule{
				AuthSessAmbr: &models.Ambr{Uplink: "1 Mbps", Downlink: "1 Mbps"},
				AuthDefQos: &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{
					Var5qi: 9, Arp: &models.Arp{PriorityLevel: 1},
				},
			}),
		},
		Tunnel: &smf_context.UPTunnel{DataPathPool: smf_context.DataPathPool{
			1: {IsDefaultPath: true, FirstDPNode: &smf_context.DataPathNode{
				UPF: &smf_context.UPF{N3Interfaces: []*smf_context.UPFInterfaceInfo{
					{IPv4EndPointAddresses: []net.IP{net.ParseIP("192.0.2.1").To4()}},
				}},
			}},
		}},
	}
	application := &reportOAuthApp{}
	var err error
	application.consumer, err = consumer.NewConsumer(application)
	require.NoError(t, err)
	p, err := NewProcessor(application)
	require.NoError(t, err)
	require.NoError(t, p.notifyAMFOfDownlinkData(smContext))

	select {
	case request := <-tokenRequests:
		require.Equal(t, self.NfInstanceID, request.Get("nfInstanceId"))
		require.Equal(t, "namf-comm", request.Get("scope"))
		require.Equal(t, "AMF", request.Get("targetNfType"))
		require.Equal(t, amfID, request.Get("targetNfInstanceId"))
	default:
		t.Fatal("downlink notification did not request an NRF token")
	}
	select {
	case token := <-transferTokens:
		require.Equal(t, "Bearer report-token", token)
	default:
		t.Fatal("downlink notification did not reach AMF")
	}
}
