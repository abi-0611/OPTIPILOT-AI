package slo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/metrics"
)

// defaultErrorBudgetTotal is used when no ErrorBudget is specified.
const defaultErrorBudgetTotal = 0.001 // 0.1%

// defaultEvaluationPeriod is the rolling window used for budget consumption.
const defaultEvaluationPeriod = 30 * 24 * time.Hour // 30 days

// SLOEvaluationResult holds the output of an SLO evaluation cycle.
type SLOEvaluationResult struct {
	// Objectives contains per-objective results.
	Objectives []ObjectiveResult

	// BudgetRemaining is the composite remaining error budget (0.0 to 1.0).
	BudgetRemaining float64

	// AllCompliant is true when every objective is within its target.
	AllCompliant bool

	// EvaluatedAt is the wall-clock time of this evaluation.
	EvaluatedAt time.Time
}

// ObjectiveResult holds the evaluation result for a single SLO objective.
type ObjectiveResult struct {
	Metric     slov1alpha1.MetricType
	Target     float64 // parsed numeric target
	Actual     float64 // current measured value from Prometheus
	BurnRate   float64 // current burn rate against the error budget
	Compliant  bool    // whether actual satisfies target
	BudgetUsed float64 // fraction of total error budget consumed (0.0 → 1.0)
}

// SLOEvaluator evaluates ServiceObjective resources against Prometheus.
type SLOEvaluator struct {
	PromClient metrics.PrometheusClient
	Builder    *PromQLBuilder
}

// NewSLOEvaluator creates a new evaluator.
func NewSLOEvaluator(client metrics.PrometheusClient, builder *PromQLBuilder) *SLOEvaluator {
	return &SLOEvaluator{PromClient: client, Builder: builder}
}

// Evaluate queries Prometheus for every objective in the ServiceObjective and
// computes burn rates and budget consumption using the Google SRE multi-window model.
func (e *SLOEvaluator) Evaluate(ctx context.Context, so *slov1alpha1.ServiceObjective) (*SLOEvaluationResult, error) {
	errorBudgetTotal, err := parseErrorBudgetTotal(so.Spec.ErrorBudget)
	if err != nil {
		return nil, fmt.Errorf("parsing error budget: %w", err)
	}

	results := make([]ObjectiveResult, 0, len(so.Spec.Objectives))
	allCompliant := true
	worstBudgetUsed := 0.0

	for _, obj := range so.Spec.Objectives {
		window := obj.Window
		if window == "" {
			window = "5m"
		}

		query, err := e.Builder.BuildQuery(obj.Metric, window, obj.CustomQuery)
		if err != nil {
			return nil, fmt.Errorf("building query for metric %q: %w", obj.Metric, err)
		}

		actual, err := e.PromClient.Query(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("querying metric %q: %w", obj.Metric, err)
		}

		target, err := ParseTarget(obj.Target, obj.Metric)
		if err != nil {
			return nil, fmt.Errorf("parsing target %q for metric %q: %w", obj.Target, obj.Metric, err)
		}

		compliant := isCompliant(obj.Metric, actual, target)
		burnRate := computeBurnRate(obj.Metric, actual, target, errorBudgetTotal)
		budgetUsed := computeBudgetUsed(burnRate, parseDurationOrDefault(window))

		if !compliant {
			allCompliant = false
		}
		if budgetUsed > worstBudgetUsed {
			worstBudgetUsed = budgetUsed
		}

		results = append(results, ObjectiveResult{
			Metric:     obj.Metric,
			Target:     target,
			Actual:     actual,
			BurnRate:   burnRate,
			Compliant:  compliant,
			BudgetUsed: budgetUsed,
		})
	}

	budgetRemaining := 1.0 - worstBudgetUsed
	if budgetRemaining < 0 {
		budgetRemaining = 0
	}

	return &SLOEvaluationResult{
		Objectives:      results,
		BudgetRemaining: budgetRemaining,
		AllCompliant:    allCompliant,
		EvaluatedAt:     time.Now(),
	}, nil
}

// isCompliant returns true when the actual metric value satisfies the target.
// The compliance direction depends on metric type:
//   - Latency: actual <= target (lower is better)
//   - ErrorRate: actual <= target (lower is better)
//   - Availability: actual >= target (higher is better)
//   - Throughput: actual >= target (higher is better)
//   - Custom: actual >= target (higher is better, matches availability convention)
func isCompliant(metric slov1alpha1.MetricType, actual, target float64) bool {
	switch metric {
	case slov1alpha1.MetricLatencyP50,
		slov1alpha1.MetricLatencyP90,
		slov1alpha1.MetricLatencyP95,
		slov1alpha1.MetricLatencyP99,
		slov1alpha1.MetricErrorRate:
		return actual <= target
	default: // availability, throughput, custom
		return actual >= target
	}
}

// computeBurnRate calculates how fast the error budget is being consumed.
// A burn rate of 1.0 means the budget is consumed at exactly the sustainable rate.
// A burn rate > 1.0 means the budget will be exhausted before the end of the window.
func computeBurnRate(metric slov1alpha1.MetricType, actual, target, errorBudgetTotal float64) float64 {
	if errorBudgetTotal <= 0 {
		return 0
	}

	var badRatio float64
	switch metric {
	case slov1alpha1.MetricLatencyP50,
		slov1alpha1.MetricLatencyP90,
		slov1alpha1.MetricLatencyP95,
		slov1alpha1.MetricLatencyP99:
		// bad ratio = fraction of requests (approximated) exceeding the target
		// For latency percentiles: if actual > target, surplus ratio = (actual-target)/target
		if actual <= target {
			return 0
		}
		badRatio = (actual - target) / target

	case slov1alpha1.MetricErrorRate:
		// bad ratio = actual error rate directly
		badRatio = actual

	case slov1alpha1.MetricAvailability:
		// bad ratio = 1 - actual (downtime fraction)
		if actual >= 1.0 {
			return 0
		}
		badRatio = 1.0 - actual

	case slov1alpha1.MetricThroughput, slov1alpha1.MetricCustom:
		// For throughput/custom: compliance is binary; burn rate reflects deficit ratio
		if actual >= target {
			return 0
		}
		if target == 0 {
			return 0
		}
		badRatio = (target - actual) / target

	default:
		return 0
	}

	return badRatio / errorBudgetTotal
}

// computeBudgetUsed estimates the fraction of error budget consumed over the window.
// Based on: budgetUsed = burnRate * (windowDuration / evaluationPeriod)
func computeBudgetUsed(burnRate float64, window time.Duration) float64 {
	if window <= 0 {
		window = 5 * time.Minute
	}
	ratio := window.Seconds() / defaultEvaluationPeriod.Seconds()
	used := burnRate * ratio
	if used > 1.0 {
		return 1.0
	}
	return used
}

// ParseTarget converts a human-readable target string to a float64:
//
//	"200ms"  → 0.200 (seconds)
//	"1500ms" → 1.500 (seconds)
//	"0.1%"   → 0.001
//	"99.95%" → 0.9995
//	"1000rps"→ 1000.0
//	"0.5"    → 0.5  (bare float, used for custom metrics)
func ParseTarget(target string, _ slov1alpha1.MetricType) (float64, error) {
	target = strings.TrimSpace(target)

	if strings.HasSuffix(target, "ms") {
		s := strings.TrimSuffix(target, "ms")
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid ms value %q: %w", target, err)
		}
		return v / 1000.0, nil
	}

	if strings.HasSuffix(target, "rps") {
		s := strings.TrimSuffix(target, "rps")
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid rps value %q: %w", target, err)
		}
		return v, nil
	}

	if strings.HasSuffix(target, "%") {
		s := strings.TrimSuffix(target, "%")
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid percent value %q: %w", target, err)
		}
		return v / 100.0, nil
	}

	// Bare float (e.g. custom metrics)
	v, err := strconv.ParseFloat(target, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse target %q: %w", target, err)
	}
	return v, nil
}

// parseErrorBudgetTotal returns the total error budget as a fraction (0.0 to 1.0).
func parseErrorBudgetTotal(eb *slov1alpha1.ErrorBudget) (float64, error) {
	if eb == nil || eb.Total == "" {
		return defaultErrorBudgetTotal, nil
	}
	v, err := ParseTarget(eb.Total, slov1alpha1.MetricErrorRate)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// parseDurationOrDefault parses a PromQL window string like "5m", "1h", "30d".
// Returns 5m if parsing fails.
func parseDurationOrDefault(window string) time.Duration {
	d, err := parsePromQLDuration(window)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

// parsePromQLDuration parses PromQL-style duration strings (e.g., "5m", "1h", "30d", "30s").
func parsePromQLDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("duration too short: %q", s)
	}
	unit := s[len(s)-1]
	valStr := s[:len(s)-1]
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value in %q: %w", s, err)
	}
	switch unit {
	case 's':
		return time.Duration(val * float64(time.Second)), nil
	case 'm':
		return time.Duration(val * float64(time.Minute)), nil
	case 'h':
		return time.Duration(val * float64(time.Hour)), nil
	case 'd':
		return time.Duration(val * float64(24*time.Hour)), nil
	case 'w':
		return time.Duration(val * float64(7*24*time.Hour)), nil
	default:
		return 0, fmt.Errorf("unknown duration unit %q in %q", string(unit), s)
	}
}
