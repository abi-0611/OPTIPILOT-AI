package engine

import (
	"fmt"
	"math"
	"testing"

	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/metrics"
	"github.com/optipilot-ai/optipilot/internal/storage"
)

// ---------------------------------------------------------------------------
// EnrichMetrics
// ---------------------------------------------------------------------------

func TestEnrichMetrics_NilSignals(t *testing.T) {
	input := &SolverInput{}
	EnrichMetrics(input, nil)
	if input.Metrics != nil {
		t.Error("expected nil metrics")
	}
}

func TestEnrichMetrics_EmptyResults(t *testing.T) {
	input := &SolverInput{}
	EnrichMetrics(input, &AdvancedSignals{})
	if input.Metrics != nil {
		t.Error("expected nil metrics")
	}
}

func TestEnrichMetrics_MergesValues(t *testing.T) {
	input := &SolverInput{Metrics: map[string]float64{"existing": 1.0}}
	signals := &AdvancedSignals{
		CustomMetricResults: []metrics.CustomMetricResult{
			{Name: "gpu_util", Value: 0.85},
			{Name: "disk_io", Value: 120},
		},
	}
	EnrichMetrics(input, signals)
	if input.Metrics["gpu_util"] != 0.85 {
		t.Errorf("gpu_util=%f", input.Metrics["gpu_util"])
	}
	if input.Metrics["disk_io"] != 120 {
		t.Errorf("disk_io=%f", input.Metrics["disk_io"])
	}
	if input.Metrics["existing"] != 1.0 {
		t.Errorf("existing key lost")
	}
}

func TestEnrichMetrics_SkipsErrors(t *testing.T) {
	input := &SolverInput{}
	signals := &AdvancedSignals{
		CustomMetricResults: []metrics.CustomMetricResult{
			{Name: "good", Value: 42},
			{Name: "bad", Err: fmt.Errorf("fail")},
		},
	}
	EnrichMetrics(input, signals)
	if _, ok := input.Metrics["bad"]; ok {
		t.Error("errored metric should be skipped")
	}
	if input.Metrics["good"] != 42 {
		t.Errorf("good=%f", input.Metrics["good"])
	}
}

func TestEnrichMetrics_NilMap(t *testing.T) {
	input := &SolverInput{}
	signals := &AdvancedSignals{
		CustomMetricResults: []metrics.CustomMetricResult{
			{Name: "x", Value: 1},
		},
	}
	EnrichMetrics(input, signals)
	if input.Metrics == nil || input.Metrics["x"] != 1 {
		t.Errorf("metrics=%v", input.Metrics)
	}
}

// ---------------------------------------------------------------------------
// EnrichTuning
// ---------------------------------------------------------------------------

func TestEnrichTuning_NilSignals(t *testing.T) {
	action := &ScalingAction{}
	EnrichTuning(action, nil)
	if action.TuningParams != nil {
		t.Error("expected nil")
	}
}

func TestEnrichTuning_Empty(t *testing.T) {
	action := &ScalingAction{}
	EnrichTuning(action, &AdvancedSignals{})
	if action.TuningParams != nil {
		t.Error("expected nil")
	}
}

func TestEnrichTuning_SetsParams(t *testing.T) {
	action := &ScalingAction{}
	signals := &AdvancedSignals{
		TuningOverrides: map[string]string{"workers": "8", "cache_mb": "512"},
	}
	EnrichTuning(action, signals)
	if action.TuningParams["workers"] != "8" || action.TuningParams["cache_mb"] != "512" {
		t.Errorf("params=%v", action.TuningParams)
	}
}

func TestEnrichTuning_MergesWithExisting(t *testing.T) {
	action := &ScalingAction{TuningParams: map[string]string{"a": "1"}}
	signals := &AdvancedSignals{
		TuningOverrides: map[string]string{"b": "2"},
	}
	EnrichTuning(action, signals)
	if action.TuningParams["a"] != "1" || action.TuningParams["b"] != "2" {
		t.Errorf("params=%v", action.TuningParams)
	}
}

// ---------------------------------------------------------------------------
// CustomMetricScore
// ---------------------------------------------------------------------------

func TestCustomMetricScore_Nil(t *testing.T) {
	if s := CustomMetricScore(nil); s != 0 {
		t.Errorf("got %f", s)
	}
}

func TestCustomMetricScore_OnTarget(t *testing.T) {
	signals := &AdvancedSignals{
		CustomMetricResults: []metrics.CustomMetricResult{
			{Name: "a", Value: 100, Target: 100, Weight: 1},
		},
	}
	if s := CustomMetricScore(signals); s != 0 {
		t.Errorf("got %f", s)
	}
}

func TestCustomMetricScore_OffTarget(t *testing.T) {
	signals := &AdvancedSignals{
		CustomMetricResults: []metrics.CustomMetricResult{
			{Name: "a", Value: 120, Target: 100, Weight: 1},
		},
	}
	s := CustomMetricScore(signals)
	if math.Abs(s-0.2) > 1e-9 {
		t.Errorf("got %f, want 0.2", s)
	}
}

// ---------------------------------------------------------------------------
// StorageMonthlySavings / StorageHourlySavingsEstimate
// ---------------------------------------------------------------------------

func TestStorageMonthlySavings_Nil(t *testing.T) {
	if s := StorageMonthlySavings(nil); s != 0 {
		t.Errorf("got %f", s)
	}
}

func TestStorageMonthlySavings_Sums(t *testing.T) {
	signals := &AdvancedSignals{
		StorageRecommendations: []storage.Recommendation{
			{EstMonthlySavings: 10},
			{EstMonthlySavings: -5},
			{EstMonthlySavings: 20},
		},
	}
	if s := StorageMonthlySavings(signals); s != 25 {
		t.Errorf("got %f", s)
	}
}

func TestStorageHourlySavingsEstimate_Zero(t *testing.T) {
	if s := StorageHourlySavingsEstimate(nil); s != 0 {
		t.Errorf("got %f", s)
	}
}

func TestStorageHourlySavingsEstimate_Conversion(t *testing.T) {
	signals := &AdvancedSignals{
		StorageRecommendations: []storage.Recommendation{
			{EstMonthlySavings: 73},
		},
	}
	expected := 73.0 / 730.0
	if s := StorageHourlySavingsEstimate(signals); math.Abs(s-expected) > 1e-9 {
		t.Errorf("got %f, want %f", s, expected)
	}
}

// ---------------------------------------------------------------------------
// AdjustCostScore
// ---------------------------------------------------------------------------

func TestAdjustCostScore_NilSignals(t *testing.T) {
	if s := AdjustCostScore(0.5, 1.0, nil); s != 0.5 {
		t.Errorf("got %f", s)
	}
}

func TestAdjustCostScore_NoSavings(t *testing.T) {
	signals := &AdvancedSignals{}
	if s := AdjustCostScore(0.5, 1.0, signals); s != 0.5 {
		t.Errorf("got %f", s)
	}
}

func TestAdjustCostScore_WithSavings(t *testing.T) {
	signals := &AdvancedSignals{
		StorageRecommendations: []storage.Recommendation{
			{EstMonthlySavings: 730}, // $1/hr savings
		},
	}
	s := AdjustCostScore(0.5, 1.0, signals)
	// bonus = 1/(1+1) = 0.5, capped at 0.2 → 0.5 + 0.2 = 0.7
	if math.Abs(s-0.7) > 1e-9 {
		t.Errorf("got %f, want 0.7", s)
	}
}

func TestAdjustCostScore_SmallSavings(t *testing.T) {
	signals := &AdvancedSignals{
		StorageRecommendations: []storage.Recommendation{
			{EstMonthlySavings: 7.3}, // $0.01/hr savings
		},
	}
	s := AdjustCostScore(0.5, 1.0, signals)
	// bonus = 0.01/(0.01+1) ≈ 0.0099, < 0.2 → 0.5 + 0.0099 ≈ 0.51
	if s <= 0.5 || s > 0.52 {
		t.Errorf("got %f, expected ~0.51", s)
	}
}

func TestAdjustCostScore_CappedAt1(t *testing.T) {
	signals := &AdvancedSignals{
		StorageRecommendations: []storage.Recommendation{
			{EstMonthlySavings: 730},
		},
	}
	s := AdjustCostScore(0.95, 0.1, signals)
	if s > 1.0 {
		t.Errorf("got %f, should be capped at 1.0", s)
	}
}

func TestAdjustCostScore_ZeroCost(t *testing.T) {
	signals := &AdvancedSignals{
		StorageRecommendations: []storage.Recommendation{
			{EstMonthlySavings: 100},
		},
	}
	// zero hourly cost → no adjustment
	if s := AdjustCostScore(0.5, 0, signals); s != 0.5 {
		t.Errorf("got %f", s)
	}
}

// ---------------------------------------------------------------------------
// EnrichScoredCandidates
// ---------------------------------------------------------------------------

func TestEnrichScoredCandidates_NilSignals(t *testing.T) {
	scored := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 2, EstimatedCost: 1.0}, Score: CandidateScore{Cost: 0.5, Weighted: 0.8}, Viable: true},
	}
	EnrichScoredCandidates(scored, nil)
	if scored[0].Score.Cost != 0.5 || scored[0].Score.Weighted != 0.8 {
		t.Error("should be no-op")
	}
}

func TestEnrichScoredCandidates_StorageSavings(t *testing.T) {
	scored := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 2, EstimatedCost: 1.0}, Score: CandidateScore{Cost: 0.5, Weighted: 0.8}, Viable: true},
	}
	signals := &AdvancedSignals{
		StorageRecommendations: []storage.Recommendation{
			{EstMonthlySavings: 730},
		},
	}
	EnrichScoredCandidates(scored, signals)
	if scored[0].Score.Cost <= 0.5 {
		t.Errorf("cost should increase with savings, got %f", scored[0].Score.Cost)
	}
}

func TestEnrichScoredCandidates_CustomMetricPenalty(t *testing.T) {
	scored := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 2, EstimatedCost: 1.0}, Score: CandidateScore{Cost: 0.5, Weighted: 0.8}, Viable: true},
	}
	signals := &AdvancedSignals{
		CustomMetricResults: []metrics.CustomMetricResult{
			{Name: "a", Value: 200, Target: 100, Weight: 1}, // distance=1.0
		},
	}
	EnrichScoredCandidates(scored, signals)
	// penalty = 1.0 * 0.1 = 0.1, weighted = 0.8 - 0.1 = 0.7
	if math.Abs(scored[0].Score.Weighted-0.7) > 1e-9 {
		t.Errorf("weighted=%f, want 0.7", scored[0].Score.Weighted)
	}
}

func TestEnrichScoredCandidates_WeightedFloor(t *testing.T) {
	scored := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 2}, Score: CandidateScore{Weighted: 0.05}, Viable: true},
	}
	signals := &AdvancedSignals{
		CustomMetricResults: []metrics.CustomMetricResult{
			{Name: "a", Value: 1000, Target: 100, Weight: 1}, // huge distance
		},
	}
	EnrichScoredCandidates(scored, signals)
	if scored[0].Score.Weighted < 0 {
		t.Errorf("weighted=%f, should not be negative", scored[0].Score.Weighted)
	}
}

func TestEnrichScoredCandidates_Combined(t *testing.T) {
	scored := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 2, EstimatedCost: 1.0}, Score: CandidateScore{Cost: 0.5, Weighted: 0.8}, Viable: true},
		{Plan: cel.CandidatePlan{Replicas: 4, EstimatedCost: 2.0}, Score: CandidateScore{Cost: 0.3, Weighted: 0.6}, Viable: true},
	}
	signals := &AdvancedSignals{
		CustomMetricResults: []metrics.CustomMetricResult{
			{Name: "gpu", Value: 100, Target: 100, Weight: 1}, // on-target
		},
		StorageRecommendations: []storage.Recommendation{
			{EstMonthlySavings: 730},
		},
	}
	EnrichScoredCandidates(scored, signals)
	// On-target metrics → no penalty; storage saves bump cost.
	if scored[0].Score.Cost <= 0.5 {
		t.Error("expected cost improvement from storage savings")
	}
	if scored[0].Score.Weighted != 0.8 {
		t.Errorf("weighted should be unchanged when metrics on-target, got %f", scored[0].Score.Weighted)
	}
}

// ---------------------------------------------------------------------------
// AdvancedSignals zero-value safety
// ---------------------------------------------------------------------------

func TestAdvancedSignals_ZeroValue(t *testing.T) {
	var s AdvancedSignals
	input := &SolverInput{}
	action := &ScalingAction{}
	scored := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 1}, Score: CandidateScore{Weighted: 0.5}, Viable: true},
	}

	// All should be no-ops without panics.
	EnrichMetrics(input, &s)
	EnrichTuning(action, &s)
	EnrichScoredCandidates(scored, &s)

	if CustomMetricScore(&s) != 0 {
		t.Error("expected 0")
	}
	if StorageMonthlySavings(&s) != 0 {
		t.Error("expected 0")
	}
}
