package service_test

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v2"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/nrf/NFMgmt"
	"github.com/free5gc/smf/pkg/factory"
)

func TestSBIConfigTLSVerification(t *testing.T) {
	t.Cleanup(func() {
		if err := openapi.ConfigureHTTP2Clients(
			openapi.TransportReadIdleTimeout, openapi.TransportPingTimeout, false,
		); err != nil {
			t.Errorf("restore HTTP clients: %v", err)
		}
	})
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{}`); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	server.EnableHTTP2 = true
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	for _, value := range []string{"", "false", "true"} {
		t.Run("verify="+value, func(t *testing.T) {
			data := "configuration:\n  sbi:\n    scheme: https\n"
			if value != "" {
				data += "    tls:\n      verify: " + value + "\n"
			}
			var cfg factory.Config
			if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.GetTLSVerify() != (value == "true") {
				t.Fatal("verify parsing mismatch")
			}
			if err := openapi.ConfigureHTTP2Clients(
				cfg.GetReadIdleTimeout(), cfg.GetPingTimeout(), cfg.GetTLSVerify(),
			); err != nil {
				t.Fatal(err)
			}
			apiCfg := NFMgmt.NewConfiguration()
			apiCfg.SetBasePath(server.URL)
			client := NFMgmt.NewAPIClient(apiCfg)
			req := &NFMgmt.GetNFInstanceRequest{}
			req.SetNfInstanceID("tls-test")
			before := requests.Load()
			_, err := client.NFInstanceIDDocumentApi.GetNFInstance(context.Background(), req)
			if value == "true" {
				var unknown x509.UnknownAuthorityError
				if !errors.As(err, &unknown) {
					t.Fatalf("expected unknown authority, got %v", err)
				}
				if requests.Load() != before {
					t.Fatal("HTTP handler reached despite rejected certificate")
				}
			} else if err != nil {
				t.Fatal(err)
			} else if requests.Load() != before+1 {
				t.Fatal("request not received")
			}
		})
	}
}
