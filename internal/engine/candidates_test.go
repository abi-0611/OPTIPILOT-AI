package engine

import (
	"testing"

	"github.com/optipilot-ai/optipilot/internal/cel"
)

func baseInput() *SolverInput {
	return &SolverInput{
		Namespace:     "default",
		Service:       "test-svc",
		InstanceTypes: []string{"m5.large"},
		Region:        "us-east-1",
		Current: cel.CurrentState{
			Replicas:      4,
			CPURequest:    0.5,
			MemoryRequest: 1.0,
			CPUUsage:      0.3,
			MemoryUsage:   0.5,
			SpotRatio:     0.0,
			HourlyCost:    0.384,
		},
	}
}

func TestGenerateCandidates_BasicCount(t *testing.T) {
	input := baseInput()
	candidates := GenerateCandidates(input, DefaultMaxCandidates)

	// 8 replica multipliers × 1 resource variant × 5 spot ratios = 40 max before dedup.
	// Some replica multipliers produce the same int (e.g., 4*0.9=3.6→4, same as 4*1.0=4).
	// After dedup, expect fewer. After pruning, may be fewer still.
	if len(candidates) < 10 {
		t.Errorf("expected at least 10 candidates, got %d", len(candidates))
	}
	if len(candidates) > 40 {
		t.Errorf("expected at most 40 candidates (before right-size), got %d", len(candidates))
	}
	t.Logf("Generated %d candidates from 4 replicas", len(candidates))
}

func TestGenerateCandidates_Dedup(t *testing.T) {
	// With 1 replica, many multipliers clamp to 1 (0.5→1, 0.75→1, 0.9→1, 1.0→1).
	// This tests deduplication.
	input := baseInput()
	input.Current.Replicas = 1

	candidates := GenerateCandidates(input, DefaultMaxCandidates)

	seen := make(map[candidateKey]struct{})
	for _, c := range candidates {
		key := candidateKey{Replicas: c.Replicas, CPURequest: c.CPURequest, MemoryRequest: c.MemoryRequest, SpotRatio: c.SpotRatio}
		if _, dup := seen[key]; dup {
			t.Errorf("duplicate candidate: replicas=%d cpu=%.3f mem=%.3f spot=%.1f", c.Replicas, c.CPURequest, c.MemoryRequest, c.SpotRatio)
		}
		seen[key] = struct{}{}
	}
	t.Logf("Generated %d unique candidates from 1 replica", len(candidates))
}

func TestGenerateCandidates_MinOneReplica(t *testing.T) {
	input := baseInput()
	input.Current.Replicas = 1

	candidates := GenerateCandidates(input, DefaultMaxCandidates)
	for _, c := range candidates {
		if c.Replicas < 1 {
			t.Errorf("candidate has %d replicas, expected >= 1", c.Replicas)
		}
	}
}

func TestGenerateCandidates_WithRightSizing(t *testing.T) {
	input := baseInput()
	rightCPU := 0.25
	rightMem := 0.5
	input.RightSizedCPU = &rightCPU
	input.RightSizedMemory = &rightMem

	candidates := GenerateCandidates(input, DefaultMaxCandidates)

	// Should see both current (0.5) and right-sized (0.25) CPU values.
	hasCurrent := false
	hasRightSized := false
	for _, c := range candidates {
		if c.CPURequest == 0.5 {
			hasCurrent = true
		}
		if c.CPURequest == 0.25 {
			hasRightSized = true
		}
	}
	if !hasCurrent {
		t.Error("expected candidates with current CPU (0.5)")
	}
	if !hasRightSized {
		t.Error("expected candidates with right-sized CPU (0.25)")
	}
	t.Logf("Generated %d candidates with right-sizing", len(candidates))
}

func TestGenerateCandidates_MemoryOnlyRightSizingVariant(t *testing.T) {
	input := baseInput()
	rightCPU := input.Current.CPURequest
	rightMem := 0.5
	input.RightSizedCPU = &rightCPU
	input.RightSizedMemory = &rightMem

	candidates := GenerateCandidates(input, DefaultMaxCandidates)

	hasCurrent := false
	hasRightSized := false
	for _, c := range candidates {
		if c.Replicas != input.Current.Replicas || c.SpotRatio != 0.0 {
			continue
		}
		if c.CPURequest == input.Current.CPURequest && c.MemoryRequest == input.Current.MemoryRequest {
			hasCurrent = true
		}
		if c.CPURequest == input.Current.CPURequest && c.MemoryRequest == rightMem {
			hasRightSized = true
		}
	}

	if !hasCurrent {
		t.Fatal("expected current resource candidate to be preserved")
	}
	if !hasRightSized {
		t.Fatal("expected memory-only right-sized candidate to be preserved")
	}
}

func TestGenerateCandidates_Cap(t *testing.T) {
	input := baseInput()
	input.Current.Replicas = 100 // Large current → all 8 multipliers produce unique values
	rightCPU := 0.25
	rightMem := 0.5
	input.RightSizedCPU = &rightCPU
	input.RightSizedMemory = &rightMem
	// 8 × 2 × 5 = 80 raw, but to force > cap, use a small cap.
	candidates := GenerateCandidates(input, 10)
	if len(candidates) > 10 {
		t.Errorf("expected at most 10 candidates after cap, got %d", len(candidates))
	}
	if len(candidates) < 1 {
		t.Error("expected at least 1 candidate after cap")
	}
}

func TestGenerateCandidates_CostEstimated(t *testing.T) {
	input := baseInput()
	candidates := GenerateCandidates(input, DefaultMaxCandidates)

	for _, c := range candidates {
		if c.EstimatedCost < 0 {
			t.Errorf("negative cost for candidate: %s", CandidateDebugString(c))
		}
		if c.EstimatedCarbon < 0 {
			t.Errorf("negative carbon for candidate: %s", CandidateDebugString(c))
		}
		// Spot candidates should be cheaper than all-on-demand with same replicas.
		if c.SpotRatio > 0 && c.SpotCount > 0 {
			allOnDemand := estimateCost(cel.CandidatePlan{
				Replicas:      c.Replicas,
				CPURequest:    c.CPURequest,
				MemoryRequest: c.MemoryRequest,
				OnDemandCount: c.Replicas,
				SpotCount:     0,
			}, "m5.large", "us-east-1")
			if c.EstimatedCost > allOnDemand+0.0001 {
				t.Errorf("spot candidate (spot=%.0f%%) cost $%.4f exceeds all-on-demand $%.4f",
					c.SpotRatio*100, c.EstimatedCost, allOnDemand)
			}
		}
	}
}

func TestEstimateCost_RightSizedRequestsCheaper(t *testing.T) {
	full := cel.CandidatePlan{
		Replicas:      1,
		CPURequest:    0.5,
		MemoryRequest: 1.0,
		OnDemandCount: 1,
	}
	rightSized := cel.CandidatePlan{
		Replicas:      1,
		CPURequest:    0.25,
		MemoryRequest: 0.5,
		OnDemandCount: 1,
	}

	fullCost := estimateCost(full, "m5.large", "us-east-1")
	rightSizedCost := estimateCost(rightSized, "m5.large", "us-east-1")

	if rightSizedCost >= fullCost {
		t.Fatalf("expected right-sized plan to cost less, got right-sized=$%.4f full=$%.4f", rightSizedCost, fullCost)
	}
}

func TestGenerateCandidates_SpotCountConsistency(t *testing.T) {
	input := baseInput()
	candidates := GenerateCandidates(input, DefaultMaxCandidates)

	for _, c := range candidates {
		if c.SpotCount+c.OnDemandCount != c.Replicas {
			t.Errorf("spot(%d) + on-demand(%d) != replicas(%d)",
				c.SpotCount, c.OnDemandCount, c.Replicas)
		}
	}
}

func TestGenerateCandidates_Pruning(t *testing.T) {
	// Verify that strictly dominated candidates are removed.
	// Create input where some candidates will definitely be dominated.
	input := baseInput()
	input.Current.Replicas = 10

	candidates := GenerateCandidates(input, DefaultMaxCandidates)

	// Verify no candidate in the result is dominated by another.
	for i, a := range candidates {
		for j, b := range candidates {
			if i == j {
				continue
			}
			if dominates(a, b) {
				t.Errorf("candidate %d dominates candidate %d — pruning missed it\n  A: %s\n  B: %s",
					i, j, CandidateDebugString(a), CandidateDebugString(b))
			}
		}
	}
}

func TestGenerateCandidates_ZeroReplicas(t *testing.T) {
	input := baseInput()
	input.Current.Replicas = 0 // edge case

	candidates := GenerateCandidates(input, DefaultMaxCandidates)
	for _, c := range candidates {
		if c.Replicas < 1 {
			t.Errorf("candidate has %d replicas, expected >= 1", c.Replicas)
		}
	}
	if len(candidates) == 0 {
		t.Error("expected at least some candidates even with 0 current replicas")
	}
}

func TestDominates(t *testing.T) {
	tests := []struct {
		name string
		a, b cel.CandidatePlan
		want bool
	}{
		{
			name: "b has higher replicas, cost, and spot — dominated",
			a:    cel.CandidatePlan{Replicas: 2, EstimatedCost: 0.1, SpotRatio: 0.0},
			b:    cel.CandidatePlan{Replicas: 4, EstimatedCost: 0.5, SpotRatio: 0.5},
			want: true,
		},
		{
			name: "b worse on two but equal spot — not dominated",
			a:    cel.CandidatePlan{Replicas: 2, EstimatedCost: 0.1, SpotRatio: 0.5},
			b:    cel.CandidatePlan{Replicas: 4, EstimatedCost: 0.5, SpotRatio: 0.5},
			want: false, // spot equal, not strictly worse
		},
		{
			name: "equal on all",
			a:    cel.CandidatePlan{Replicas: 2, EstimatedCost: 0.1, SpotRatio: 0.5},
			b:    cel.CandidatePlan{Replicas: 2, EstimatedCost: 0.1, SpotRatio: 0.5},
			want: false,
		},
		{
			name: "b better on cost — not dominated",
			a:    cel.CandidatePlan{Replicas: 2, EstimatedCost: 0.6, SpotRatio: 0.0},
			b:    cel.CandidatePlan{Replicas: 4, EstimatedCost: 0.5, SpotRatio: 0.5},
			want: false,
		},
		{
			name: "b has fewer replicas — not dominated",
			a:    cel.CandidatePlan{Replicas: 4, EstimatedCost: 0.5, SpotRatio: 0.5},
			b:    cel.CandidatePlan{Replicas: 2, EstimatedCost: 0.6, SpotRatio: 0.8},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dominates(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("dominates(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// ── Pre-warming + spot-reduction candidate tests ──────────────────────────────

func TestPreWarmingCandidates_Basic(t *testing.T) {
	input := baseInput()
	input.Current.Replicas = 10

	candidates := PreWarmingCandidates(input)

	// 10×1.3=13, 10×1.5=15 → 2 candidates
	if len(candidates) != 2 {
		t.Fatalf("expected 2 pre-warming candidates, got %d", len(candidates))
	}

	if candidates[0].Replicas != 13 {
		t.Errorf("first pre-warming candidate: replicas=%d, want 13", candidates[0].Replicas)
	}
	if candidates[1].Replicas != 15 {
		t.Errorf("second pre-warming candidate: replicas=%d, want 15", candidates[1].Replicas)
	}

	// Should preserve current CPU/memory
	for _, c := range candidates {
		if c.CPURequest != input.Current.CPURequest {
			t.Errorf("CPURequest=%f, want %f", c.CPURequest, input.Current.CPURequest)
		}
		if c.MemoryRequest != input.Current.MemoryRequest {
			t.Errorf("MemoryRequest=%f, want %f", c.MemoryRequest, input.Current.MemoryRequest)
		}
		if c.EstimatedCost <= 0 {
			t.Error("expected positive cost estimate")
		}
	}
}

func TestPreWarmingCandidates_SmallReplicas(t *testing.T) {
	input := baseInput()
	input.Current.Replicas = 1

	candidates := PreWarmingCandidates(input)

	// 1×1.3=1.3→1 (equal, skip), 1×1.5=1.5→2 → only 1 candidate
	if len(candidates) != 1 {
		t.Fatalf("expected 1 pre-warming candidate for 1 replica, got %d", len(candidates))
	}
	if candidates[0].Replicas != 2 {
		t.Errorf("replicas=%d, want 2", candidates[0].Replicas)
	}
}

func TestPreWarmingCandidates_ZeroReplicas(t *testing.T) {
	input := baseInput()
	input.Current.Replicas = 0

	candidates := PreWarmingCandidates(input)

	// Clamped to 1; 1×1.3→1 (skip), 1×1.5→2 → 1 candidate
	if len(candidates) != 1 {
		t.Fatalf("expected 1 pre-warming candidate, got %d", len(candidates))
	}
}

func TestPreWarmingCandidates_PreservesSpotRatio(t *testing.T) {
	input := baseInput()
	input.Current.Replicas = 10
	input.Current.SpotRatio = 0.5

	candidates := PreWarmingCandidates(input)
	for _, c := range candidates {
		if c.SpotRatio != 0.5 {
			t.Errorf("SpotRatio=%f, want 0.5", c.SpotRatio)
		}
		expectedSpot := int64(float64(c.Replicas)*0.5 + 0.5) // math.Round
		if c.SpotCount != expectedSpot {
			t.Errorf("SpotCount=%d, want %d for %d replicas", c.SpotCount, expectedSpot, c.Replicas)
		}
	}
}

func TestSpotReductionCandidates_HighSpotRatio(t *testing.T) {
	input := baseInput()
	input.Current.Replicas = 4
	input.Current.SpotRatio = 0.8

	candidates := SpotReductionCandidates(input)

	// spotReductionRatios = [0.0, 0.3] — both < 0.8 → 2 candidates
	if len(candidates) != 2 {
		t.Fatalf("expected 2 spot-reduction candidates, got %d", len(candidates))
	}

	for _, c := range candidates {
		if c.SpotRatio >= input.Current.SpotRatio {
			t.Errorf("spot ratio %f should be < current %f", c.SpotRatio, input.Current.SpotRatio)
		}
		if c.Replicas != input.Current.Replicas {
			t.Errorf("replicas=%d, want %d (unchanged)", c.Replicas, input.Current.Replicas)
		}
		if c.SpotCount+c.OnDemandCount != c.Replicas {
			t.Errorf("spot(%d) + od(%d) != replicas(%d)", c.SpotCount, c.OnDemandCount, c.Replicas)
		}
	}
}

func TestSpotReductionCandidates_LowSpotRatio(t *testing.T) {
	input := baseInput()
	input.Current.SpotRatio = 0.0 // already all on-demand

	candidates := SpotReductionCandidates(input)

	// No ratio is < 0.0, so no candidates
	if len(candidates) != 0 {
		t.Errorf("expected 0 spot-reduction candidates for 0%% spot, got %d", len(candidates))
	}
}

func TestSpotReductionCandidates_MidSpotRatio(t *testing.T) {
	input := baseInput()
	input.Current.SpotRatio = 0.3

	candidates := SpotReductionCandidates(input)

	// Only 0.0 is < 0.3 (0.3 is not strictly less) → 1 candidate
	if len(candidates) != 1 {
		t.Fatalf("expected 1 spot-reduction candidate, got %d", len(candidates))
	}
	if candidates[0].SpotRatio != 0.0 {
		t.Errorf("SpotRatio=%f, want 0.0", candidates[0].SpotRatio)
	}
}
