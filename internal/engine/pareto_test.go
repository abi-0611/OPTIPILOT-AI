package engine

import (
	"testing"

	"github.com/optipilot-ai/optipilot/internal/cel"
)

func TestFindParetoFront_TwoNonDominated(t *testing.T) {
	// 4 candidates:
	// A: SLO=1.0, Cost=0.2, Carbon=0.3, Fairness=1.0 (high SLO, low cost)
	// B: SLO=0.4, Cost=0.9, Carbon=0.8, Fairness=1.0 (low SLO, high cost/carbon)
	// C: SLO=0.8, Cost=0.5, Carbon=0.5, Fairness=0.7 (middle, worse fairness)
	// D: SLO=0.4, Cost=0.4, Carbon=0.4, Fairness=0.7 (dominated by C on all dims)
	//
	// A dominates nothing (cost/carbon low).
	// B dominates nothing (SLO low).
	// C dominates D (0.8>0.4, 0.5>0.4, 0.5>0.4, 0.7==0.7 — strict on 3).
	// No one dominates A (highest SLO).
	// No one dominates B (highest cost/carbon).
	// C is dominated by neither A nor B (tradeoffs).
	// Front = {A, B, C} — D is pruned.

	candidates := []ScoredCandidate{
		{Score: CandidateScore{SLO: 1.0, Cost: 0.2, Carbon: 0.3, Fairness: 1.0}},
		{Score: CandidateScore{SLO: 0.4, Cost: 0.9, Carbon: 0.8, Fairness: 1.0}},
		{Score: CandidateScore{SLO: 0.8, Cost: 0.5, Carbon: 0.5, Fairness: 0.7}},
		{Score: CandidateScore{SLO: 0.4, Cost: 0.4, Carbon: 0.4, Fairness: 0.7}},
	}

	front := FindParetoFront(candidates)
	if len(front) != 3 {
		t.Fatalf("expected 3 in Pareto front, got %d", len(front))
	}

	// Verify D (index 3) is not in the front.
	for _, f := range front {
		if f.Score.SLO == 0.4 && f.Score.Cost == 0.4 {
			t.Error("dominated candidate D should not be in the Pareto front")
		}
	}
}

func TestFindParetoFront_AllDominatedByOne(t *testing.T) {
	// One candidate is best on ALL dimensions.
	candidates := []ScoredCandidate{
		{Score: CandidateScore{SLO: 1.0, Cost: 1.0, Carbon: 1.0, Fairness: 1.0}},
		{Score: CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5}},
		{Score: CandidateScore{SLO: 0.3, Cost: 0.3, Carbon: 0.3, Fairness: 0.3}},
	}

	front := FindParetoFront(candidates)
	if len(front) != 1 {
		t.Fatalf("expected 1 in Pareto front, got %d", len(front))
	}
	if front[0].Score.SLO != 1.0 {
		t.Error("expected the dominant candidate in the front")
	}
}

func TestFindParetoFront_NoneDominated(t *testing.T) {
	// Each candidate is best on a different dimension — no dominance.
	candidates := []ScoredCandidate{
		{Score: CandidateScore{SLO: 1.0, Cost: 0.0, Carbon: 0.0, Fairness: 0.0}},
		{Score: CandidateScore{SLO: 0.0, Cost: 1.0, Carbon: 0.0, Fairness: 0.0}},
		{Score: CandidateScore{SLO: 0.0, Cost: 0.0, Carbon: 1.0, Fairness: 0.0}},
		{Score: CandidateScore{SLO: 0.0, Cost: 0.0, Carbon: 0.0, Fairness: 1.0}},
	}

	front := FindParetoFront(candidates)
	if len(front) != 4 {
		t.Fatalf("expected all 4 in Pareto front, got %d", len(front))
	}
}

func TestFindParetoFront_Empty(t *testing.T) {
	front := FindParetoFront(nil)
	if front != nil {
		t.Errorf("expected nil for empty input, got %d", len(front))
	}
}

func TestFindParetoFront_Single(t *testing.T) {
	candidates := []ScoredCandidate{
		{Score: CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5}},
	}
	front := FindParetoFront(candidates)
	if len(front) != 1 {
		t.Fatalf("expected 1, got %d", len(front))
	}
}

func TestFindParetoFront_AllEqual(t *testing.T) {
	// All identical scores → none dominates another → all in front.
	candidates := []ScoredCandidate{
		{Score: CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5}},
		{Score: CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5}},
		{Score: CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5}},
	}
	front := FindParetoFront(candidates)
	if len(front) != 3 {
		t.Fatalf("expected all 3 (equal scores, no dominance), got %d", len(front))
	}
}

func TestSelectBest_HighestWeighted(t *testing.T) {
	front := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 4}, Score: CandidateScore{Weighted: 0.7}},
		{Plan: cel.CandidatePlan{Replicas: 6}, Score: CandidateScore{Weighted: 0.9}},
		{Plan: cel.CandidatePlan{Replicas: 8}, Score: CandidateScore{Weighted: 0.5}},
	}

	current := cel.CurrentState{Replicas: 4}
	best := SelectBest(front, current)
	if best.Plan.Replicas != 6 {
		t.Errorf("expected replicas=6 (highest weighted), got %d", best.Plan.Replicas)
	}
}

func TestSelectBest_TieBreakByDisruption(t *testing.T) {
	// Two candidates with same weighted score; prefer the one closer to current state.
	front := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 10, CPURequest: 0.5, SpotRatio: 0.0}, Score: CandidateScore{Weighted: 0.8}},
		{Plan: cel.CandidatePlan{Replicas: 5, CPURequest: 0.5, SpotRatio: 0.0}, Score: CandidateScore{Weighted: 0.8}},
	}

	current := cel.CurrentState{Replicas: 4, CPURequest: 0.5, SpotRatio: 0.0}
	best := SelectBest(front, current)
	// replicas=5 is closer to current=4 than replicas=10.
	if best.Plan.Replicas != 5 {
		t.Errorf("expected replicas=5 (less disruptive), got %d", best.Plan.Replicas)
	}
}

func TestSelectBest_Single(t *testing.T) {
	front := []ScoredCandidate{
		{Plan: cel.CandidatePlan{Replicas: 3}, Score: CandidateScore{Weighted: 0.6}},
	}
	best := SelectBest(front, cel.CurrentState{Replicas: 3})
	if best.Plan.Replicas != 3 {
		t.Errorf("expected replicas=3, got %d", best.Plan.Replicas)
	}
}

func TestScoreDominates(t *testing.T) {
	tests := []struct {
		name string
		a, b CandidateScore
		want bool
	}{
		{
			name: "a strictly better on all",
			a:    CandidateScore{SLO: 1.0, Cost: 1.0, Carbon: 1.0, Fairness: 1.0},
			b:    CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5},
			want: true,
		},
		{
			name: "a better on 3, equal on 1",
			a:    CandidateScore{SLO: 1.0, Cost: 0.5, Carbon: 1.0, Fairness: 1.0},
			b:    CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5},
			want: true,
		},
		{
			name: "all equal — no dominance",
			a:    CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5},
			b:    CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5},
			want: false,
		},
		{
			name: "a worse on one dimension",
			a:    CandidateScore{SLO: 1.0, Cost: 0.3, Carbon: 1.0, Fairness: 1.0},
			b:    CandidateScore{SLO: 0.5, Cost: 0.5, Carbon: 0.5, Fairness: 0.5},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scoreDominates(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("scoreDominates = %v, want %v", got, tt.want)
			}
		})
	}
}
