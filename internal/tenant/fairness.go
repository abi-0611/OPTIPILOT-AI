package tenant

import (
	"math"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// FairnessIndex is the global Jain's fairness index across all tenants.
	FairnessIndex = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "optipilot",
			Name:      "fairness_index",
			Help:      "Global Jain's fairness index across all tenants (1/n worst, 1.0 perfectly fair)",
		},
	)

	// TenantFairnessScore is the per-tenant xi = actual/guaranteed ratio.
	TenantFairnessScore = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "optipilot",
			Subsystem: "tenant",
			Name:      "fairness_score",
			Help:      "Per-tenant fairness ratio (actual_allocation / guaranteed_share)",
		},
		[]string{"tenant"},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		FairnessIndex,
		TenantFairnessScore,
	)
}

// FairnessInput holds one tenant's data for the fairness calculation.
type FairnessInput struct {
	Name            string
	CurrentCores    float64
	GuaranteedCores float64
}

// FairnessResult holds the computed fairness values.
type FairnessResult struct {
	// GlobalIndex is the Jain's fairness index J(x) across all tenants.
	// Range: 1/n (worst) to 1.0 (perfectly fair).
	GlobalIndex float64

	// PerTenant maps tenant name to xi = actual/guaranteed ratio.
	PerTenant map[string]float64
}

// ComputeFairness calculates Jain's fairness index.
//
//	J(x) = (Σ xi)² / (n × Σ xi²)
//
// where xi = actual_allocation / guaranteed_share for each tenant.
// Tenants with zero guaranteed share are excluded from the calculation.
// Returns nil if fewer than 1 tenant with guaranteed > 0.
func ComputeFairness(inputs []FairnessInput) *FairnessResult {
	perTenant := make(map[string]float64, len(inputs))
	var xs []float64

	for _, in := range inputs {
		if in.GuaranteedCores <= 0 {
			continue
		}
		xi := in.CurrentCores / in.GuaranteedCores
		perTenant[in.Name] = xi
		xs = append(xs, xi)
	}

	n := float64(len(xs))
	if n == 0 {
		return nil
	}

	sumX := 0.0
	sumX2 := 0.0
	for _, x := range xs {
		sumX += x
		sumX2 += x * x
	}

	var index float64
	if sumX2 == 0 {
		// All tenants at 0 usage with 0 guaranteed → perfectly fair vacuously.
		index = 1.0
	} else {
		index = (sumX * sumX) / (n * sumX2)
	}

	// Clamp to [0, 1] in case of floating-point drift.
	index = math.Max(0, math.Min(1, index))

	return &FairnessResult{
		GlobalIndex: index,
		PerTenant:   perTenant,
	}
}

// RecordFairnessMetrics updates the Prometheus gauges with the latest fairness values.
func RecordFairnessMetrics(result *FairnessResult) {
	if result == nil {
		return
	}
	FairnessIndex.Set(result.GlobalIndex)
	for name, score := range result.PerTenant {
		TenantFairnessScore.WithLabelValues(name).Set(score)
	}
}
