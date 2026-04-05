package tenant

import (
	"context"
	"testing"
	"time"

	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
)

// ─────────────────────────────────────────────────────────────────────────────
// Phase 7 Integration Tests
//
// Wires all Phase 7 components in a realistic 3-tenant scenario:
//   Manager → FairShare → Quota → FairnessIndex → NoisyNeighborDetector
//
// Fixtures: gold (tier gold, weight 10, guaranteed 15%, cap 40%)
//           silver (tier silver, weight 5, guaranteed 10%, cap 25%)
//           bronze (tier bronze, weight 3, guaranteed 5%, cap 15%)
//           100-core cluster
// ─────────────────────────────────────────────────────────────────────────────

const integClusterCores = 100.0

// integSetup creates a Manager with mock Prometheus pre-loaded with 3 profiles.
func integSetup(t *testing.T) (*Manager, *mockProm, *fakeClock) {
	t.Helper()
	clk := &fakeClock{now: time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)}
	prom := newMockProm()
	mgr := NewManager(prom, WithClock(clk), WithRefreshInterval(30*time.Second))
	mgr.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfileFull("gold", tenantv1alpha1.TierGold, 10,
			[]string{"gold-ns1", "gold-ns2"},
			50, 100, "5000",
			15, true, 40,
		),
		makeProfileFull("silver", tenantv1alpha1.TierSilver, 5,
			[]string{"silver-ns"},
			30, 60, "2000",
			10, true, 25,
		),
		makeProfileFull("bronze", tenantv1alpha1.TierBronze, 3,
			[]string{"bronze-ns"},
			20, 30, "1000",
			5, true, 15,
		),
	})
	return mgr, prom, clk
}

// integRegisterUsage sets mock Prometheus responses for the 3-tenant fixture.
func integRegisterUsage(prom *mockProm, goldCores, silverCores, bronzeCores float64) {
	const gib = 1024 * 1024 * 1024
	prom.SetResult(`sum(namespace_cpu_usage_seconds_total{namespace=~"gold-ns1|gold-ns2"})`, goldCores)
	prom.SetResult(`sum(namespace_memory_working_set_bytes{namespace=~"gold-ns1|gold-ns2"})`, 40*gib)
	prom.SetResult(`sum(namespace_cost_per_hour{namespace=~"gold-ns1|gold-ns2"})`, 5.0)

	prom.SetResult(`sum(namespace_cpu_usage_seconds_total{namespace=~"silver-ns"})`, silverCores)
	prom.SetResult(`sum(namespace_memory_working_set_bytes{namespace=~"silver-ns"})`, 16*gib)
	prom.SetResult(`sum(namespace_cost_per_hour{namespace=~"silver-ns"})`, 2.0)

	prom.SetResult(`sum(namespace_cpu_usage_seconds_total{namespace=~"bronze-ns"})`, bronzeCores)
	prom.SetResult(`sum(namespace_memory_working_set_bytes{namespace=~"bronze-ns"})`, 8*gib)
	prom.SetResult(`sum(namespace_cost_per_hour{namespace=~"bronze-ns"})`, 1.0)
}

// ── Test 1: Manager loads 3 profiles correctly ────────────────────────────────

func TestIntegration_ManagerLoadsThreeTenants(t *testing.T) {
	mgr, _, _ := integSetup(t)

	if got := mgr.TenantCount(); got != 3 {
		t.Fatalf("want 3 tenants, got %d", got)
	}

	gold := mgr.GetState("gold")
	if gold == nil {
		t.Fatal("gold state must not be nil")
	}
	if gold.Tier != "gold" {
		t.Errorf("gold Tier: want gold, got %q", gold.Tier)
	}
	if gold.Weight != 10 {
		t.Errorf("gold Weight: want 10, got %d", gold.Weight)
	}
	if gold.GuaranteedCoresPercent != 15 {
		t.Errorf("gold GuaranteedCoresPercent: want 15, got %d", gold.GuaranteedCoresPercent)
	}
	if gold.MaxCores != 50 {
		t.Errorf("gold MaxCores: want 50, got %d", gold.MaxCores)
	}
	if len(gold.Namespaces) != 2 {
		t.Errorf("gold Namespaces: want 2, got %d", len(gold.Namespaces))
	}

	silver := mgr.GetState("silver")
	if silver == nil || silver.Weight != 5 {
		t.Fatal("silver not loaded correctly")
	}
	bronze := mgr.GetState("bronze")
	if bronze == nil || bronze.Weight != 3 {
		t.Fatal("bronze not loaded correctly")
	}
}

// ── Test 2: Manager Refresh populates CPU/memory/cost from Prometheus ─────────

func TestIntegration_RefreshPopulatesUsage(t *testing.T) {
	mgr, prom, _ := integSetup(t)
	integRegisterUsage(prom, 20.0, 8.0, 3.0)

	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	gold := mgr.GetState("gold")
	if gold.CurrentCores != 20.0 {
		t.Errorf("gold CurrentCores: want 20.0, got %.1f", gold.CurrentCores)
	}
	if got := gold.CurrentMemoryGiB; got < 39.9 || got > 40.1 {
		t.Errorf("gold CurrentMemoryGiB: want ~40, got %.2f", got)
	}
	if gold.CurrentCostUSD != 5.0 {
		t.Errorf("gold CurrentCostUSD: want 5.0, got %.1f", gold.CurrentCostUSD)
	}

	silver := mgr.GetState("silver")
	if silver.CurrentCores != 8.0 {
		t.Errorf("silver CurrentCores: want 8.0, got %.1f", silver.CurrentCores)
	}

	bronze := mgr.GetState("bronze")
	if bronze.CurrentCores != 3.0 {
		t.Errorf("bronze CurrentCores: want 3.0, got %.1f", bronze.CurrentCores)
	}
}

// ── Test 3: LastRefreshed is set after Refresh ────────────────────────────────

func TestIntegration_LastRefreshedSet(t *testing.T) {
	mgr, prom, clk := integSetup(t)
	integRegisterUsage(prom, 20.0, 8.0, 3.0)

	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	wantTime := clk.Now()
	for _, name := range []string{"gold", "silver", "bronze"} {
		s := mgr.GetState(name)
		if s.LastRefreshed.IsZero() {
			t.Errorf("%s: LastRefreshed is zero", name)
		}
		if !s.LastRefreshed.Equal(wantTime) {
			t.Errorf("%s: LastRefreshed want %v, got %v", name, wantTime, s.LastRefreshed)
		}
	}
}

// ── Test 4: Fair-share gives gold highest allocation ──────────────────────────

func TestIntegration_FairShareGoldPriority(t *testing.T) {
	inputs := []FairShareInput{
		{Name: "gold", Weight: 10, GuaranteedCoresPercent: 15, Burstable: true, MaxBurstPercent: 40},
		{Name: "silver", Weight: 5, GuaranteedCoresPercent: 10, Burstable: true, MaxBurstPercent: 25},
		{Name: "bronze", Weight: 3, GuaranteedCoresPercent: 5, Burstable: true, MaxBurstPercent: 15},
	}
	shares := ComputeFairShares(integClusterCores, inputs)

	if len(shares) != 3 {
		t.Fatalf("want 3 shares, got %d", len(shares))
	}

	byName := make(map[string]ResourceShare, 3)
	for _, s := range shares {
		byName[s.Name] = s
	}

	gold, silver, bronze := byName["gold"], byName["silver"], byName["bronze"]

	// Guaranteed allocations match spec percentages.
	if got := gold.GuaranteedCores; got != 15.0 {
		t.Errorf("gold guaranteed: want 15, got %.1f", got)
	}
	if got := silver.GuaranteedCores; got != 10.0 {
		t.Errorf("silver guaranteed: want 10, got %.1f", got)
	}
	if got := bronze.GuaranteedCores; got != 5.0 {
		t.Errorf("bronze guaranteed: want 5, got %.1f", got)
	}

	// Gold > silver > bronze in total allocation.
	if gold.TotalCores <= silver.TotalCores {
		t.Errorf("gold total (%.1f) should exceed silver total (%.1f)", gold.TotalCores, silver.TotalCores)
	}
	if silver.TotalCores <= bronze.TotalCores {
		t.Errorf("silver total (%.1f) should exceed bronze total (%.1f)", silver.TotalCores, bronze.TotalCores)
	}

	// Burst caps enforced (MaxCores ≤ cap% of cluster).
	if gold.MaxCores > integClusterCores*0.40+0.01 {
		t.Errorf("gold MaxCores (%.1f) exceeds 40%% cap", gold.MaxCores)
	}
	if silver.MaxCores > integClusterCores*0.25+0.01 {
		t.Errorf("silver MaxCores (%.1f) exceeds 25%% cap", silver.MaxCores)
	}
	if bronze.MaxCores > integClusterCores*0.15+0.01 {
		t.Errorf("bronze MaxCores (%.1f) exceeds 15%% cap", bronze.MaxCores)
	}

	// Total allocation does not exceed cluster capacity.
	total := gold.TotalCores + silver.TotalCores + bronze.TotalCores
	if total > integClusterCores+0.01 {
		t.Errorf("total allocation %.2f exceeds cluster capacity %.1f", total, integClusterCores)
	}
}

// ── Test 5: Quota allows gold scale-up within limits ──────────────────────────

func TestIntegration_QuotaAllowsGoldScaleUp(t *testing.T) {
	mgr, prom, _ := integSetup(t)
	integRegisterUsage(prom, 20.0, 8.0, 3.0)
	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	gold := mgr.GetState("gold")                                     // MaxCores=50, CurrentCores=20
	result := CheckQuota(gold, ResourceDelta{AdditionalCores: 25.0}) // projected 45 ≤ 50
	if !result.Allowed {
		t.Errorf("expected quota allowed, got denied: %s", result.Reason)
	}
}

// ── Test 6: Quota blocks gold exceeding max cores ─────────────────────────────

func TestIntegration_QuotaBlocksGoldOverAllocation(t *testing.T) {
	mgr, prom, _ := integSetup(t)
	integRegisterUsage(prom, 20.0, 8.0, 3.0)
	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	gold := mgr.GetState("gold")                                     // MaxCores=50, CurrentCores=20
	result := CheckQuota(gold, ResourceDelta{AdditionalCores: 35.0}) // projected 55 > 50
	if result.Allowed {
		t.Error("expected quota denied for cores overshoot, got allowed")
	}
	if result.Reason == "" {
		t.Error("denied result must include a reason string")
	}
}

// ── Test 7: Quota blocks bronze exceeding max cores ───────────────────────────

func TestIntegration_QuotaBlocksBronzeOverAllocation(t *testing.T) {
	mgr, prom, _ := integSetup(t)
	integRegisterUsage(prom, 20.0, 8.0, 18.0) // bronze near limit
	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	bronze := mgr.GetState("bronze")                                  // MaxCores=20, CurrentCores=18
	result := CheckQuota(bronze, ResourceDelta{AdditionalCores: 5.0}) // projected 23 > 20
	if result.Allowed {
		t.Error("expected quota denied for bronze overshoot, got allowed")
	}
}

// ── Test 8: Fairness index is within valid range [1/n, 1.0] ──────────────────

func TestIntegration_FairnessIndexInRange(t *testing.T) {
	inputs := []FairnessInput{
		{Name: "gold", CurrentCores: 20.0, GuaranteedCores: 15.0},
		{Name: "silver", CurrentCores: 8.0, GuaranteedCores: 10.0},
		{Name: "bronze", CurrentCores: 3.0, GuaranteedCores: 5.0},
	}

	result := ComputeFairness(inputs)
	if result == nil {
		t.Fatal("ComputeFairness returned nil")
	}

	n := float64(len(inputs))
	minFairness := 1.0 / n
	if result.GlobalIndex < minFairness-0.01 || result.GlobalIndex > 1.01 {
		t.Errorf("GlobalIndex %.4f out of range [%.4f, 1.0]", result.GlobalIndex, minFairness)
	}
}

// ── Test 9: Fairness result covers all 3 tenants ──────────────────────────────

func TestIntegration_FairnessCoversAllTenants(t *testing.T) {
	inputs := []FairnessInput{
		{Name: "gold", CurrentCores: 20.0, GuaranteedCores: 15.0},
		{Name: "silver", CurrentCores: 8.0, GuaranteedCores: 10.0},
		{Name: "bronze", CurrentCores: 3.0, GuaranteedCores: 5.0},
	}

	result := ComputeFairness(inputs)
	if result == nil {
		t.Fatal("nil result")
	}

	for _, name := range []string{"gold", "silver", "bronze"} {
		if _, ok := result.PerTenant[name]; !ok {
			t.Errorf("fairness result missing per-tenant score for %q", name)
		}
	}
}

// ── Test 10: RecordFairnessMetrics does not panic ─────────────────────────────

func TestIntegration_FairnessMetricsRecorded(t *testing.T) {
	inputs := []FairnessInput{
		{Name: "gold", CurrentCores: 20.0, GuaranteedCores: 15.0},
		{Name: "silver", CurrentCores: 8.0, GuaranteedCores: 10.0},
		{Name: "bronze", CurrentCores: 3.0, GuaranteedCores: 5.0},
	}
	result := ComputeFairness(inputs)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RecordFairnessMetrics panicked: %v", r)
		}
	}()
	RecordFairnessMetrics(result)
}

// ── Test 11: Noisy-neighbor detects gold as aggressor, bronze as victim ────────

func TestIntegration_NoisyNeighborGoldDetected(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)}
	detector := NewNoisyNeighborDetector(WithNoisyClock(clk))

	// Baseline snapshot.
	detector.RecordUsage("gold", 10.0)
	detector.RecordUsage("silver", 8.0)
	detector.RecordUsage("bronze", 3.0)

	// Advance 5 minutes: gold grows 150%, bronze stays below guaranteed.
	clk.Advance(5 * time.Minute)
	detector.RecordUsage("gold", 25.0) // +150% growth
	detector.RecordUsage("silver", 8.5)
	detector.RecordUsage("bronze", 3.0)

	guaranteed := map[string]float64{"gold": 15, "silver": 10, "bronze": 5}
	current := map[string]float64{"gold": 25, "silver": 8.5, "bronze": 3}

	alerts := detector.Detect(guaranteed, current)
	if len(alerts) == 0 {
		t.Fatal("expected at least one noisy-neighbor alert, got none")
	}

	var goldAlert *NoisyNeighborAlert
	for i := range alerts {
		if alerts[i].Aggressor == "gold" {
			goldAlert = &alerts[i]
			break
		}
	}
	if goldAlert == nil {
		t.Fatal("gold should be flagged as aggressor")
	}
	if goldAlert.AggressorGrowth <= NoisyGrowthThreshold {
		t.Errorf("gold growth %.2f should exceed threshold %.2f", goldAlert.AggressorGrowth, NoisyGrowthThreshold)
	}
	if len(goldAlert.Victims) == 0 {
		t.Error("gold alert should list at least one victim")
	}

	// Solver signals.
	if !detector.IsNoisy("gold") {
		t.Error("IsNoisy('gold') should be true after alert")
	}

	// Both silver and bronze are below their guaranteed  → both are victims.
	victimSet := make(map[string]bool)
	for _, v := range goldAlert.Victims {
		victimSet[v] = true
	}
	if !victimSet["bronze"] {
		t.Errorf("bronze should be listed as victim; got victims: %v", goldAlert.Victims)
	}
}

// ── Test 12: AllocationStatusFor returns correct status for each usage level ──

func TestIntegration_AllocationStatuses(t *testing.T) {
	inputs := []FairShareInput{
		{Name: "gold", Weight: 10, GuaranteedCoresPercent: 15, Burstable: true, MaxBurstPercent: 40},
	}
	shares := ComputeFairShares(integClusterCores, inputs)
	if len(shares) != 1 {
		t.Fatalf("expected 1 share, got %d", len(shares))
	}
	goldShare := shares[0] // guaranteed=15, TotalCores=40

	cases := []struct {
		current float64
		want    string
	}{
		{10.0, "under_allocated"}, // ratio=0.67 < 0.8
		{15.0, "guaranteed"},      // ratio=1.0, at guaranteed
		{20.0, "bursting"},        // ratio=1.33, above guaranteed but ≤ total
		{45.0, "throttled"},       // above TotalCores (40)
	}

	for _, tc := range cases {
		got := AllocationStatusFor(tc.current, goldShare)
		if got != tc.want {
			t.Errorf("AllocationStatusFor(%.1f) = %q, want %q (share: %+v)", tc.current, got, tc.want, goldShare)
		}
	}
}

// ── Test 13: End-to-end full cycle chains all components ─────────────────────

func TestIntegration_FullCycleEndToEnd(t *testing.T) {
	// 1. Setup.
	clk := &fakeClock{now: time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)}
	prom := newMockProm()
	mgr := NewManager(prom, WithClock(clk), WithRefreshInterval(30*time.Second))
	mgr.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfileFull("gold", tenantv1alpha1.TierGold, 10,
			[]string{"gold-ns1", "gold-ns2"},
			50, 100, "5000",
			15, true, 40,
		),
		makeProfileFull("silver", tenantv1alpha1.TierSilver, 5,
			[]string{"silver-ns"},
			30, 60, "2000",
			10, true, 25,
		),
		makeProfileFull("bronze", tenantv1alpha1.TierBronze, 3,
			[]string{"bronze-ns"},
			20, 30, "1000",
			5, true, 15,
		),
	})
	integRegisterUsage(prom, 20.0, 8.0, 3.0)

	detector := NewNoisyNeighborDetector(WithNoisyClock(clk))

	// 2. Refresh — populates current usage from Prometheus.
	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	states := mgr.GetAllStates()
	if len(states) != 3 {
		t.Fatalf("want 3 tenants after refresh, got %d", len(states))
	}

	// 3. FairShare — gold must lead in total allocation.
	fsInputs := []FairShareInput{
		{Name: "gold", Weight: 10, GuaranteedCoresPercent: 15, Burstable: true, MaxBurstPercent: 40,
			CurrentCores: states["gold"].CurrentCores},
		{Name: "silver", Weight: 5, GuaranteedCoresPercent: 10, Burstable: true, MaxBurstPercent: 25,
			CurrentCores: states["silver"].CurrentCores},
		{Name: "bronze", Weight: 3, GuaranteedCoresPercent: 5, Burstable: true, MaxBurstPercent: 15,
			CurrentCores: states["bronze"].CurrentCores},
	}
	shares := ComputeFairShares(integClusterCores, fsInputs)
	if len(shares) != 3 {
		t.Fatalf("want 3 fair-share allocations, got %d", len(shares))
	}
	sharesMap := make(map[string]ResourceShare, 3)
	for _, s := range shares {
		sharesMap[s.Name] = s
	}
	if sharesMap["gold"].TotalCores <= sharesMap["silver"].TotalCores {
		t.Errorf("gold total (%.1f) must exceed silver total (%.1f)",
			sharesMap["gold"].TotalCores, sharesMap["silver"].TotalCores)
	}
	if sharesMap["silver"].TotalCores <= sharesMap["bronze"].TotalCores {
		t.Errorf("silver total (%.1f) must exceed bronze total (%.1f)",
			sharesMap["silver"].TotalCores, sharesMap["bronze"].TotalCores)
	}

	// 4. Quota — gold allowed within limit, denied when over.
	goldState := states["gold"]                                       // MaxCores=50, CurrentCores=20
	q1 := CheckQuota(goldState, ResourceDelta{AdditionalCores: 25.0}) // 45 ≤ 50
	if !q1.Allowed {
		t.Errorf("gold quota: expected allowed for +25, got denied: %s", q1.Reason)
	}
	q2 := CheckQuota(goldState, ResourceDelta{AdditionalCores: 40.0}) // 60 > 50
	if q2.Allowed {
		t.Error("gold quota: expected denied for +40 (over limit)")
	}

	// 5. Fairness index is in [1/3, 1.0] and metrics can be recorded.
	fInputs := []FairnessInput{
		{Name: "gold", CurrentCores: 20.0, GuaranteedCores: 15.0},
		{Name: "silver", CurrentCores: 8.0, GuaranteedCores: 10.0},
		{Name: "bronze", CurrentCores: 3.0, GuaranteedCores: 5.0},
	}
	fairResult := ComputeFairness(fInputs)
	if fairResult == nil {
		t.Fatal("ComputeFairness returned nil")
	}
	if fairResult.GlobalIndex < 1.0/3.0-0.01 || fairResult.GlobalIndex > 1.01 {
		t.Errorf("GlobalIndex %.4f out of expected range [0.33, 1.0]", fairResult.GlobalIndex)
	}
	RecordFairnessMetrics(fairResult) // must not panic

	// 6. Noisy neighbor — gold spikes, bronze becomes victim.
	for _, name := range []string{"gold", "silver", "bronze"} {
		detector.RecordUsage(name, states[name].CurrentCores)
	}
	clk.Advance(5 * time.Minute)
	detector.RecordUsage("gold", 40.0) // +100% growth (20→40)
	detector.RecordUsage("silver", 8.0)
	detector.RecordUsage("bronze", 3.0)

	guaranteed := map[string]float64{"gold": 15, "silver": 10, "bronze": 5}
	current := map[string]float64{"gold": 40, "silver": 8, "bronze": 3}
	alerts := detector.Detect(guaranteed, current)

	if len(alerts) == 0 {
		t.Fatal("expected noisy-neighbor alert after gold spikes")
	}
	if !detector.IsNoisy("gold") {
		t.Error("gold should be detected as noisy after spike")
	}

	// 7. LastRefreshed is set on all tenants.
	for _, name := range []string{"gold", "silver", "bronze"} {
		if s := mgr.GetState(name); s.LastRefreshed.IsZero() {
			t.Errorf("tenant %q has zero LastRefreshed after Refresh", name)
		}
	}
}
