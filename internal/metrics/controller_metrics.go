package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// SLOBurnRate is the current burn rate per service per objective.
	// 1.0 means consuming budget at exactly the sustainable rate.
	SLOBurnRate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "optipilot",
			Subsystem: "slo",
			Name:      "burn_rate",
			Help:      "Current SLO burn rate (1.0 = consuming budget at exactly sustainable rate)",
		},
		[]string{"namespace", "service", "objective"},
	)

	// SLOBudgetRemaining is the remaining error budget as a ratio (0.0 to 1.0).
	SLOBudgetRemaining = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "optipilot",
			Subsystem: "slo",
			Name:      "budget_remaining_ratio",
			Help:      "Remaining error budget as a ratio (0.0 to 1.0)",
		},
		[]string{"namespace", "service"},
	)

	// SLOEvaluationDuration tracks how long each SLO evaluation takes.
	SLOEvaluationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "optipilot",
			Subsystem: "slo",
			Name:      "evaluation_duration_seconds",
			Help:      "Time taken to evaluate an SLO",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"namespace", "service"},
	)

	// SLOEvaluationErrors counts errors during SLO evaluation.
	SLOEvaluationErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "optipilot",
			Subsystem: "slo",
			Name:      "evaluation_errors_total",
			Help:      "Total number of SLO evaluation errors",
		},
		[]string{"namespace", "service", "reason"},
	)

	// SLOCompliant reports current compliance status (1 = compliant, 0 = violating).
	SLOCompliant = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "optipilot",
			Subsystem: "slo",
			Name:      "compliant",
			Help:      "Whether the SLO is currently compliant (1 = yes, 0 = no)",
		},
		[]string{"namespace", "service"},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		SLOBurnRate,
		SLOBudgetRemaining,
		SLOEvaluationDuration,
		SLOEvaluationErrors,
		SLOCompliant,
	)
}
