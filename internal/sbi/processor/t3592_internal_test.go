package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

// A T3592 retransmission whose N1N2MessageTransfer fails (AMF 404/409,
// timeout, unreachable AMF) must release SMLock; otherwise every later
// UpdateSMContext for the session (the Release Complete included) blocks.
func TestT3592RetransmissionFailureReleasesSMLock(t *testing.T) {
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info:          &factory.Info{Description: "T3592 retransmission regression"},
		Configuration: &factory.Configuration{Sbi: &factory.Sbi{}},
	}))
	self := smf_context.GetSelf()
	oldOAuth2Required, oldConfig := self.OAuth2Required, factory.SmfConfig
	self.OAuth2Required = false
	factory.SmfConfig = &factory.Config{Configuration: &factory.Configuration{
		T3592: &factory.TimerValue{Enable: true, ExpireTime: 10 * time.Millisecond, MaxRetryTimes: 1000},
	}}
	t.Cleanup(func() {
		self.OAuth2Required, factory.SmfConfig = oldOAuth2Required, oldConfig
	})

	smContext := smf_context.NewSMContext("imsi-208930000000258", 5)
	t.Cleanup(func() { smf_context.RemoveSMContext(smContext.Ref) })
	smContext.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000258", PduSessionId: 5,
	}
	// An empty Namf_Communication API prefix makes every N1N2MessageTransfer
	// fail before any request is sent.
	smContext.CommunicationClientApiPrefix = ""

	p := newEventExposureProcessor(t)
	smContext.SMLock.Lock()
	p.sendGSMPDUSessionReleaseCommand(smContext, []byte{0x2e, 0x05, 0x02, 0xd3, 0x24})
	timer := smContext.T3592
	smContext.SMLock.Unlock()
	require.NotNil(t, timer)
	t.Cleanup(timer.Stop)

	require.Eventually(t, func() bool { return timer.ExpireTimes() >= 2 }, 2*time.Second, time.Millisecond,
		"T3592 did not expire twice")
	require.Eventually(t, smContext.SMLock.TryLock, time.Second, time.Millisecond,
		"SMLock is still held after a failed T3592 retransmission")
	smContext.SMLock.Unlock()
}
