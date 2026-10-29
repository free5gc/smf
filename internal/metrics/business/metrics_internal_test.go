package business

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/free5gc/util/metrics/utils"
)

const (
	testNamespace   = "free5gc"
	testUpf         = "10.200.200.101"
	testActiveState = "Active"
)

// enableTestMetrics creates fresh collectors and turns business metrics on, the way
// service.getCustomMetrics does at startup when metrics are enabled.
func enableTestMetrics(t *testing.T, countByState func() map[string]int) []prometheus.Collector {
	t.Helper()

	collectors := GetPduSessionHandlerMetrics(testNamespace, countByState)
	collectors = append(collectors, GetPfcpHandlerMetrics(testNamespace)...)
	EnablePduSessionMetrics()
	EnablePfcpMetrics()
	utils.EnableBusinessMetrics()

	t.Cleanup(func() {
		pduSessionMetricsEnabled = false
		pfcpMetricsEnabled = false
	})
	return collectors
}

func TestCollectorsRegisterWithExpectedNames(t *testing.T) {
	collectors := enableTestMetrics(t, func() map[string]int { return map[string]int{testActiveState: 0} })

	reg := prometheus.NewRegistry()
	for _, collector := range collectors {
		require.NoError(t, reg.Register(collector))
	}

	// Vectors only appear in a scrape once a label combination is used.
	IncrPduSessionEstablishmentSuccess()
	IncrPduSessionReleaseSuccess(RELEASE_TRIGGER_UE_REQUESTED)
	ObservePfcpRequest(PFCP_MSG_HEARTBEAT, testUpf, PFCP_RESULT_SUCCESS, time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)
	var names []string
	for _, family := range families {
		names = append(names, family.GetName())
	}
	require.ElementsMatch(t, []string{
		"free5gc_smf_business_pdu_session_establishment_total",
		"free5gc_smf_business_pdu_session_release_total",
		"free5gc_smf_business_pdu_session_current_count",
		"free5gc_smf_business_pfcp_request_total",
		"free5gc_smf_business_pfcp_request_duration_seconds",
	}, names)
}

func TestPduSessionEstablishmentCounter(t *testing.T) {
	enableTestMetrics(t, nil)

	IncrPduSessionEstablishmentSuccess()
	IncrPduSessionEstablishmentSuccess()
	IncrPduSessionEstablishmentFailure("DNN_NOT_SUPPORTED")
	IncrPduSessionEstablishmentFailure(ESTABLISHMENT_PFCP_FAILURE)

	require.InDelta(t, 2, testutil.ToFloat64(
		PduSessionEstablishmentCounter.WithLabelValues(utils.SuccessMetric, PDU_SESSION_EMPTY_CAUSE)), 0)
	require.InDelta(t, 1, testutil.ToFloat64(
		PduSessionEstablishmentCounter.WithLabelValues(utils.FailureMetric, "DNN_NOT_SUPPORTED")), 0)
	require.InDelta(t, 1, testutil.ToFloat64(
		PduSessionEstablishmentCounter.WithLabelValues(utils.FailureMetric, ESTABLISHMENT_PFCP_FAILURE)), 0)
	require.Equal(t, 3, testutil.CollectAndCount(PduSessionEstablishmentCounter))
}

func TestPduSessionReleaseCounter(t *testing.T) {
	enableTestMetrics(t, nil)

	IncrPduSessionReleaseSuccess(RELEASE_TRIGGER_AMF_REQUESTED)
	IncrPduSessionReleaseFailure(RELEASE_TRIGGER_DUPLICATE_SM_CONTEXT, RELEASE_PFCP_DELETION_FAILURE)

	require.InDelta(t, 1, testutil.ToFloat64(PduSessionReleaseCounter.WithLabelValues(
		RELEASE_TRIGGER_AMF_REQUESTED, utils.SuccessMetric, PDU_SESSION_EMPTY_CAUSE)), 0)
	require.InDelta(t, 1, testutil.ToFloat64(PduSessionReleaseCounter.WithLabelValues(
		RELEASE_TRIGGER_DUPLICATE_SM_CONTEXT, utils.FailureMetric, RELEASE_PFCP_DELETION_FAILURE)), 0)
}

func TestPduSessionStateCollectorCountsAtScrapeTime(t *testing.T) {
	counts := map[string]int{testActiveState: 2, "ActivePending": 0, "InActive": 1}
	collectors := enableTestMetrics(t, func() map[string]int { return counts })
	stateCollector := collectors[2]

	expected := `
# HELP free5gc_smf_business_pdu_session_current_count ` + PDU_SESSION_STATE_GAUGE_DESC + `
# TYPE free5gc_smf_business_pdu_session_current_count gauge
free5gc_smf_business_pdu_session_current_count{state="Active"} 2
free5gc_smf_business_pdu_session_current_count{state="ActivePending"} 0
free5gc_smf_business_pdu_session_current_count{state="InActive"} 1
`
	require.NoError(t, testutil.CollectAndCompare(stateCollector, strings.NewReader(expected)))

	// The value follows the pool on the next scrape without any Inc/Dec call.
	counts = map[string]int{testActiveState: 0, "ActivePending": 0, "InActive": 0}
	expected = `
# HELP free5gc_smf_business_pdu_session_current_count ` + PDU_SESSION_STATE_GAUGE_DESC + `
# TYPE free5gc_smf_business_pdu_session_current_count gauge
free5gc_smf_business_pdu_session_current_count{state="Active"} 0
free5gc_smf_business_pdu_session_current_count{state="ActivePending"} 0
free5gc_smf_business_pdu_session_current_count{state="InActive"} 0
`
	require.NoError(t, testutil.CollectAndCompare(stateCollector, strings.NewReader(expected)))
}

func TestObservePfcpRequest(t *testing.T) {
	enableTestMetrics(t, nil)

	ObservePfcpRequest(PFCP_MSG_HEARTBEAT, testUpf, PFCP_RESULT_SUCCESS, 250*time.Millisecond)
	ObservePfcpRequest(PFCP_MSG_HEARTBEAT, testUpf, PFCP_RESULT_REJECTED, 500*time.Millisecond)
	ObservePfcpRequest(PFCP_MSG_HEARTBEAT, testUpf, PFCP_RESULT_TIMEOUT, 12*time.Second)

	for _, result := range []string{PFCP_RESULT_SUCCESS, PFCP_RESULT_REJECTED, PFCP_RESULT_TIMEOUT} {
		require.InDelta(t, 1, testutil.ToFloat64(
			PfcpRequestCounter.WithLabelValues(PFCP_MSG_HEARTBEAT, testUpf, result)), 0, result)
	}

	// The timeout is counted above but is not a response, so only two latencies are observed.
	expected := `
# HELP free5gc_smf_business_pfcp_request_duration_seconds ` + PFCP_REQUEST_DURATION_HIST_DESC + `
# TYPE free5gc_smf_business_pfcp_request_duration_seconds histogram
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.0005"} 0
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.001"} 0
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.0025"} 0
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.005"} 0
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.01"} 0
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.025"} 0
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.05"} 0
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.1"} 0
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.25"} 1
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="0.5"} 2
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="1"} 2
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="2.5"} 2
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="5"} 2
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="10"} 2
free5gc_smf_business_pfcp_request_duration_seconds_bucket{message_type="heartbeat",upf="10.200.200.101",le="+Inf"} 2
free5gc_smf_business_pfcp_request_duration_seconds_sum{message_type="heartbeat",upf="10.200.200.101"} 0.75
free5gc_smf_business_pfcp_request_duration_seconds_count{message_type="heartbeat",upf="10.200.200.101"} 2
`
	require.NoError(t, testutil.CollectAndCompare(PfcpRequestDurationHist, strings.NewReader(expected)))
}

func TestRecordingIsNoopWhenMetricsAreDisabled(t *testing.T) {
	// With metrics disabled at startup the collectors are never created. The recording functions
	// must then return before touching the nil vectors.
	PduSessionEstablishmentCounter = nil
	PduSessionReleaseCounter = nil
	PfcpRequestCounter = nil
	PfcpRequestDurationHist = nil
	pduSessionMetricsEnabled = false
	pfcpMetricsEnabled = false

	require.NotPanics(t, func() {
		IncrPduSessionEstablishmentSuccess()
		IncrPduSessionEstablishmentFailure(ESTABLISHMENT_PFCP_FAILURE)
		IncrPduSessionReleaseSuccess(RELEASE_TRIGGER_UE_REQUESTED)
		IncrPduSessionReleaseFailure(RELEASE_TRIGGER_UE_REQUESTED, RELEASE_PFCP_DELETION_FAILURE)
		ObservePfcpRequest(PFCP_MSG_HEARTBEAT, testUpf, PFCP_RESULT_TIMEOUT, time.Second)
	})
}
