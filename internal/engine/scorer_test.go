package engine

import (
	"math"
	"testing"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name          string
		value, mi, ma float64
		want          float64
	}{
		{"mid", 5, 0, 10, 0.5},
		{"min", 0, 0, 10, 0.0},
		{"max", 10, 0, 10, 1.0},
		{"equal bounds", 5, 5, 5, 0.0},
		{"below min", -1, 0, 10, 0.0},
		{"above max", 15, 0, 10, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalize(tt.value, tt.mi, tt.ma)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("normalize(%.1f, %.1f, %.1f) = %.6f, want %.6f", tt.value, tt.mi, tt.ma, got, tt.want)
			}
		})
	}
}

func TestScoreSLO_CapacityRatio(t *testing.T) {
	// current demand = CPUUsage = 2.0 cores
	input := &SolverInput{
		Current: cel.CurrentState{
			Replicas:   4,
			CPURequest: 0.5,
			CPUUsage:   2.0,
		},
	}
	scorer := NewScorer(input, nil)

	tests := []struct {
		name     string
		replicas int64
		cpu      float64
		want     float64
	}{
		// capacity = 6 * 0.5 = 3.0; ratio = 3.0/2.0 = 1.5 >= 1.2 → 1.0
		{"20% headroom", 6, 0.5, 1.0},
		// capacity = 4 * 0.5 = 2.0; ratio = 2.0/2.0 = 1.0 → 0.8
		{"exact match", 4, 0.5, 0.8},
		// capacity = 3 * 0.5 = 1.5; ratio = 1.5/2.0 = 0.75 < 0.8 → 0.0
		{"under capacity", 3, 0.5, 0.0},
		// capacity = 4 * 0.45 = 1.8; ratio = 1.8/2.0 = 0.9 >= 0.8 → 0.4
		{"tight capacity", 4, 0.45, 0.4},
		// capacity = 5 * 0.5 = 2.5; ratio = 2.5/2.0 = 1.25 >= 1.2 → 1.0
		{"just over 1.2", 5, 0.5, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := cel.CandidatePlan{Replicas: tt.replicas, CPURequest: tt.cpu}
			got := scorer.scoreSLO(plan)
			if got != tt.want {
				t.Errorf("scoreSLO(replicas=%d, cpu=%.2f) = %.1f, want %.1f", tt.replicas, tt.cpu, got, tt.want)
			}
		})
	}
}

func TestScoreSLO_WithForecast(t *testing.T) {
	// Current CPUUsage = 2.0, forecast says +50% → effective demand = 3.0
	input := &SolverInput{
		Current: cel.CurrentState{CPUUsage: 2.0},
		Forecast: &cel.ForecastResult{
			PredictedRPS:  100,
			ChangePercent: 50, // +50%
		},
	}
	scorer := NewScorer(input, nil)

	// demand = 2.0 * 1.5 = 3.0
	// capacity = 6 * 0.5 = 3.0; ratio = 3.0/3.0 = 1.0 → 0.8
	plan := cel.CandidatePlan{Replicas: 6, CPURequest: 0.5}
	got := scorer.scoreSLO(plan)
	if got != 0.8 {
		t.Errorf("scoreSLO with forecast: got %.1f, want 0.8", got)
	}

	// capacity = 8 * 0.5 = 4.0; ratio = 4.0/3.0 = 1.33 >= 1.2 → 1.0
	plan2 := cel.CandidatePlan{Replicas: 8, CPURequest: 0.5}
	got2 := scorer.scoreSLO(plan2)
	if got2 != 1.0 {
		t.Errorf("scoreSLO with forecast (headroom): got %.1f, want 1.0", got2)
	}
}

func TestScoreSLO_NonCompliantPrefersScaleUp(t *testing.T) {
	input := &SolverInput{
		Current: cel.CurrentState{
			Replicas:   1,
			CPURequest: 0.1,
			CPUUsage:   0.01,
		},
		SLO: cel.SLOStatus{
			Compliant: false,
			BurnRate:  25,
		},
	}
	scorer := NewScorer(input, nil)

	down := scorer.scoreSLO(cel.CandidatePlan{Replicas: 1, CPURequest: 0.1})
	up := scorer.scoreSLO(cel.CandidatePlan{Replicas: 2, CPURequest: 0.1})

	if up <= down {
		t.Fatalf("expected scale-up candidate to outscore current candidate when SLO is degraded, got up=%.2f current=%.2f", up, down)
	}

	downscale := scorer.scoreSLO(cel.CandidatePlan{Replicas: 0, CPURequest: 0.1})
	if downscale != 0.0 {
		t.Fatalf("expected downscale candidate to score 0 when SLO is degraded, got %.2f", downscale)
	}
}

func TestScoreCostNormalization(t *testing.T) {
	// Three candidates with costs: 1.0, 2.0, 3.0
	// min=1.0, max=3.0
	// scoreCost(1.0) = 1 - 0/2 = 1.0  (cheapest)
	// scoreCost(2.0) = 1 - 1/2 = 0.5
	// scoreCost(3.0) = 1 - 2/2 = 0.0  (most expensive)
	tests := []struct {
		cost     float64
		wantCost float64
	}{
		{1.0, 1.0},
		{2.0, 0.5},
		{3.0, 0.0},
	}

	for _, tt := range tests {
		got := scoreCost(tt.cost, 1.0, 3.0)
		if math.Abs(got-tt.wantCost) > 1e-9 {
			t.Errorf("scoreCost(%.1f, 1.0, 3.0) = %.6f, want %.6f", tt.cost, got, tt.wantCost)
		}
	}
}

func TestScoreCarbonNormalization(t *testing.T) {
	// Same normalization logic as cost.
	got := scoreCarbon(50, 50, 150)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("scoreCarbon(50, 50, 150) = %.6f, want 1.0", got)
	}
	got2 := scoreCarbon(100, 50, 150)
	if math.Abs(got2-0.5) > 1e-9 {
		t.Errorf("scoreCarbon(100, 50, 150) = %.6f, want 0.5", got2)
	}
}

func TestScoreFairness(t *testing.T) {
	tests := []struct {
		name      string
		tenant    *cel.TenantStatus
		candidate cel.CandidatePlan
		current   cel.CurrentState
		wantScore float64
	}{
		{
			name:      "no tenant",
			tenant:    nil,
			candidate: cel.CandidatePlan{Replicas: 4, CPURequest: 0.5},
			current:   cel.CurrentState{Replicas: 4, CPURequest: 0.5},
			wantScore: 1.0,
		},
		{
			name: "within guaranteed share",
			tenant: &cel.TenantStatus{
				CurrentCores:    2.0,
				GuaranteedCores: 5.0,
				MaxCores:        10.0,
			},
			candidate: cel.CandidatePlan{Replicas: 4, CPURequest: 0.5}, // 2.0 cores
			current:   cel.CurrentState{Replicas: 4, CPURequest: 0.5},  // delta = 0
			wantScore: 1.0,                                             // 2.0 + 0 <= 5.0
		},
		{
			name: "between guaranteed and max (burst)",
			tenant: &cel.TenantStatus{
				CurrentCores:    4.0,
				GuaranteedCores: 3.0,
				MaxCores:        8.0,
			},
			candidate: cel.CandidatePlan{Replicas: 6, CPURequest: 0.5}, // 3.0 cores
			current:   cel.CurrentState{Replicas: 4, CPURequest: 0.5},  // 2.0 cores, delta=+1.0
			wantScore: 0.7,                                             // 4.0 + 1.0 = 5.0 > 3.0 but <= 8.0
		},
		{
			name: "exceeds max burst",
			tenant: &cel.TenantStatus{
				CurrentCores:    8.0,
				GuaranteedCores: 3.0,
				MaxCores:        10.0,
			},
			candidate: cel.CandidatePlan{Replicas: 10, CPURequest: 0.5}, // 5.0 cores
			current:   cel.CurrentState{Replicas: 4, CPURequest: 0.5},   // 2.0 cores, delta=+3.0
			wantScore: 0.2,                                              // 8.0 + 3.0 = 11.0 > 10.0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &SolverInput{
				Current: tt.current,
				Tenant:  tt.tenant,
			}
			scorer := NewScorer(input, nil)
			got := scorer.scoreFairness(tt.candidate)
			if got != tt.wantScore {
				t.Errorf("scoreFairness() = %.1f, want %.1f", got, tt.wantScore)
			}
		})
	}
}

func TestScoreAll_HandComputed(t *testing.T) {
	// Hand-computed example with 3 candidates.
	// current demand (CPUUsage) = 2.0 cores.
	input := &SolverInput{
		Current: cel.CurrentState{
			Replicas:      4,
			CPURequest:    0.5,
			CPUUsage:      2.0,
			MemoryRequest: 1.0,
		},
		Region:        "us-east-1",
		InstanceTypes: []string{"m5.large"},
	}

	objectives := []policyv1alpha1.PolicyObjective{
		{Name: "slo", Weight: 0.4, Direction: policyv1alpha1.DirectionMaximize},
		{Name: "cost", Weight: 0.3, Direction: policyv1alpha1.DirectionMinimize},
		{Name: "carbon", Weight: 0.2, Direction: policyv1alpha1.DirectionMinimize},
		{Name: "fairness", Weight: 0.1, Direction: policyv1alpha1.DirectionMaximize},
	}

	candidates := []cel.CandidatePlan{
		{Replicas: 2, CPURequest: 0.5, SpotRatio: 0.0, OnDemandCount: 2, SpotCount: 0,
			EstimatedCost: 0.192, EstimatedCarbon: 87.552},
		{Replicas: 4, CPURequest: 0.5, SpotRatio: 0.0, OnDemandCount: 4, SpotCount: 0,
			EstimatedCost: 0.384, EstimatedCarbon: 175.104},
		{Replicas: 6, CPURequest: 0.5, SpotRatio: 0.5, OnDemandCount: 3, SpotCount: 3,
			EstimatedCost: 0.3744, EstimatedCarbon: 170.726},
	}

	scorer := NewScorer(input, objectives)
	scored := scorer.ScoreAll(candidates)

	if len(scored) != 3 {
		t.Fatalf("expected 3 scored candidates, got %d", len(scored))
	}

	// SLO scores:
	// candidate 0: capacity = 2*0.5 = 1.0, ratio = 1.0/2.0 = 0.5 < 0.8 → 0.0
	// candidate 1: capacity = 4*0.5 = 2.0, ratio = 2.0/2.0 = 1.0 → 0.8
	// candidate 2: capacity = 6*0.5 = 3.0, ratio = 3.0/2.0 = 1.5 >= 1.2 → 1.0
	assertScore(t, "SLO[0]", scored[0].Score.SLO, 0.0)
	assertScore(t, "SLO[1]", scored[1].Score.SLO, 0.8)
	assertScore(t, "SLO[2]", scored[2].Score.SLO, 1.0)

	// Cost normalization: min=0.192, max=0.384
	// cost[0] = 1 - (0.192-0.192)/(0.384-0.192) = 1 - 0 = 1.0
	// cost[1] = 1 - (0.384-0.192)/(0.384-0.192) = 1 - 1 = 0.0
	// cost[2] = 1 - (0.3744-0.192)/(0.384-0.192) = 1 - 0.95 = 0.05
	assertScore(t, "Cost[0]", scored[0].Score.Cost, 1.0)
	assertScore(t, "Cost[1]", scored[1].Score.Cost, 0.0)
	assertScoreApprox(t, "Cost[2]", scored[2].Score.Cost, 0.05, 0.01)

	// Fairness: no tenant → all 1.0
	assertScore(t, "Fairness[0]", scored[0].Score.Fairness, 1.0)
	assertScore(t, "Fairness[1]", scored[1].Score.Fairness, 1.0)
	assertScore(t, "Fairness[2]", scored[2].Score.Fairness, 1.0)

	// All scores should be in [0, 1].
	for i, sc := range scored {
		for _, v := range []float64{sc.Score.SLO, sc.Score.Cost, sc.Score.Carbon, sc.Score.Fairness} {
			if v < 0 || v > 1.0 {
				t.Errorf("candidate %d has out-of-range score: %+v", i, sc.Score)
			}
		}
	}

	// Weighted score: with weights [slo=0.4, cost=0.3, carbon=0.2, fairness=0.1]
	// candidate 2 should have highest weighted score (best SLO, decent cost/carbon).
	if scored[2].Score.Weighted <= scored[1].Score.Weighted {
		t.Errorf("expected candidate 2 (scaled up) to have higher weighted score than candidate 1; got %.4f vs %.4f",
			scored[2].Score.Weighted, scored[1].Score.Weighted)
	}
}

func TestScoreAll_EqualCandidates(t *testing.T) {
	input := &SolverInput{
		Current: cel.CurrentState{CPUUsage: 1.0, Replicas: 2, CPURequest: 0.5},
	}

	candidates := []cel.CandidatePlan{
		{Replicas: 2, CPURequest: 0.5, EstimatedCost: 0.192, EstimatedCarbon: 87.0},
		{Replicas: 2, CPURequest: 0.5, EstimatedCost: 0.192, EstimatedCarbon: 87.0},
	}

	scorer := NewScorer(input, nil)
	scored := scorer.ScoreAll(candidates)

	// When all equal, cost/carbon normalization returns 0 (min==max), so scores = 1-0 = 1.0
	if scored[0].Score.Cost != scored[1].Score.Cost {
		t.Errorf("equal candidates should have equal cost scores: %.4f vs %.4f",
			scored[0].Score.Cost, scored[1].Score.Cost)
	}
}

func TestScoreAll_EmptyCandidates(t *testing.T) {
	input := &SolverInput{}
	scorer := NewScorer(input, nil)
	scored := scorer.ScoreAll(nil)
	if scored != nil {
		t.Errorf("expected nil for empty candidates, got %d", len(scored))
	}
}

func TestScoreAll_DefaultWeights(t *testing.T) {
	// No objectives → equal weight to all 4 dimensions.
	input := &SolverInput{
		Current: cel.CurrentState{CPUUsage: 1.0, Replicas: 2, CPURequest: 0.5},
	}
	candidates := []cel.CandidatePlan{
		{Replicas: 3, CPURequest: 0.5, EstimatedCost: 0.2, EstimatedCarbon: 50},
	}

	scorer := NewScorer(input, nil)
	scored := scorer.ScoreAll(candidates)

	// weighted = (SLO + Cost + Carbon + Fairness) / 4
	expected := (scored[0].Score.SLO + scored[0].Score.Cost + scored[0].Score.Carbon + scored[0].Score.Fairness) / 4.0
	if math.Abs(scored[0].Score.Weighted-expected) > 1e-9 {
		t.Errorf("default weighted = %.6f, expected avg = %.6f", scored[0].Score.Weighted, expected)
	}
}

func assertScore(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %.6f, want %.6f", label, got, want)
	}
}

func assertScoreApprox(t *testing.T, label string, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s = %.6f, want %.6f (±%.4f)", label, got, want, tolerance)
	}
}
