package prebid

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricsKey is the stage-map key for this module: vendor.module with dots
// replaced, matching modules.moduleReplacer.
const MetricsKey = "openadtech_vastlint"

const (
	bidSkipped  = "skipped"
	bidChecked  = "checked"
	bidRejected = "rejected"
	bidError    = "error"
)

type recorder interface {
	Finding(caller, ruleID string, revenue bool)
	Bid(caller, result string)
}

type nopRecorder struct{}

func (nopRecorder) Finding(string, string, bool) {}

func (nopRecorder) Bid(string, string) {}

type promRecorder struct {
	findings *prometheus.CounterVec
	bids     *prometheus.CounterVec
}

func (p promRecorder) Finding(caller, ruleID string, revenue bool) {
	impact := "false"
	if revenue {
		impact = "true"
	}
	p.findings.WithLabelValues(caller, ruleID, impact).Inc()
}

func (p promRecorder) Bid(caller, result string) {
	p.bids.WithLabelValues(caller, result).Inc()
}

var (
	tallyMu sync.RWMutex
	tally   recorder = nopRecorder{}
)

// Register adds vastlint_findings_total to the Prebid Server Prometheus
// registry. Hosts scrape it on the existing /metrics port. The series is
// created only when this module is enabled.
func Register(reg prometheus.Registerer, namespace, subsystem string) {
	if reg == nil {
		return
	}
	findings := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "vastlint_findings_total",
		Help:      "Count of vastlint findings on video adm, labeled by bidder, rule id, and revenue impact.",
	}, []string{"caller", "rule_id", "revenue_impact"})
	bids := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "vastlint_bids_total",
		Help:      "Count of bids seen by vastlint, labeled by bidder and result (checked, skipped, rejected, error).",
	}, []string{"caller", "result"})
	reg.MustRegister(findings)
	reg.MustRegister(bids)

	tallyMu.Lock()
	tally = promRecorder{findings: findings, bids: bids}
	tallyMu.Unlock()
}

func currentRecorder() recorder {
	tallyMu.RLock()
	defer tallyMu.RUnlock()
	return tally
}

func resetRecorder() {
	tallyMu.Lock()
	tally = nopRecorder{}
	tallyMu.Unlock()
}
