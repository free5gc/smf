//nolint:testpackage // Package-local access is required to verify the BSF HTTP client policy.
package consumer

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/free5gc/smf/pkg/app"
	"github.com/free5gc/smf/pkg/factory"
)

func TestSBIBsfTLSVerification(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	for _, verify := range []bool{false, true} {
		cfg := &factory.Config{Configuration: &factory.Configuration{Sbi: &factory.Sbi{Tls: &factory.Tls{Verify: verify}}}}
		mockApp := app.NewMockApp(gomock.NewController(t))
		mockApp.EXPECT().Config().Return(cfg).AnyTimes()
		service := &nbsfService{consumer: &Consumer{App: mockApp}}
		client := service.getHTTPClient()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if resp != nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Errorf("close response body: %v", closeErr)
			}
		}
		if verify {
			var unknown x509.UnknownAuthorityError
			if !errors.As(err, &unknown) {
				t.Fatalf("expected certificate rejection, got %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
