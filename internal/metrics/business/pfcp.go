package business

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/free5gc/util/metrics/utils"
)

var (
	// PfcpRequestCounter Counter for PFCP requests sent to UPFs, labeled by message type, UPF and result.
	PfcpRequestCounter *prometheus.CounterVec
	// PfcpRequestDurationHist Histogram for the PFCP request/response round trip in seconds, labeled by
	// message type and UPF. Only requests that received a response are observed.
	PfcpRequestDurationHist *prometheus.HistogramVec
)

func GetPfcpHandlerMetrics(namespace string) []prometheus.Collector {
	var collectors []prometheus.Collector

	PfcpRequestCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: SUBSYSTEM_NAME,
			Name:      PFCP_REQUEST_COUNTER_NAME,
			Help:      PFCP_REQUEST_COUNTER_DESC,
		},
		[]string{PFCP_MESSAGE_TYPE_LABEL, PFCP_UPF_LABEL, PFCP_RESULT_LABEL},
	)

	collectors = append(collectors, PfcpRequestCounter)

	PfcpRequestDurationHist = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: SUBSYSTEM_NAME,
			Name:      PFCP_REQUEST_DURATION_HIST_NAME,
			Help:      PFCP_REQUEST_DURATION_HIST_DESC,
			// A UPF on the same network answers in well under 10ms. The buckets above 1s catch
			// responses that only arrived after a retransmission (the default T1 is 3s).
			Buckets: []float64{
				0.0005,
				0.001,
				0.0025,
				0.005,
				0.01,
				0.025,
				0.05,
				0.1,
				0.25,
				0.5,
				1,
				2.5,
				5,
				10,
			},
		},
		[]string{PFCP_MESSAGE_TYPE_LABEL, PFCP_UPF_LABEL},
	)

	collectors = append(collectors, PfcpRequestDurationHist)

	return collectors
}

// ObservePfcpRequest records the result of one PFCP request. The latency is only observed when the
// UPF answered (success or rejected): timeouts would only add the retransmission timer to the
// histogram, and they are already counted with result="timeout".
func ObservePfcpRequest(messageType string, upf string, result string, latency time.Duration) {
	if utils.IsBusinessMetricsEnabled() && IsPfcpMetricsEnabled() {
		PfcpRequestCounter.With(prometheus.Labels{
			PFCP_MESSAGE_TYPE_LABEL: messageType,
			PFCP_UPF_LABEL:          upf,
			PFCP_RESULT_LABEL:       result,
		}).Inc()

		if result == PFCP_RESULT_SUCCESS || result == PFCP_RESULT_REJECTED {
			PfcpRequestDurationHist.With(prometheus.Labels{
				PFCP_MESSAGE_TYPE_LABEL: messageType,
				PFCP_UPF_LABEL:          upf,
			}).Observe(latency.Seconds())
		}
	}
}
