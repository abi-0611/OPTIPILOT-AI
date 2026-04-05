package engine

import (
	"github.com/optipilot-ai/optipilot/internal/metrics"
	"github.com/optipilot-ai/optipilot/internal/storage"
)

// ---------------------------------------------------------------------------
// AdvancedSignals holds optional Phase-11 signals that enrich the solver.
// All fields are nil-safe — when nil the solver behaves identically to before.
// ---------------------------------------------------------------------------

// AdvancedSignals bundles optional inputs from Phase-11 subsystems.
type AdvancedSignals struct {
	// CustomMetricResults from the CustomMetricAdapter (nil = not configured).
	CustomMetricResults []metrics.CustomMetricResult

	// TuningOverrides maps parameter-name → proposed-value from the
	// ApplicationTuning optimizer.  Nil = no tuning active.
	TuningOverrides map[string]string

	// StorageRecommendations from the Storage Recommender (nil = not configured).
	StorageRecommendations []storage.Recommendation
}

// ---------------------------------------------------------------------------
// Enrichment helpers
// ---------------------------------------------------------------------------

// EnrichMetrics merges custom-metric values into the solver input's Metrics map
// so that CEL expressions can reference them as `metrics.<name>`.
// No-op when signals or CustomMetricResults is nil.
func EnrichMetrics(input *SolverInput, signals *AdvancedSignals) {
	if signals == nil || len(signals.CustomMetricResults) == 0 {
		return
	}
	if input.Metrics == nil {
		input.Metrics = make(map[string]float64)
	}
	for _, r := range signals.CustomMetricResults {
		if r.Err == nil {
			input.Metrics[r.Name] = r.Value
		}
	}
}

// EnrichTuning copies ApplicationTuning parameter overrides into the
// ScalingAction.TuningParams. Called after the solver picks a winner.
// No-op when signals or TuningOverrides is nil.
func EnrichTuning(action *ScalingAction, signals *AdvancedSignals) {
	if signals == nil || len(signals.TuningOverrides) == 0 {
		return
	}
	if action.TuningParams == nil {
		action.TuningParams = make(map[string]string, len(signals.TuningOverrides))
	}
	for k, v := range signals.TuningOverrides {
		action.TuningParams[k] = v
	}
}

// CustomMetricScore returns the weighted-distance score from the custom
// metric adapter.  Returns 0 when no results are present (backward-compat).
func CustomMetricScore(signals *AdvancedSignals) float64 {
	if signals == nil || len(signals.CustomMetricResults) == 0 {
		return 0
	}
	return metrics.Score(signals.CustomMetricResults)
}

// StorageMonthlySavings sums estimated monthly savings across all storage
// recommendations.  Returns 0 when no recommendations exist.
func StorageMonthlySavings(signals *AdvancedSignals) float64 {
	if signals == nil || len(signals.StorageRecommendations) == 0 {
		return 0
	}
	total := 0.0
	for _, r := range signals.StorageRecommendations {
		total += r.EstMonthlySavings
	}
	return total
}

// StorageHourlySavingsEstimate converts monthly savings to an hourly estimate
// (divides by 730 hours/month).  Used to integrate into cost dimension scoring.
func StorageHourlySavingsEstimate(signals *AdvancedSignals) float64 {
	monthly := StorageMonthlySavings(signals)
	if monthly == 0 {
		return 0
	}
	return monthly / 730.0
}

// AdjustCostScore incorporates storage savings into a candidate's cost score.
// The adjustment is proportional: savings / (savings + current hourly cost).
// Returns the original score when no savings are available.
func AdjustCostScore(original float64, hourlyCost float64, signals *AdvancedSignals) float64 {
	hourly := StorageHourlySavingsEstimate(signals)
	if hourly <= 0 || hourlyCost <= 0 {
		return original
	}
	// Bonus capped at 0.2 to avoid storage savings dominating the cost dimension.
	bonus := hourly / (hourly + hourlyCost)
	if bonus > 0.2 {
		bonus = 0.2
	}
	adjusted := original + bonus
	if adjusted > 1.0 {
		adjusted = 1.0
	}
	return adjusted
}

// EnrichScoredCandidates applies storage cost adjustments and custom metric
// penalty to all candidates in-place.  Backward-compatible (no-op when nil).
func EnrichScoredCandidates(scored []ScoredCandidate, signals *AdvancedSignals) {
	if signals == nil {
		return
	}

	cmScore := CustomMetricScore(signals)

	for i := range scored {
		sc := &scored[i]

		// Storage savings adjust cost dimension.
		sc.Score.Cost = AdjustCostScore(sc.Score.Cost, sc.Plan.EstimatedCost, signals)

		// Custom metric distance penalises the weighted score.
		// Convention: lower distance is better → subtract from weighted.
		if cmScore > 0 {
			sc.Score.Weighted -= cmScore * 0.1 // 10% weight on custom metric distance
			if sc.Score.Weighted < 0 {
				sc.Score.Weighted = 0
			}
		}
	}
}
