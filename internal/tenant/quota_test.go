package tenant

import (
	"strings"
	"testing"
)

// ── CheckQuota — cores ───────────────────────────────────────────────────────

func TestCheckQuota_CoresWithinLimit(t *testing.T) {
	s := &TenantState{CurrentCores: 8, MaxCores: 20}
	r := CheckQuota(s, ResourceDelta{AdditionalCores: 4})
	if !r.Allowed {
		t.Errorf("expected allowed, got: %s", r.Reason)
	}
}

func TestCheckQuota_CoresExactLimit(t *testing.T) {
	s := &TenantState{CurrentCores: 16, MaxCores: 20}
	r := CheckQuota(s, ResourceDelta{AdditionalCores: 4})
	if !r.Allowed {
		t.Errorf("exactly at limit should be allowed, got: %s", r.Reason)
	}
}

func TestCheckQuota_CoresExceeded(t *testing.T) {
	s := &TenantState{CurrentCores: 18, MaxCores: 20}
	r := CheckQuota(s, ResourceDelta{AdditionalCores: 5})
	if r.Allowed {
		t.Fatal("expected denied")
	}
	if !strings.Contains(r.Reason, "cores quota exceeded") {
		t.Errorf("unexpected reason: %s", r.Reason)
	}
}

func TestCheckQuota_CoresZeroLimitUnlimited(t *testing.T) {
	s := &TenantState{CurrentCores: 100, MaxCores: 0}
	r := CheckQuota(s, ResourceDelta{AdditionalCores: 999})
	if !r.Allowed {
		t.Errorf("MaxCores=0 should be unlimited, got: %s", r.Reason)
	}
}

// ── CheckQuota — memory ─────────────────────────────────────────────────────

func TestCheckQuota_MemoryWithinLimit(t *testing.T) {
	s := &TenantState{CurrentMemoryGiB: 32, MaxMemoryGiB: 128}
	r := CheckQuota(s, ResourceDelta{AdditionalMemoryGiB: 16})
	if !r.Allowed {
		t.Errorf("expected allowed, got: %s", r.Reason)
	}
}

func TestCheckQuota_MemoryExceeded(t *testing.T) {
	s := &TenantState{CurrentMemoryGiB: 120, MaxMemoryGiB: 128}
	r := CheckQuota(s, ResourceDelta{AdditionalMemoryGiB: 16})
	if r.Allowed {
		t.Fatal("expected denied")
	}
	if !strings.Contains(r.Reason, "memory quota exceeded") {
		t.Errorf("unexpected reason: %s", r.Reason)
	}
}

func TestCheckQuota_MemoryZeroLimitUnlimited(t *testing.T) {
	s := &TenantState{CurrentMemoryGiB: 999, MaxMemoryGiB: 0}
	r := CheckQuota(s, ResourceDelta{AdditionalMemoryGiB: 100})
	if !r.Allowed {
		t.Errorf("MaxMemoryGiB=0 should be unlimited, got: %s", r.Reason)
	}
}

// ── CheckQuota — cost ────────────────────────────────────────────────────────

func TestCheckQuota_CostWithinLimit(t *testing.T) {
	s := &TenantState{CurrentCostUSD: 800, MaxMonthlyCostUSD: 1000}
	r := CheckQuota(s, ResourceDelta{AdditionalCostUSD: 100})
	if !r.Allowed {
		t.Errorf("expected allowed, got: %s", r.Reason)
	}
}

func TestCheckQuota_CostExceeded(t *testing.T) {
	s := &TenantState{CurrentCostUSD: 950, MaxMonthlyCostUSD: 1000}
	r := CheckQuota(s, ResourceDelta{AdditionalCostUSD: 100})
	if r.Allowed {
		t.Fatal("expected denied")
	}
	if !strings.Contains(r.Reason, "cost quota exceeded") {
		t.Errorf("unexpected reason: %s", r.Reason)
	}
}

func TestCheckQuota_CostZeroLimitUnlimited(t *testing.T) {
	s := &TenantState{CurrentCostUSD: 999, MaxMonthlyCostUSD: 0}
	r := CheckQuota(s, ResourceDelta{AdditionalCostUSD: 99999})
	if !r.Allowed {
		t.Errorf("MaxMonthlyCostUSD=0 should be unlimited, got: %s", r.Reason)
	}
}

// ── CheckQuota — multi-dimension ────────────────────────────────────────────

func TestCheckQuota_AllLimitsOK(t *testing.T) {
	s := &TenantState{
		CurrentCores:      10,
		CurrentMemoryGiB:  32,
		CurrentCostUSD:    500,
		MaxCores:          50,
		MaxMemoryGiB:      128,
		MaxMonthlyCostUSD: 1000,
	}
	r := CheckQuota(s, ResourceDelta{
		AdditionalCores:     5,
		AdditionalMemoryGiB: 8,
		AdditionalCostUSD:   50,
	})
	if !r.Allowed {
		t.Errorf("all within limits, got: %s", r.Reason)
	}
}

func TestCheckQuota_CoresFailsFirst(t *testing.T) {
	// Cores would fail, memory and cost are fine.
	s := &TenantState{
		CurrentCores:      48,
		CurrentMemoryGiB:  10,
		CurrentCostUSD:    100,
		MaxCores:          50,
		MaxMemoryGiB:      128,
		MaxMonthlyCostUSD: 1000,
	}
	r := CheckQuota(s, ResourceDelta{
		AdditionalCores:     5,
		AdditionalMemoryGiB: 2,
		AdditionalCostUSD:   10,
	})
	if r.Allowed {
		t.Fatal("expected denied on cores")
	}
	if !strings.Contains(r.Reason, "cores") {
		t.Errorf("should fail on cores first: %s", r.Reason)
	}
}

func TestCheckQuota_MemoryFailsWhenCoresOK(t *testing.T) {
	s := &TenantState{
		CurrentCores:      10,
		CurrentMemoryGiB:  125,
		CurrentCostUSD:    100,
		MaxCores:          50,
		MaxMemoryGiB:      128,
		MaxMonthlyCostUSD: 1000,
	}
	r := CheckQuota(s, ResourceDelta{
		AdditionalCores:     2,
		AdditionalMemoryGiB: 8,
		AdditionalCostUSD:   10,
	})
	if r.Allowed {
		t.Fatal("expected denied on memory")
	}
	if !strings.Contains(r.Reason, "memory") {
		t.Errorf("should fail on memory: %s", r.Reason)
	}
}

func TestCheckQuota_CostFailsWhenOthersOK(t *testing.T) {
	s := &TenantState{
		CurrentCores:      10,
		CurrentMemoryGiB:  32,
		CurrentCostUSD:    980,
		MaxCores:          50,
		MaxMemoryGiB:      128,
		MaxMonthlyCostUSD: 1000,
	}
	r := CheckQuota(s, ResourceDelta{
		AdditionalCores:     2,
		AdditionalMemoryGiB: 4,
		AdditionalCostUSD:   50,
	})
	if r.Allowed {
		t.Fatal("expected denied on cost")
	}
	if !strings.Contains(r.Reason, "cost") {
		t.Errorf("should fail on cost: %s", r.Reason)
	}
}

// ── CheckQuota — edge cases ─────────────────────────────────────────────────

func TestCheckQuota_NilState(t *testing.T) {
	r := CheckQuota(nil, ResourceDelta{AdditionalCores: 1})
	if r.Allowed {
		t.Fatal("nil state should be denied")
	}
	if !strings.Contains(r.Reason, "nil") {
		t.Errorf("reason should mention nil: %s", r.Reason)
	}
}

func TestCheckQuota_ZeroDelta(t *testing.T) {
	s := &TenantState{CurrentCores: 50, MaxCores: 50}
	r := CheckQuota(s, ResourceDelta{})
	if !r.Allowed {
		t.Errorf("zero delta should be allowed: %s", r.Reason)
	}
}

func TestCheckQuota_NegativeDelta(t *testing.T) {
	// Scaling down should be fine even at limit.
	s := &TenantState{CurrentCores: 50, MaxCores: 50}
	r := CheckQuota(s, ResourceDelta{AdditionalCores: -10})
	if !r.Allowed {
		t.Errorf("negative delta (scale-down) should be allowed: %s", r.Reason)
	}
}

func TestCheckQuota_NoLimitsSet(t *testing.T) {
	s := &TenantState{
		CurrentCores:     100,
		CurrentMemoryGiB: 256,
		CurrentCostUSD:   9999,
	}
	r := CheckQuota(s, ResourceDelta{
		AdditionalCores:     100,
		AdditionalMemoryGiB: 256,
		AdditionalCostUSD:   9999,
	})
	if !r.Allowed {
		t.Errorf("no limits set should be unlimited: %s", r.Reason)
	}
}

func TestCheckQuota_ReasonContainsNumbers(t *testing.T) {
	s := &TenantState{CurrentCores: 18.5, MaxCores: 20}
	r := CheckQuota(s, ResourceDelta{AdditionalCores: 3})
	if r.Allowed {
		t.Fatal("expected denied")
	}
	// Reason should contain projected and limit values.
	if !strings.Contains(r.Reason, "21.5") {
		t.Errorf("reason should contain projected value: %s", r.Reason)
	}
	if !strings.Contains(r.Reason, "20") {
		t.Errorf("reason should contain limit: %s", r.Reason)
	}
}
