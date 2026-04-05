package tenant

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/metrics"
)

// ── helpers ──────────────────────────────────────────────────────────────────

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// mockProm allows per-query responses.
type mockProm struct {
	mu       sync.Mutex
	results  map[string]float64
	errors   map[string]error
	queries  []string // recorded queries in order
	queryErr error    // default error for unregistered queries
}

func newMockProm() *mockProm {
	return &mockProm{
		results: make(map[string]float64),
		errors:  make(map[string]error),
	}
}

func (m *mockProm) SetResult(query string, val float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[query] = val
}

func (m *mockProm) SetError(query string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[query] = err
}

func (m *mockProm) Query(_ context.Context, query string) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queries = append(m.queries, query)
	if err, ok := m.errors[query]; ok {
		return 0, err
	}
	if val, ok := m.results[query]; ok {
		return val, nil
	}
	if m.queryErr != nil {
		return 0, m.queryErr
	}
	return 0, nil
}

func (m *mockProm) QueryRange(_ context.Context, _ string, _, _ time.Time, _ time.Duration) ([]metrics.DataPoint, error) {
	return nil, nil
}

func (m *mockProm) Healthy(_ context.Context) error { return nil }

func (m *mockProm) QueriesCalled() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.queries))
	copy(out, m.queries)
	return out
}

func makeProfile(name string, tier tenantv1alpha1.TenantTier, weight int32, namespaces []string) tenantv1alpha1.TenantProfile {
	return tenantv1alpha1.TenantProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: tenantv1alpha1.TenantProfileSpec{
			Tier:       tier,
			Weight:     weight,
			Namespaces: namespaces,
		},
	}
}

func makeProfileFull(name string, tier tenantv1alpha1.TenantTier, weight int32, namespaces []string,
	maxCores, maxMemGiB int32, maxCostUSD string, guaranteedPct int32, burstable bool, maxBurstPct int32,
) tenantv1alpha1.TenantProfile {
	return tenantv1alpha1.TenantProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: tenantv1alpha1.TenantProfileSpec{
			Tier:       tier,
			Weight:     weight,
			Namespaces: namespaces,
			Budgets: &tenantv1alpha1.TenantBudgets{
				MaxCores:          maxCores,
				MaxMemoryGiB:      maxMemGiB,
				MaxMonthlyCostUSD: maxCostUSD,
			},
			FairSharePolicy: &tenantv1alpha1.FairSharePolicy{
				GuaranteedCoresPercent: guaranteedPct,
				Burstable:              burstable,
				MaxBurstPercent:        maxBurstPct,
			},
		},
	}
}

func newTestManager(prom metrics.PrometheusClient) (*Manager, *fakeClock) {
	clk := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	mgr := NewManager(prom, WithClock(clk), WithRefreshInterval(5*time.Second))
	return mgr, clk
}

// ── NewManager ───────────────────────────────────────────────────────────────

func TestNewManager_Defaults(t *testing.T) {
	prom := newMockProm()
	m := NewManager(prom)
	if m.interval != 30*time.Second {
		t.Errorf("interval=%v, want 30s", m.interval)
	}
	if m.prom != prom {
		t.Error("prom client not set")
	}
	if m.TenantCount() != 0 {
		t.Error("expected 0 tenants")
	}
}

func TestNewManager_WithOptions(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	m := NewManager(newMockProm(), WithClock(clk), WithRefreshInterval(10*time.Second))
	if m.interval != 10*time.Second {
		t.Errorf("interval=%v, want 10s", m.interval)
	}
	if m.clock != clk {
		t.Error("custom clock not set")
	}
}

// ── UpdateProfiles ───────────────────────────────────────────────────────────

func TestUpdateProfiles_AddsNew(t *testing.T) {
	m, _ := newTestManager(newMockProm())

	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("gold-team", tenantv1alpha1.TierGold, 20, []string{"gold-ns1", "gold-ns2"}),
		makeProfile("silver-team", tenantv1alpha1.TierSilver, 10, []string{"silver-ns"}),
	})

	if m.TenantCount() != 2 {
		t.Fatalf("TenantCount=%d, want 2", m.TenantCount())
	}

	s := m.GetState("gold-team")
	if s == nil {
		t.Fatal("gold-team state is nil")
	}
	if s.Tier != "gold" {
		t.Errorf("Tier=%s, want gold", s.Tier)
	}
	if s.Weight != 20 {
		t.Errorf("Weight=%d, want 20", s.Weight)
	}
	if len(s.Namespaces) != 2 || s.Namespaces[0] != "gold-ns1" {
		t.Errorf("Namespaces=%v", s.Namespaces)
	}
}

func TestUpdateProfiles_RemovesStale(t *testing.T) {
	m, _ := newTestManager(newMockProm())

	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("a", tenantv1alpha1.TierGold, 10, []string{"ns-a"}),
		makeProfile("b", tenantv1alpha1.TierSilver, 5, []string{"ns-b"}),
	})
	if m.TenantCount() != 2 {
		t.Fatal("expected 2")
	}

	// Remove "b"
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("a", tenantv1alpha1.TierGold, 10, []string{"ns-a"}),
	})
	if m.TenantCount() != 1 {
		t.Fatalf("TenantCount=%d, want 1", m.TenantCount())
	}
	if m.GetState("b") != nil {
		t.Error("tenant b should be removed")
	}
}

func TestUpdateProfiles_UpdatesExisting(t *testing.T) {
	m, _ := newTestManager(newMockProm())

	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("team", tenantv1alpha1.TierSilver, 5, []string{"ns1"}),
	})

	// Update weight and tier
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("team", tenantv1alpha1.TierGold, 25, []string{"ns1", "ns2"}),
	})

	s := m.GetState("team")
	if s.Tier != "gold" {
		t.Errorf("Tier=%s, want gold", s.Tier)
	}
	if s.Weight != 25 {
		t.Errorf("Weight=%d, want 25", s.Weight)
	}
	if len(s.Namespaces) != 2 {
		t.Error("expected 2 namespaces")
	}
}

func TestUpdateProfiles_FullSpec(t *testing.T) {
	m, _ := newTestManager(newMockProm())

	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfileFull("team", tenantv1alpha1.TierPlatinum, 100, []string{"ns"},
			50, 128, "5000.50", 15, true, 40),
	})

	s := m.GetState("team")
	if s.MaxCores != 50 {
		t.Errorf("MaxCores=%d, want 50", s.MaxCores)
	}
	if s.MaxMemoryGiB != 128 {
		t.Errorf("MaxMemoryGiB=%d, want 128", s.MaxMemoryGiB)
	}
	if s.MaxMonthlyCostUSD != 5000.50 {
		t.Errorf("MaxMonthlyCostUSD=%f, want 5000.50", s.MaxMonthlyCostUSD)
	}
	if s.GuaranteedCoresPercent != 15 {
		t.Errorf("GuaranteedCoresPercent=%d, want 15", s.GuaranteedCoresPercent)
	}
	if !s.Burstable {
		t.Error("expected Burstable=true")
	}
	if s.MaxBurstPercent != 40 {
		t.Errorf("MaxBurstPercent=%d, want 40", s.MaxBurstPercent)
	}
}

func TestUpdateProfiles_NilBudgetsClears(t *testing.T) {
	m, _ := newTestManager(newMockProm())

	// First set with budgets.
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfileFull("t", tenantv1alpha1.TierGold, 10, []string{"ns"}, 50, 128, "1000", 10, true, 30),
	})
	// Then update without budgets.
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("t", tenantv1alpha1.TierGold, 10, []string{"ns"}),
	})

	s := m.GetState("t")
	if s.MaxCores != 0 || s.MaxMemoryGiB != 0 || s.MaxMonthlyCostUSD != 0 {
		t.Error("expected budgets cleared")
	}
	if s.GuaranteedCoresPercent != 0 || s.Burstable || s.MaxBurstPercent != 0 {
		t.Error("expected fair-share policy cleared")
	}
}

// ── GetState ─────────────────────────────────────────────────────────────────

func TestGetState_UnknownReturnsNil(t *testing.T) {
	m, _ := newTestManager(newMockProm())
	if m.GetState("nope") != nil {
		t.Error("expected nil for unknown tenant")
	}
}

func TestGetState_ReturnsCopy(t *testing.T) {
	m, _ := newTestManager(newMockProm())
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("t", tenantv1alpha1.TierGold, 10, []string{"ns1"}),
	})

	s1 := m.GetState("t")
	s1.CurrentCores = 999
	s1.Namespaces[0] = "mutated"

	s2 := m.GetState("t")
	if s2.CurrentCores != 0 {
		t.Error("mutation leaked to internal state")
	}
	if s2.Namespaces[0] != "ns1" {
		t.Error("namespace mutation leaked")
	}
}

// ── GetAllStates ─────────────────────────────────────────────────────────────

func TestGetAllStates_Empty(t *testing.T) {
	m, _ := newTestManager(newMockProm())
	all := m.GetAllStates()
	if len(all) != 0 {
		t.Error("expected empty map")
	}
}

func TestGetAllStates_ReturnsAllCopies(t *testing.T) {
	m, _ := newTestManager(newMockProm())
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("a", tenantv1alpha1.TierGold, 10, []string{"ns-a"}),
		makeProfile("b", tenantv1alpha1.TierSilver, 5, []string{"ns-b"}),
	})

	all := m.GetAllStates()
	if len(all) != 2 {
		t.Fatalf("len=%d, want 2", len(all))
	}
	// Mutate copy; verify no leak.
	all["a"].CurrentCores = 100
	s := m.GetState("a")
	if s.CurrentCores != 0 {
		t.Error("mutation leaked through GetAllStates")
	}
}

// ── Refresh ──────────────────────────────────────────────────────────────────

func TestRefresh_QueriesPrometheus(t *testing.T) {
	prom := newMockProm()
	m, clk := newTestManager(prom)
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("team", tenantv1alpha1.TierGold, 10, []string{"ns1", "ns2"}),
	})

	nsRegex := "ns1|ns2"
	cpuQ := fmt.Sprintf(`sum(namespace_cpu_usage_seconds_total{namespace=~"%s"})`, nsRegex)
	memQ := fmt.Sprintf(`sum(namespace_memory_working_set_bytes{namespace=~"%s"})`, nsRegex)
	costQ := fmt.Sprintf(`sum(namespace_cost_per_hour{namespace=~"%s"})`, nsRegex)

	prom.SetResult(cpuQ, 8.5)
	prom.SetResult(memQ, 32*1024*1024*1024) // 32 GiB in bytes
	prom.SetResult(costQ, 12.50)

	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	s := m.GetState("team")
	if s.CurrentCores != 8.5 {
		t.Errorf("CurrentCores=%f, want 8.5", s.CurrentCores)
	}
	if s.CurrentMemoryGiB != 32 {
		t.Errorf("CurrentMemoryGiB=%f, want 32", s.CurrentMemoryGiB)
	}
	if s.CurrentCostUSD != 12.50 {
		t.Errorf("CurrentCostUSD=%f, want 12.50", s.CurrentCostUSD)
	}
	if !s.LastRefreshed.Equal(clk.Now()) {
		t.Error("LastRefreshed not set to clock time")
	}

	// Verify all 3 queries were made.
	queries := prom.QueriesCalled()
	if len(queries) != 3 {
		t.Fatalf("expected 3 queries, got %d", len(queries))
	}
}

func TestRefresh_MultiTenant(t *testing.T) {
	prom := newMockProm()
	m, _ := newTestManager(prom)
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("a", tenantv1alpha1.TierGold, 10, []string{"ns-a"}),
		makeProfile("b", tenantv1alpha1.TierSilver, 5, []string{"ns-b"}),
	})

	prom.SetResult(`sum(namespace_cpu_usage_seconds_total{namespace=~"ns-a"})`, 4.0)
	prom.SetResult(`sum(namespace_memory_working_set_bytes{namespace=~"ns-a"})`, 16*1024*1024*1024)
	prom.SetResult(`sum(namespace_cost_per_hour{namespace=~"ns-a"})`, 5.0)
	prom.SetResult(`sum(namespace_cpu_usage_seconds_total{namespace=~"ns-b"})`, 2.0)
	prom.SetResult(`sum(namespace_memory_working_set_bytes{namespace=~"ns-b"})`, 8*1024*1024*1024)
	prom.SetResult(`sum(namespace_cost_per_hour{namespace=~"ns-b"})`, 2.0)

	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	sa := m.GetState("a")
	sb := m.GetState("b")
	if sa.CurrentCores != 4.0 {
		t.Errorf("a cores=%f, want 4.0", sa.CurrentCores)
	}
	if sb.CurrentCores != 2.0 {
		t.Errorf("b cores=%f, want 2.0", sb.CurrentCores)
	}
	if sa.CurrentMemoryGiB != 16 {
		t.Errorf("a mem=%f, want 16", sa.CurrentMemoryGiB)
	}
	if sb.CurrentMemoryGiB != 8 {
		t.Errorf("b mem=%f, want 8", sb.CurrentMemoryGiB)
	}
}

func TestRefresh_EmptyNamespacesSkipped(t *testing.T) {
	prom := newMockProm()
	m, _ := newTestManager(prom)

	m.mu.Lock()
	m.tenants["empty"] = &TenantState{Name: "empty", Namespaces: nil}
	m.mu.Unlock()

	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prom.QueriesCalled()) != 0 {
		t.Error("should not query for tenant with no namespaces")
	}
}

func TestRefresh_PartialErrorContinues(t *testing.T) {
	prom := newMockProm()
	m, _ := newTestManager(prom)
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("t", tenantv1alpha1.TierGold, 10, []string{"ns"}),
	})

	prom.SetError(`sum(namespace_cpu_usage_seconds_total{namespace=~"ns"})`, fmt.Errorf("timeout"))
	prom.SetResult(`sum(namespace_memory_working_set_bytes{namespace=~"ns"})`, 4*1024*1024*1024)
	prom.SetResult(`sum(namespace_cost_per_hour{namespace=~"ns"})`, 1.5)

	err := m.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cpu query") {
		t.Errorf("error should mention cpu query: %v", err)
	}

	// Memory and cost should still be set despite CPU error.
	s := m.GetState("t")
	if s.CurrentCores != 0 {
		t.Errorf("cores should be 0 on error, got %f", s.CurrentCores)
	}
	if s.CurrentMemoryGiB != 4 {
		t.Errorf("mem=%f, want 4", s.CurrentMemoryGiB)
	}
	if s.CurrentCostUSD != 1.5 {
		t.Errorf("cost=%f, want 1.5", s.CurrentCostUSD)
	}
}

func TestRefresh_AllErrors(t *testing.T) {
	prom := newMockProm()
	prom.queryErr = fmt.Errorf("prom down")
	m, _ := newTestManager(prom)
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("t", tenantv1alpha1.TierGold, 10, []string{"ns"}),
	})

	err := m.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error when all queries fail")
	}
	// Should have 3 error entries (cpu, memory, cost).
	parts := strings.Split(err.Error(), ";")
	if len(parts) < 3 {
		t.Errorf("expected 3 error parts, got: %s", err.Error())
	}
}

func TestRefresh_NoTenantsNoError(t *testing.T) {
	m, _ := newTestManager(newMockProm())
	if err := m.Refresh(context.Background()); err != nil {
		t.Errorf("empty refresh should not error: %v", err)
	}
}

func TestRefresh_UpdatesTimestamp(t *testing.T) {
	prom := newMockProm()
	m, clk := newTestManager(prom)
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("t", tenantv1alpha1.TierGold, 10, []string{"ns"}),
	})

	_ = m.Refresh(context.Background())
	t1 := m.GetState("t").LastRefreshed

	clk.Advance(5 * time.Second)
	_ = m.Refresh(context.Background())
	t2 := m.GetState("t").LastRefreshed

	if !t2.After(t1) {
		t.Error("second refresh should have later timestamp")
	}
}

// ── Concurrent access ────────────────────────────────────────────────────────

func TestConcurrentAccess(t *testing.T) {
	prom := newMockProm()
	m, _ := newTestManager(prom)
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("t", tenantv1alpha1.TierGold, 10, []string{"ns"}),
	})

	var wg sync.WaitGroup
	ctx := context.Background()

	// Concurrent readers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = m.GetState("t")
				_ = m.GetAllStates()
				_ = m.TenantCount()
			}
		}()
	}

	// Concurrent refreshers.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = m.Refresh(ctx)
			}
		}()
	}

	// Concurrent updater.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
				makeProfile("t", tenantv1alpha1.TierGold, 10, []string{"ns"}),
			})
		}
	}()

	wg.Wait()
}

// ── Start (context cancellation) ─────────────────────────────────────────────

func TestStart_CancelStops(t *testing.T) {
	prom := newMockProm()
	m, _ := newTestManager(prom)
	m.UpdateProfiles([]tenantv1alpha1.TenantProfile{
		makeProfile("t", tenantv1alpha1.TierGold, 10, []string{"ns"}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.Start(ctx)
	}()

	// Let initial refresh run, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not stop after context cancellation")
	}

	// Verify at least one refresh occurred.
	if len(prom.QueriesCalled()) == 0 {
		t.Error("expected at least one query from initial refresh")
	}
}
