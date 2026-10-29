package processor

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	business_metrics "github.com/free5gc/smf/internal/metrics/business"
	"github.com/free5gc/util/metrics/utils"
)

func TestReleaseSessionCountsOutcomeByTrigger(t *testing.T) {
	business_metrics.GetPduSessionHandlerMetrics("free5gc", smf_context.CountSMContextsByState)
	business_metrics.EnablePduSessionMetrics()
	utils.EnableBusinessMetrics()

	// Without data paths there is no PFCP session to delete, so the release succeeds.
	smContext := &smf_context.SMContext{
		Tunnel:   smf_context.NewUPTunnel(),
		DCTunnel: smf_context.NewUPTunnel(),
		Log:      logger.PduSessLog,
	}
	status := (&Processor{}).releaseSession(smContext, business_metrics.RELEASE_TRIGGER_AMF_REQUESTED)

	require.Equal(t, smf_context.SessionReleaseSuccess, status)
	require.True(t, smContext.PFCPReleaseDone)
	require.InDelta(t, 1, testutil.ToFloat64(business_metrics.PduSessionReleaseCounter.WithLabelValues(
		business_metrics.RELEASE_TRIGGER_AMF_REQUESTED, utils.SuccessMetric, business_metrics.PDU_SESSION_EMPTY_CAUSE,
	)), 0)
	require.Equal(t, 1, testutil.CollectAndCount(business_metrics.PduSessionReleaseCounter))
}
