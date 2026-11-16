package sbi

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

func TestServiceOAuthAuthorization(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	nrfID, smfID := uuid.NewString(), uuid.NewString()
	identity, err := url.Parse("urn:uuid:" + nrfID)
	require.NoError(t, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
		URIs:     []*url.URL{identity},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(t, err)
	certPath := filepath.Join(t.TempDir(), "nrf.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))

	tests := []struct {
		name     string
		audience string
		issuer   string
		scope    string
		expiry   int64
		scheme   string
		disabled bool
		want     int
	}{
		{
			"instance token", smfID, nrfID, "nsmf-pdusession", time.Now().Add(time.Minute).Unix(), "Bearer", false,
			http.StatusNotImplemented,
		},
		{
			"type token", "SMF", nrfID, "nsmf-pdusession", time.Now().Add(time.Minute).Unix(), "Bearer", false,
			http.StatusNotImplemented,
		},
		{
			"wrong instance", uuid.NewString(), nrfID, "nsmf-pdusession", time.Now().Add(time.Minute).Unix(), "Bearer", false,
			http.StatusUnauthorized,
		},
		{
			"wrong issuer", smfID, uuid.NewString(), "nsmf-pdusession", time.Now().Add(time.Minute).Unix(), "Bearer", false,
			http.StatusUnauthorized,
		},
		{
			"expired", smfID, nrfID, "nsmf-pdusession", time.Now().Add(-time.Minute).Unix(), "Bearer", false,
			http.StatusUnauthorized,
		},
		{"missing expiry", smfID, nrfID, "nsmf-pdusession", 0, "Bearer", false, http.StatusUnauthorized},
		{
			"wrong scope", smfID, nrfID, "npcf-smpolicycontrol", time.Now().Add(time.Minute).Unix(), "Bearer", false,
			http.StatusUnauthorized,
		},
		{
			"wrong scheme", smfID, nrfID, "nsmf-pdusession", time.Now().Add(time.Minute).Unix(), "Basic", false,
			http.StatusUnauthorized,
		},
		{"missing token", smfID, nrfID, "nsmf-pdusession", 0, "", false, http.StatusUnauthorized},
		{"OAuth disabled", smfID, nrfID, "", 0, "", true, http.StatusNotImplemented},
	}
	self := smf_context.GetSelf()
	previousID, previousURI, previousOAuth := self.NfInstanceID, self.NrfUri, self.OAuth2Required
	previousCert, previousNRFID, previousConfig := self.NrfCertPem, self.NrfNfInstanceID, factory.SmfConfig
	t.Cleanup(func() {
		self.NfInstanceID, self.NrfUri, self.OAuth2Required = previousID, previousURI, previousOAuth
		self.NrfCertPem, self.NrfNfInstanceID, factory.SmfConfig = previousCert, previousNRFID, previousConfig
	})
	self.NfInstanceID, self.NrfUri, self.NrfCertPem = smfID, "http://127.0.0.1", certPath
	factory.SmfConfig = &factory.Config{Configuration: &factory.Configuration{
		ServiceNameList: []string{"nsmf-pdusession"},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, self.SetOAuth2Required(!tt.disabled))
			router := newRouter(&Server{})
			request := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, factory.SmfPdusessionResUriPrefix+"/pdu-sessions/ref/retrieve", nil)
			if tt.scheme != "" {
				claims := jwt.MapClaims{"iss": tt.issuer, "aud": tt.audience, "scope": tt.scope}
				if tt.expiry != 0 {
					claims["exp"] = tt.expiry
				}
				token, signErr := jwt.NewWithClaims(jwt.SigningMethodRS512, claims).SignedString(key)
				require.NoError(t, signErr)
				request.Header.Set("Authorization", tt.scheme+" "+token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			// The existing retrieve handler returns 501. Reaching it proves the
			// real service route still validates tokens while Notify is exempt.
			require.Equal(t, tt.want, response.Code, response.Body.String())
		})
	}
}
