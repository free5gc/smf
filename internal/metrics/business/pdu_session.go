package business

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/free5gc/util/metrics/utils"
)

var (
	// PduSessionEstablishmentCounter Counter for PDU session establishment outcomes, labeled by status
	// (successful, failure) and cause (empty on success).
	PduSessionEstablishmentCounter *prometheus.CounterVec
	// PduSessionReleaseCounter Counter for PDU session release outcomes, labeled by trigger, status and cause.
	PduSessionReleaseCounter *prometheus.CounterVec
)

// GetPduSessionHandlerMetrics returns the PDU session collectors. countByState is called on every
// scrape and must return the number of SM contexts per state name. It is passed in rather than
// imported so that this package does not depend on the SMF context package.
func GetPduSessionHandlerMetrics(namespace string, countByState func() map[string]int) []prometheus.Collector {
	var collectors []prometheus.Collector

	PduSessionEstablishmentCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: SUBSYSTEM_NAME,
			Name:      PDU_SESSION_ESTABLISHMENT_COUNTER_NAME,
			Help:      PDU_SESSION_ESTABLISHMENT_COUNTER_DESC,
		},
		[]string{PDU_SESSION_STATUS_LABEL, PDU_SESSION_CAUSE_LABEL},
	)

	collectors = append(collectors, PduSessionEstablishmentCounter)

	PduSessionReleaseCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: SUBSYSTEM_NAME,
			Name:      PDU_SESSION_RELEASE_COUNTER_NAME,
			Help:      PDU_SESSION_RELEASE_COUNTER_DESC,
		},
		[]string{PDU_SESSION_TRIGGER_LABEL, PDU_SESSION_STATUS_LABEL, PDU_SESSION_CAUSE_LABEL},
	)

	collectors = append(collectors, PduSessionReleaseCounter)

	stateCollector := &pduSessionStateCollector{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, SUBSYSTEM_NAME, PDU_SESSION_STATE_GAUGE_NAME),
			PDU_SESSION_STATE_GAUGE_DESC,
			[]string{PDU_SESSION_STATE_LABEL},
			nil,
		),
		countByState: countByState,
	}

	collectors = append(collectors, stateCollector)

	return collectors
}

func IncrPduSessionEstablishmentSuccess() {
	incrPduSessionEstablishmentCounter(utils.SuccessMetric, PDU_SESSION_EMPTY_CAUSE)
}

// IncrPduSessionEstablishmentFailure counts a failed establishment. cause must come from a fixed set
// (a 3GPP cause constant or one of the ESTABLISHMENT_* constants) to keep the label bounded.
func IncrPduSessionEstablishmentFailure(cause string) {
	incrPduSessionEstablishmentCounter(utils.FailureMetric, cause)
}

func incrPduSessionEstablishmentCounter(status string, cause string) {
	if utils.IsBusinessMetricsEnabled() && IsPduSessionMetricsEnabled() {
		PduSessionEstablishmentCounter.With(prometheus.Labels{
			PDU_SESSION_STATUS_LABEL: status,
			PDU_SESSION_CAUSE_LABEL:  cause,
		}).Inc()
	}
}

func IncrPduSessionReleaseSuccess(trigger string) {
	incrPduSessionReleaseCounter(trigger, utils.SuccessMetric, PDU_SESSION_EMPTY_CAUSE)
}

func IncrPduSessionReleaseFailure(trigger string, cause string) {
	incrPduSessionReleaseCounter(trigger, utils.FailureMetric, cause)
}

func incrPduSessionReleaseCounter(trigger string, status string, cause string) {
	if utils.IsBusinessMetricsEnabled() && IsPduSessionMetricsEnabled() {
		PduSessionReleaseCounter.With(prometheus.Labels{
			PDU_SESSION_TRIGGER_LABEL: trigger,
			PDU_SESSION_STATUS_LABEL:  status,
			PDU_SESSION_CAUSE_LABEL:   cause,
		}).Inc()
	}
}

// pduSessionStateCollector reports the number of SM contexts per state by counting the SM context
// pool at scrape time. A gauge updated with Inc/Dec on every state change would drift whenever a
// code path forgets to update it, and the leak paths are exactly such paths. Counting the pool
// shows what the SMF really holds, including leaked contexts.
type pduSessionStateCollector struct {
	desc         *prometheus.Desc
	countByState func() map[string]int
}

func (c *pduSessionStateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *pduSessionStateCollector) Collect(ch chan<- prometheus.Metric) {
	for state, count := range c.countByState() {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(count), state)
	}
}
