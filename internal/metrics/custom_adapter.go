package metrics

import (
	"context"
	"fmt"
	"math"
	"sync"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
)

// CustomMetricResult holds the outcome of querying a single custom metric.
type CustomMetricResult struct {
	Name   string
	Value  float64
	Target float64
	Weight float64
	Err    error
}

// CustomMetricAdapter queries Prometheus for each CustomMetric defined in a
// ServiceObjective and produces a map suitable for EvalContext.Metrics.
type CustomMetricAdapter struct {
	client PrometheusClient
}

// NewCustomMetricAdapter creates an adapter backed by the given Prometheus client.
func NewCustomMetricAdapter(client PrometheusClient) *CustomMetricAdapter {
	return &CustomMetricAdapter{client: client}
}

// Fetch queries all custom metrics concurrently and returns {name → value}.
// Errors are collected per-metric; metrics that fail are omitted from the map.
// The second return value contains per-metric results (including errors) for
// observability.
func (a *CustomMetricAdapter) Fetch(ctx context.Context, metrics []slov1alpha1.CustomMetric) (map[string]float64, []CustomMetricResult) {
	if len(metrics) == 0 {
		return nil, nil
	}

	results := make([]CustomMetricResult, len(metrics))
	var wg sync.WaitGroup
	wg.Add(len(metrics))

	for i, m := range metrics {
		go func(idx int, cm slov1alpha1.CustomMetric) {
			defer wg.Done()
			r := CustomMetricResult{
				Name:   cm.Name,
				Target: cm.Target,
				Weight: cm.Weight,
			}
			val, err := a.client.Query(ctx, cm.Query)
			if err != nil {
				r.Err = fmt.Errorf("custom metric %q: %w", cm.Name, err)
			} else {
				r.Value = val
			}
			results[idx] = r
		}(i, m)
	}
	wg.Wait()

	out := make(map[string]float64, len(metrics))
	for _, r := range results {
		if r.Err == nil {
			out[r.Name] = r.Value
		}
	}
	return out, results
}

// Score computes a weighted distance score for the given metric results.
// For each metric with Weight > 0:
//
//	distance = |value - target| / max(|target|, 1)
//	score   += weight * distance
//
// Lower is better. Metrics that had query errors are skipped.
// Returns 0 when no scoreable metrics exist.
func Score(results []CustomMetricResult) float64 {
	var total float64
	for _, r := range results {
		if r.Err != nil || r.Weight <= 0 {
			continue
		}
		denom := math.Abs(r.Target)
		if denom < 1.0 {
			denom = 1.0
		}
		distance := math.Abs(r.Value-r.Target) / denom
		total += r.Weight * distance
	}
	return total
}

// MergeIntoMetrics merges fetched values into an existing metrics map.
// If dst is nil a new map is created. Existing keys are overwritten.
func MergeIntoMetrics(dst map[string]float64, fetched map[string]float64) map[string]float64 {
	if dst == nil {
		dst = make(map[string]float64, len(fetched))
	}
	for k, v := range fetched {
		dst[k] = v
	}
	return dst
}
