package consumer

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/nrf/NFDisc"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/app"
)

func TestBuildNFProfileAuthorizationPolicy(t *testing.T) {
	services := []models.Nrf_NFMgmt_NFService{
		{ServiceName: models.Nrf_NFMgmt_ServiceName_NSMF_PDUSESSION},
		{ServiceName: smf_context.ServiceNameNsmfCallback},
		{ServiceName: models.Nrf_NFMgmt_ServiceName_NSMF_EVENT_EXPOSURE},
		{ServiceName: models.Nrf_NFMgmt_ServiceName_NSMF_OAM},
	}
	self := &smf_context.SMFContext{NfProfile: smf_context.NFProfile{
		NFServices: &services, SMFInfo: &models.Nrf_NFMgmt_SmfInfo{},
	}}
	s := &nnrfService{}
	profile, err := s.buildNfProfile(self)
	require.NoError(t, err)
	require.ElementsMatch(t, []models.Nrf_NFMgmt_NFType{
		models.Nrf_NFMgmt_NFType_AMF, models.Nrf_NFMgmt_NFType_SMF,
	}, profile.NfServices[0].AllowedNfTypes)
	require.ElementsMatch(t, []models.Nrf_NFMgmt_NFType{
		models.Nrf_NFMgmt_NFType_PCF, models.Nrf_NFMgmt_NFType_CHF,
	}, profile.NfServices[1].AllowedNfTypes)
	require.Contains(t, profile.NfServices[2].AllowedNfTypes, models.Nrf_NFMgmt_NFType_NEF)
	require.Empty(t, profile.NfServices[3].AllowedNfTypes)
	for _, service := range services {
		require.Empty(t, service.AllowedNfTypes, "building a profile must not change runtime services")
	}
	profile.NfServices[1].AllowedNfTypes[0] = models.Nrf_NFMgmt_NFType_AF
	next, err := s.buildNfProfile(self)
	require.NoError(t, err)
	require.NotContains(t, next.NfServices[1].AllowedNfTypes, models.Nrf_NFMgmt_NFType_AF)

	services = append(services, models.Nrf_NFMgmt_NFService{ServiceName: "nsmf-unknown"})
	_, err = s.buildNfProfile(self)
	require.ErrorContains(t, err, "no AllowedNfTypes policy")
	self.NfProfile.NFServices = nil
	_, err = s.buildNfProfile(self)
	require.ErrorContains(t, err, "NFServices is nil")
}

type oauthLifecycleApp struct {
	app.App
	self *smf_context.SMFContext
}

func (a *oauthLifecycleApp) Context() *smf_context.SMFContext { return a.self }

func TestNRFLifecycleOAuth(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			nrfID, smfID := uuid.NewString(), uuid.NewString()
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			require.NoError(t, err)
			identity, err := url.Parse("urn:uuid:" + nrfID)
			require.NoError(t, err)
			cert := &x509.Certificate{
				SerialNumber: big.NewInt(1), URIs: []*url.URL{identity},
				NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			}
			der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
			require.NoError(t, err)
			certPath := filepath.Join(t.TempDir(), "nrf.pem")
			require.NoError(t, os.WriteFile(certPath,
				pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
			tokenRequests := make(chan url.Values, 2)
			authorizedCalls := make(chan string, 2)
			registeredProfiles := make(chan models.Nrf_NFMgmt_NFProfile, 1)
			nrf := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodPut:
					var profile models.Nrf_NFMgmt_NFProfile
					if decodeErr := json.NewDecoder(r.Body).Decode(&profile); decodeErr != nil {
						t.Errorf("decode registration: %v", decodeErr)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					registeredProfiles <- profile
					profile.CustomInfo = map[string]interface{}{"oauth2": enabled}
					w.Header().Set("Location", "https://"+r.Host+r.URL.Path)
					w.WriteHeader(http.StatusCreated)
					if encodeErr := json.NewEncoder(w).Encode(profile); encodeErr != nil {
						t.Errorf("encode registration: %v", encodeErr)
					}
				case http.MethodPost:
					if parseErr := r.ParseForm(); parseErr != nil {
						t.Errorf("parse token request: %v", parseErr)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					tokenRequests <- r.PostForm
					if _, writeErr := w.Write([]byte(
						`{"access_token":"lifecycle-token","token_type":"Bearer","expires_in":60}`,
					)); writeErr != nil {
						t.Errorf("write token response: %v", writeErr)
					}
				case http.MethodGet:
					authorizedCalls <- r.Header.Get("Authorization")
					if _, writeErr := w.Write([]byte(`{"nfInstances":[]}`)); writeErr != nil {
						t.Errorf("write discovery response: %v", writeErr)
					}
				case http.MethodDelete:
					authorizedCalls <- r.Header.Get("Authorization")
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			nrf.EnableHTTP2 = true
			nrf.StartTLS()
			t.Cleanup(nrf.Close)

			services := []models.Nrf_NFMgmt_NFService{{ServiceName: smf_context.ServiceNameNsmfCallback}}
			self := &smf_context.SMFContext{
				NfInstanceID: smfID, NrfUri: nrf.URL, NrfCertPem: certPath,
				NfProfile: smf_context.NFProfile{NFServices: &services, SMFInfo: &models.Nrf_NFMgmt_SmfInfo{}},
			}
			c, err := NewConsumer(&oauthLifecycleApp{self: self})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, c.RegisterNFInstance(ctx))
			require.Equal(t, enabled, self.OAuth2Required)
			if enabled {
				require.Equal(t, nrfID, self.NrfNfInstanceID)
			}
			profile := <-registeredProfiles
			require.ElementsMatch(t, []models.Nrf_NFMgmt_NFType{
				models.Nrf_NFMgmt_NFType_PCF, models.Nrf_NFMgmt_NFType_CHF,
			}, profile.NfServices[0].AllowedNfTypes)
			target, requester := models.Nrf_NFMgmt_NFType_AMF, models.Nrf_NFMgmt_NFType_SMF
			_, err = c.SendSearchNFInstances(nrf.URL, target, requester,
				&NFDisc.SearchNFInstancesRequest{TargetNfType: &target, RequesterNfType: &requester})
			require.NoError(t, err)
			require.NoError(t, c.SendDeregisterNFInstance())
			for range 2 {
				header := <-authorizedCalls
				if enabled {
					require.Equal(t, "Bearer lifecycle-token", header)
				} else {
					require.Empty(t, header)
				}
			}
			if enabled {
				scopes := make([]string, 0, 2)
				for range 2 {
					request := <-tokenRequests
					require.Equal(t, smfID, request.Get("nfInstanceId"))
					require.Equal(t, nrfID, request.Get("targetNfInstanceId"))
					require.Equal(t, "NRF", request.Get("targetNfType"))
					scopes = append(scopes, request.Get("scope"))
				}
				require.ElementsMatch(t, []string{"nnrf-disc", "nnrf-nfm"}, scopes)
			} else {
				require.Empty(t, tokenRequests)
			}
		})
	}
}
