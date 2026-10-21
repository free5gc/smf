package consumer

import (
	"testing"

	"go.uber.org/mock/gomock"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/app"
	"github.com/free5gc/smf/pkg/factory"
)

// newTestSmfConfig is the smallest config InitSmfContext accepts.
func newTestSmfConfig() *factory.Config {
	return &factory.Config{
		Info: &factory.Info{
			Version:     "1.0.0",
			Description: "SMF consumer test configuration",
		},
		Configuration: &factory.Configuration{
			Sbi: &factory.Sbi{
				Scheme:       "http",
				RegisterIPv4: "127.0.0.1",
				BindingIPv4:  "127.0.0.1",
				Port:         8000,
			},
		},
	}
}

// newTestConsumer builds a Consumer over the process SMF context, which
// buildNfProfile reads through, and restores it when the test ends.
func newTestConsumer(t *testing.T, cfg *factory.Config) *Consumer {
	t.Helper()

	saved := *smf_context.GetSelf()
	t.Cleanup(func() { *smf_context.GetSelf() = saved })

	if err := smf_context.InitSmfContext(cfg); err != nil {
		t.Fatalf("InitSmfContext: %v", err)
	}
	smfContext := smf_context.GetSelf()
	smfContext.NfInstanceID = testNfId
	smfContext.NrfUri = testNrfUri

	mockApp := app.NewMockApp(gomock.NewController(t))
	mockApp.EXPECT().Context().Return(smfContext).AnyTimes()
	mockApp.EXPECT().Config().Return(cfg).AnyTimes()

	testConsumer, err := NewConsumer(mockApp)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}

	return testConsumer
}
