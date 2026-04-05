package tenant

import (
	"math"
	"testing"
)

func approxEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// ── Basic 3-Tenant Scenario (from spec) ──────────────────────────────────────
// 3 tenants, 100-core cluster
// Gold: weight 10, guaranteed 15%, burstable, maxBurst 40%
// Silver: weight 5, guaranteed 10%, burstable, maxBurst 30%
// Bronze: weight 3, guaranteed 5%, burstable, maxBurst 20%

func threeTenantsInputs() []FairShareInput {
	return []FairShareInput{
		{Name: "gold", Weight: 10, GuaranteedCoresPercent: 15, Burstable: true, MaxBurstPercent: 40},
		{Name: "silver", Weight: 5, GuaranteedCoresPercent: 10, Burstable: true, MaxBurstPercent: 30},
		{Name: "bronze", Weight: 3, GuaranteedCoresPercent: 5, Burstable: true, MaxBurstPercent: 20},
	}
}

func TestFairShare_ThreeTenants_Guaranteed(t *testing.T) {
	shares := ComputeFairShares(100, threeTenantsInputs())
	if len(shares) != 3 {
		t.Fatalf("len=%d, want 3", len(shares))
	}

	// Phase 1: guaranteed = 15, 10, 5
	if !approxEqual(shares[0].GuaranteedCores, 15, 0.01) {
		t.Errorf("gold guaranteed=%f, want 15", shares[0].GuaranteedCores)
	}
	if !approxEqual(shares[1].GuaranteedCores, 10, 0.01) {
		t.Errorf("silver guaranteed=%f, want 10", shares[1].GuaranteedCores)
	}
	if !approxEqual(shares[2].GuaranteedCores, 5, 0.01) {
		t.Errorf("bronze guaranteed=%f, want 5", shares[2].GuaranteedCores)
	}
}

func TestFairShare_ThreeTenants_BurstProportional(t *testing.T) {
	shares := ComputeFairShares(100, threeTenantsInputs())

	// Remaining = 100 - 30(guaranteed total) = 70
	// Gold burst raw = 70*10/18 = 38.89, Silver = 70*5/18 = 19.44, Bronze = 70*3/18 = 11.67
	// Gold total raw = 15+38.89 = 53.89 → cap at 40 → reclaim 13.89
	// Bronze total raw = 5+11.67 = 16.67 → cap at 20 → OK
	// Silver total raw = 10+19.44 = 29.44 → cap at 30 → OK

	// Verify gold is capped at 40.
	if shares[0].TotalCores > 40.01 {
		t.Errorf("gold total=%f, should be capped at 40", shares[0].TotalCores)
	}

	// Verify burst is > 0 for all.
	for _, s := range shares {
		if s.BurstCores < 0 {
			t.Errorf("%s burst=%f, should be >=0", s.Name, s.BurstCores)
		}
	}
}

func TestFairShare_ThreeTenants_CapEnforced(t *testing.T) {
	shares := ComputeFairShares(100, threeTenantsInputs())

	for _, s := range shares {
		if s.TotalCores > s.MaxCores+0.01 {
			t.Errorf("%s total=%f exceeds max=%f", s.Name, s.TotalCores, s.MaxCores)
		}
	}
}

func TestFairShare_ThreeTenants_TotalNotExceedCluster(t *testing.T) {
	shares := ComputeFairShares(100, threeTenantsInputs())
	total := 0.0
	for _, s := range shares {
		total += s.TotalCores
	}
	if total > 100.01 {
		t.Errorf("total allocated=%f > 100 cluster cores", total)
	}
}

func TestFairShare_ThreeTenants_ReclaimedRedistributed(t *testing.T) {
	shares := ComputeFairShares(100, threeTenantsInputs())

	// Gold is capped at 40, so reclaimed capacity goes to silver and bronze.
	// Silver and bronze should receive additional burst from the reclaimed amount.
	// Silver should receive more than bronze due to higher weight.
	silverBurst := shares[1].BurstCores
	bronzeBurst := shares[2].BurstCores

	// Silver weight(5) > bronze weight(3) → silver gets more burst.
	if silverBurst <= bronzeBurst {
		t.Errorf("silver burst=%f should > bronze burst=%f", silverBurst, bronzeBurst)
	}
}

// ── Edge Cases ────────────────────────────────────────────────────────────────

func TestFairShare_Empty(t *testing.T) {
	shares := ComputeFairShares(100, nil)
	if shares != nil {
		t.Error("expected nil for empty inputs")
	}
}

func TestFairShare_ZeroCluster(t *testing.T) {
	shares := ComputeFairShares(0, threeTenantsInputs())
	if shares != nil {
		t.Error("expected nil for 0 cluster cores")
	}
}

func TestFairShare_NegativeCluster(t *testing.T) {
	shares := ComputeFairShares(-10, threeTenantsInputs())
	if shares != nil {
		t.Error("expected nil for negative cluster cores")
	}
}

func TestFairShare_SingleTenant(t *testing.T) {
	shares := ComputeFairShares(100, []FairShareInput{
		{Name: "solo", Weight: 10, GuaranteedCoresPercent: 20, Burstable: true, MaxBurstPercent: 0},
	})

	if len(shares) != 1 {
		t.Fatalf("len=%d, want 1", len(shares))
	}
	s := shares[0]
	if !approxEqual(s.GuaranteedCores, 20, 0.01) {
		t.Errorf("guaranteed=%f, want 20", s.GuaranteedCores)
	}
	// All remaining capacity goes to solo tenant (no cap).
	if !approxEqual(s.TotalCores, 100, 0.01) {
		t.Errorf("total=%f, want 100 (no cap)", s.TotalCores)
	}
	if s.MaxCores != 100 {
		t.Errorf("maxCores=%f, want 100 (no cap)", s.MaxCores)
	}
}

func TestFairShare_SingleTenantWithCap(t *testing.T) {
	shares := ComputeFairShares(100, []FairShareInput{
		{Name: "solo", Weight: 10, GuaranteedCoresPercent: 20, Burstable: true, MaxBurstPercent: 50},
	})

	s := shares[0]
	if !approxEqual(s.TotalCores, 50, 0.01) {
		t.Errorf("total=%f, want 50 (capped)", s.TotalCores)
	}
}

func TestFairShare_NonBurstableTenant(t *testing.T) {
	shares := ComputeFairShares(100, []FairShareInput{
		{Name: "a", Weight: 10, GuaranteedCoresPercent: 20, Burstable: false, MaxBurstPercent: 0},
		{Name: "b", Weight: 10, GuaranteedCoresPercent: 20, Burstable: true, MaxBurstPercent: 0},
	})

	// "a" is non-burstable → gets guaranteed only, no burst.
	if !approxEqual(shares[0].BurstCores, 0, 0.01) {
		t.Errorf("a burst=%f, want 0 (non-burstable)", shares[0].BurstCores)
	}
	if !approxEqual(shares[0].TotalCores, 20, 0.01) {
		t.Errorf("a total=%f, want 20", shares[0].TotalCores)
	}

	// "b" gets all remaining burst (100-40=60).
	if !approxEqual(shares[1].BurstCores, 60, 0.01) {
		t.Errorf("b burst=%f, want 60", shares[1].BurstCores)
	}
}

func TestFairShare_AllNonBurstable(t *testing.T) {
	shares := ComputeFairShares(100, []FairShareInput{
		{Name: "a", Weight: 10, GuaranteedCoresPercent: 30, Burstable: false},
		{Name: "b", Weight: 5, GuaranteedCoresPercent: 20, Burstable: false},
	})

	// No burst for anyone. 50 cores remain unallocated.
	for _, s := range shares {
		if s.BurstCores != 0 {
			t.Errorf("%s burst=%f, want 0", s.Name, s.BurstCores)
		}
	}
	if !approxEqual(shares[0].TotalCores, 30, 0.01) {
		t.Errorf("a total=%f, want 30", shares[0].TotalCores)
	}
}

func TestFairShare_OversubscribedGuarantee(t *testing.T) {
	// Guarantees exceed cluster (70+40=110 > 100).
	shares := ComputeFairShares(100, []FairShareInput{
		{Name: "a", Weight: 10, GuaranteedCoresPercent: 70, Burstable: true, MaxBurstPercent: 0},
		{Name: "b", Weight: 10, GuaranteedCoresPercent: 40, Burstable: true, MaxBurstPercent: 0},
	})

	// Guarantees are still computed from %, even if oversubscribed.
	if !approxEqual(shares[0].GuaranteedCores, 70, 0.01) {
		t.Errorf("a guaranteed=%f, want 70", shares[0].GuaranteedCores)
	}
	if !approxEqual(shares[1].GuaranteedCores, 40, 0.01) {
		t.Errorf("b guaranteed=%f, want 40", shares[1].GuaranteedCores)
	}
	// No remaining for burst.
	if shares[0].BurstCores != 0 {
		t.Errorf("a burst=%f, want 0 (oversubscribed)", shares[0].BurstCores)
	}
}

func TestFairShare_ZeroWeightTenant(t *testing.T) {
	shares := ComputeFairShares(100, []FairShareInput{
		{Name: "a", Weight: 0, GuaranteedCoresPercent: 10, Burstable: true, MaxBurstPercent: 0},
		{Name: "b", Weight: 10, GuaranteedCoresPercent: 10, Burstable: true, MaxBurstPercent: 0},
	})

	// "a" has weight 0 → still gets guaranteed but no burst share.
	if !approxEqual(shares[0].BurstCores, 0, 0.01) {
		t.Errorf("a burst=%f, want 0 (weight 0)", shares[0].BurstCores)
	}
	// "b" gets all burst.
	if !approxEqual(shares[1].BurstCores, 80, 0.01) {
		t.Errorf("b burst=%f, want 80", shares[1].BurstCores)
	}
}

func TestFairShare_ZeroGuarantee(t *testing.T) {
	shares := ComputeFairShares(100, []FairShareInput{
		{Name: "a", Weight: 10, GuaranteedCoresPercent: 0, Burstable: true, MaxBurstPercent: 50},
	})

	if shares[0].GuaranteedCores != 0 {
		t.Errorf("guaranteed=%f, want 0", shares[0].GuaranteedCores)
	}
	// All 100 cores available for burst, capped at 50.
	if !approxEqual(shares[0].TotalCores, 50, 0.01) {
		t.Errorf("total=%f, want 50", shares[0].TotalCores)
	}
}

func TestFairShare_MaxCoresReflectsCap(t *testing.T) {
	shares := ComputeFairShares(100, []FairShareInput{
		{Name: "capped", Weight: 10, GuaranteedCoresPercent: 10, Burstable: true, MaxBurstPercent: 30},
		{Name: "uncapped", Weight: 10, GuaranteedCoresPercent: 10, Burstable: true, MaxBurstPercent: 0},
	})

	if !approxEqual(shares[0].MaxCores, 30, 0.01) {
		t.Errorf("capped MaxCores=%f, want 30", shares[0].MaxCores)
	}
	if !approxEqual(shares[1].MaxCores, 100, 0.01) {
		t.Errorf("uncapped MaxCores=%f, want 100 (full cluster)", shares[1].MaxCores)
	}
}

func TestFairShare_LargeCluster(t *testing.T) {
	shares := ComputeFairShares(10000, []FairShareInput{
		{Name: "a", Weight: 100, GuaranteedCoresPercent: 5, Burstable: true, MaxBurstPercent: 0},
		{Name: "b", Weight: 50, GuaranteedCoresPercent: 5, Burstable: true, MaxBurstPercent: 0},
		{Name: "c", Weight: 25, GuaranteedCoresPercent: 5, Burstable: true, MaxBurstPercent: 0},
	})

	total := 0.0
	for _, s := range shares {
		total += s.TotalCores
	}
	if !approxEqual(total, 10000, 0.01) {
		t.Errorf("total=%f, want 10000", total)
	}
}

// ── AllocationStatusFor ──────────────────────────────────────────────────────

func TestAllocationStatus_Guaranteed(t *testing.T) {
	s := ResourceShare{GuaranteedCores: 10, TotalCores: 20}
	status := AllocationStatusFor(9.0, s) // 90% of guaranteed
	if status != "guaranteed" {
		t.Errorf("status=%s, want guaranteed", status)
	}
}

func TestAllocationStatus_Bursting(t *testing.T) {
	s := ResourceShare{GuaranteedCores: 10, TotalCores: 20}
	status := AllocationStatusFor(15.0, s) // above guaranteed, under total
	if status != "bursting" {
		t.Errorf("status=%s, want bursting", status)
	}
}

func TestAllocationStatus_Throttled(t *testing.T) {
	s := ResourceShare{GuaranteedCores: 10, TotalCores: 20}
	status := AllocationStatusFor(25.0, s) // above total
	if status != "throttled" {
		t.Errorf("status=%s, want throttled", status)
	}
}

func TestAllocationStatus_UnderAllocated(t *testing.T) {
	s := ResourceShare{GuaranteedCores: 10, TotalCores: 20}
	status := AllocationStatusFor(5.0, s) // 50% of guaranteed
	if status != "under_allocated" {
		t.Errorf("status=%s, want under_allocated", status)
	}
}

func TestAllocationStatus_ZeroGuarantee(t *testing.T) {
	s := ResourceShare{GuaranteedCores: 0, TotalCores: 10}
	if AllocationStatusFor(5, s) != "bursting" {
		t.Error("expected bursting when guaranteed=0 but using cores")
	}
	if AllocationStatusFor(0, s) != "under_allocated" {
		t.Error("expected under_allocated when guaranteed=0 and no usage")
	}
}

func TestAllocationStatus_ExactGuarantee(t *testing.T) {
	s := ResourceShare{GuaranteedCores: 10, TotalCores: 20}
	status := AllocationStatusFor(10.0, s)
	if status != "guaranteed" {
		t.Errorf("status=%s, want guaranteed at exactly 100%%", status)
	}
}
