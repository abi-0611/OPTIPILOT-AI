package tenant

import (
	"math"
	"testing"
)

func fairnessApprox(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// ── Perfect fairness ─────────────────────────────────────────────────────────

func TestFairness_PerfectEquality(t *testing.T) {
	// All tenants use exactly their guaranteed share → J=1.0.
	r := ComputeFairness([]FairnessInput{
		{Name: "a", CurrentCores: 10, GuaranteedCores: 10},
		{Name: "b", CurrentCores: 20, GuaranteedCores: 20},
		{Name: "c", CurrentCores: 5, GuaranteedCores: 5},
	})
	if r == nil {
		t.Fatal("expected non-nil result")
	}
	if !fairnessApprox(r.GlobalIndex, 1.0, 0.001) {
		t.Errorf("GlobalIndex=%f, want 1.0", r.GlobalIndex)
	}
	// All per-tenant scores should be 1.0.
	for name, score := range r.PerTenant {
		if !fairnessApprox(score, 1.0, 0.001) {
			t.Errorf("%s score=%f, want 1.0", name, score)
		}
	}
}

// ── Worst case (one gets all) ────────────────────────────────────────────────

func TestFairness_WorstCase_TwoTenants(t *testing.T) {
	// One tenant at 100%, other at 0% → J = 0.5 for 2 tenants.
	r := ComputeFairness([]FairnessInput{
		{Name: "hog", CurrentCores: 10, GuaranteedCores: 10},
		{Name: "starved", CurrentCores: 0, GuaranteedCores: 10},
	})
	if r == nil {
		t.Fatal("expected non-nil result")
	}
	// J = (1+0)^2 / (2 * (1+0)) = 1/2 = 0.5
	if !fairnessApprox(r.GlobalIndex, 0.5, 0.001) {
		t.Errorf("GlobalIndex=%f, want 0.5", r.GlobalIndex)
	}
}

func TestFairness_WorstCase_ThreeTenants(t *testing.T) {
	// One tenant at 100%, two at 0% → J = 1/3.
	r := ComputeFairness([]FairnessInput{
		{Name: "hog", CurrentCores: 10, GuaranteedCores: 10},
		{Name: "s1", CurrentCores: 0, GuaranteedCores: 10},
		{Name: "s2", CurrentCores: 0, GuaranteedCores: 10},
	})
	if !fairnessApprox(r.GlobalIndex, 1.0/3.0, 0.001) {
		t.Errorf("GlobalIndex=%f, want 0.333", r.GlobalIndex)
	}
}

// ── Proportional usage ───────────────────────────────────────────────────────

func TestFairness_UnevenButProportional(t *testing.T) {
	// All tenants using same fraction of guaranteed → J=1.0.
	// 50% of guaranteed for each.
	r := ComputeFairness([]FairnessInput{
		{Name: "a", CurrentCores: 5, GuaranteedCores: 10},
		{Name: "b", CurrentCores: 10, GuaranteedCores: 20},
		{Name: "c", CurrentCores: 2.5, GuaranteedCores: 5},
	})
	if !fairnessApprox(r.GlobalIndex, 1.0, 0.001) {
		t.Errorf("GlobalIndex=%f, want 1.0 (same ratio for all)", r.GlobalIndex)
	}
}

// ── Intermediate fairness ────────────────────────────────────────────────────

func TestFairness_IntermediateValue(t *testing.T) {
	// Gold: 15/15=1.0, Silver: 5/10=0.5, Bronze: 1/5=0.2
	// J = (1.0+0.5+0.2)^2 / (3 * (1.0+0.25+0.04)) = 2.89 / 3.87 ≈ 0.747
	r := ComputeFairness([]FairnessInput{
		{Name: "gold", CurrentCores: 15, GuaranteedCores: 15},
		{Name: "silver", CurrentCores: 5, GuaranteedCores: 10},
		{Name: "bronze", CurrentCores: 1, GuaranteedCores: 5},
	})
	expected := (1.7 * 1.7) / (3.0 * (1.0 + 0.25 + 0.04))
	if !fairnessApprox(r.GlobalIndex, expected, 0.001) {
		t.Errorf("GlobalIndex=%f, want %f", r.GlobalIndex, expected)
	}
}

// ── Bursting (over-allocation) ───────────────────────────────────────────────

func TestFairness_Bursting(t *testing.T) {
	// Tenant using 200% of guaranteed → xi=2.0.
	r := ComputeFairness([]FairnessInput{
		{Name: "burst", CurrentCores: 20, GuaranteedCores: 10},
		{Name: "normal", CurrentCores: 10, GuaranteedCores: 10},
	})
	// J = (2+1)^2 / (2*(4+1)) = 9/10 = 0.9
	if !fairnessApprox(r.GlobalIndex, 0.9, 0.001) {
		t.Errorf("GlobalIndex=%f, want 0.9", r.GlobalIndex)
	}
	if !fairnessApprox(r.PerTenant["burst"], 2.0, 0.001) {
		t.Errorf("burst score=%f, want 2.0", r.PerTenant["burst"])
	}
}

// ── Edge cases ───────────────────────────────────────────────────────────────

func TestFairness_SingleTenant(t *testing.T) {
	r := ComputeFairness([]FairnessInput{
		{Name: "solo", CurrentCores: 7, GuaranteedCores: 10},
	})
	// J(x) = x^2 / (1*x^2) = 1.0 for any single tenant.
	if !fairnessApprox(r.GlobalIndex, 1.0, 0.001) {
		t.Errorf("GlobalIndex=%f, want 1.0 for single tenant", r.GlobalIndex)
	}
}

func TestFairness_Empty(t *testing.T) {
	r := ComputeFairness(nil)
	if r != nil {
		t.Error("expected nil for empty input")
	}
}

func TestFairness_NoGuaranteed(t *testing.T) {
	// All tenants have 0 guaranteed → excluded.
	r := ComputeFairness([]FairnessInput{
		{Name: "a", CurrentCores: 5, GuaranteedCores: 0},
		{Name: "b", CurrentCores: 10, GuaranteedCores: 0},
	})
	if r != nil {
		t.Error("expected nil when no tenants have guaranteed>0")
	}
}

func TestFairness_MixedGuaranteed(t *testing.T) {
	// One with guaranteed, one without.
	r := ComputeFairness([]FairnessInput{
		{Name: "a", CurrentCores: 10, GuaranteedCores: 10},
		{Name: "b", CurrentCores: 5, GuaranteedCores: 0},
	})
	if r == nil {
		t.Fatal("expected non-nil (1 valid tenant)")
	}
	// Only 1 valid tenant → J=1.0.
	if !fairnessApprox(r.GlobalIndex, 1.0, 0.001) {
		t.Errorf("GlobalIndex=%f, want 1.0", r.GlobalIndex)
	}
	if _, ok := r.PerTenant["b"]; ok {
		t.Error("tenant b (no guaranteed) should be excluded")
	}
}

func TestFairness_AllZeroUsage(t *testing.T) {
	// All tenants at 0 usage but have guaranteed.
	r := ComputeFairness([]FairnessInput{
		{Name: "a", CurrentCores: 0, GuaranteedCores: 10},
		{Name: "b", CurrentCores: 0, GuaranteedCores: 20},
	})
	// xi = 0 for all → J = 0^2/(n*0) = 0/0, handled as 1.0 (vacuously fair).
	if !fairnessApprox(r.GlobalIndex, 1.0, 0.001) {
		t.Errorf("GlobalIndex=%f, want 1.0 (both at 0)", r.GlobalIndex)
	}
}

func TestFairness_PerTenantScores(t *testing.T) {
	r := ComputeFairness([]FairnessInput{
		{Name: "a", CurrentCores: 15, GuaranteedCores: 10}, // 1.5
		{Name: "b", CurrentCores: 5, GuaranteedCores: 10},  // 0.5
	})
	if !fairnessApprox(r.PerTenant["a"], 1.5, 0.001) {
		t.Errorf("a=%f, want 1.5", r.PerTenant["a"])
	}
	if !fairnessApprox(r.PerTenant["b"], 0.5, 0.001) {
		t.Errorf("b=%f, want 0.5", r.PerTenant["b"])
	}
}

// ── RecordFairnessMetrics (nil safety) ───────────────────────────────────────

func TestRecordFairnessMetrics_NilNoOp(t *testing.T) {
	// Should not panic.
	RecordFairnessMetrics(nil)
}

func TestRecordFairnessMetrics_SetsGauge(t *testing.T) {
	r := &FairnessResult{
		GlobalIndex: 0.85,
		PerTenant:   map[string]float64{"a": 1.0, "b": 0.7},
	}
	// Should not panic; actual gauge values validated via Prometheus API in integration.
	RecordFairnessMetrics(r)
}
