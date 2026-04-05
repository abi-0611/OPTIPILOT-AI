package forecaster

import (
	"math"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestTracker(opts ...AccuracyOption) (*AccuracyTracker, *fakeClock) {
	clk := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	t := NewAccuracyTracker(opts...)
	t.SetNowFn(clk.Now)
	return t, clk
}

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func approx(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// ── basic metrics ─────────────────────────────────────────────────────────────

func TestAccuracyTracker_NoData(t *testing.T) {
	tr, _ := newTestTracker()
	if tr.MAE("svc") != 0 {
		t.Error("expected MAE=0 for unknown service")
	}
	if tr.MAPE("svc") != 0 {
		t.Error("expected MAPE=0 for unknown service")
	}
	if tr.IsFallbackActive("svc") {
		t.Error("fallback should not be active for unknown service")
	}
}

func TestAccuracyTracker_SinglePerfect(t *testing.T) {
	tr, _ := newTestTracker()
	tr.Record("svc", 100.0, 100.0)
	if tr.MAE("svc") != 0 {
		t.Errorf("MAE=%f, want 0", tr.MAE("svc"))
	}
	if tr.MAPE("svc") != 0 {
		t.Errorf("MAPE=%f, want 0", tr.MAPE("svc"))
	}
}

func TestAccuracyTracker_SingleError(t *testing.T) {
	tr, _ := newTestTracker()
	tr.Record("svc", 110.0, 100.0)
	if !approx(tr.MAE("svc"), 10.0, 0.001) {
		t.Errorf("MAE=%f, want 10.0", tr.MAE("svc"))
	}
	if !approx(tr.MAPE("svc"), 10.0, 0.001) {
		t.Errorf("MAPE=%f, want 10.0", tr.MAPE("svc"))
	}
}

func TestAccuracyTracker_MultipleRecords(t *testing.T) {
	tr, clk := newTestTracker()
	tr.Record("svc", 110.0, 100.0) // AE=10, APE=10%
	clk.Advance(time.Minute)
	tr.Record("svc", 90.0, 100.0) // AE=10, APE=10%
	if !approx(tr.MAE("svc"), 10.0, 0.001) {
		t.Errorf("MAE=%f, want 10.0", tr.MAE("svc"))
	}
	if !approx(tr.MAPE("svc"), 10.0, 0.001) {
		t.Errorf("MAPE=%f, want 10.0", tr.MAPE("svc"))
	}
}

func TestAccuracyTracker_AsymmetricMAE(t *testing.T) {
	tr, clk := newTestTracker()
	tr.Record("svc", 120.0, 100.0) // AE=20
	clk.Advance(time.Minute)
	tr.Record("svc", 105.0, 100.0) // AE=5
	// MAE = (20+5)/2 = 12.5
	if !approx(tr.MAE("svc"), 12.5, 0.001) {
		t.Errorf("MAE=%f, want 12.5", tr.MAE("svc"))
	}
}

func TestAccuracyTracker_ZeroActualExcluded(t *testing.T) {
	tr, _ := newTestTracker()
	tr.Record("svc", 10.0, 0.0)
	if tr.MAPE("svc") != 0 {
		t.Errorf("MAPE=%f, want 0 (zero actual excluded)", tr.MAPE("svc"))
	}
	if !approx(tr.MAE("svc"), 10.0, 0.001) {
		t.Errorf("MAE=%f, want 10.0", tr.MAE("svc"))
	}
}

// ── fallback logic ────────────────────────────────────────────────────────────

func TestAccuracyTracker_NoFallbackBelowThreshold(t *testing.T) {
	tr, clk := newTestTracker()
	for i := 0; i < 10; i++ {
		tr.Record("svc", 120.0, 100.0) // MAPE=20% < 30%
		clk.Advance(15 * time.Minute)
	}
	if tr.IsFallbackActive("svc") {
		t.Error("fallback should not activate for MAPE=20%%")
	}
}

func TestAccuracyTracker_FallbackActivatesAfterSustained(t *testing.T) {
	tr, clk := newTestTracker(WithSustainDuration(time.Hour))
	for i := 0; i < 5; i++ {
		tr.Record("svc", 150.0, 100.0) // MAPE=50%
		clk.Advance(15 * time.Minute)
	}
	// After 4 intervals of 15 min = 60 min since first record
	if !tr.IsFallbackActive("svc") {
		t.Error("fallback should be active after 1 hour of high MAPE")
	}
}

func TestAccuracyTracker_FallbackNotWithoutSustain(t *testing.T) {
	tr, clk := newTestTracker(WithSustainDuration(time.Hour))
	tr.Record("svc", 150.0, 100.0)
	clk.Advance(5 * time.Minute)
	tr.Record("svc", 150.0, 100.0)
	if tr.IsFallbackActive("svc") {
		t.Error("fallback should not activate after only 5 minutes")
	}
}

func TestAccuracyTracker_FallbackRecovers(t *testing.T) {
	tr, clk := newTestTracker(WithSustainDuration(time.Minute))
	// Trigger fallback
	tr.Record("svc", 150.0, 100.0)
	clk.Advance(2 * time.Minute)
	tr.Record("svc", 150.0, 100.0)
	if !tr.IsFallbackActive("svc") {
		t.Fatal("expected fallback active")
	}

	// Record accurate predictions to bring MAPE below threshold
	for i := 0; i < 20; i++ {
		clk.Advance(time.Minute)
		tr.Record("svc", 100.0, 100.0)
	}
	if tr.MAPE("svc") >= 30.0 {
		t.Fatalf("MAPE=%f, should be < 30", tr.MAPE("svc"))
	}
	if tr.IsFallbackActive("svc") {
		t.Error("fallback should have recovered once MAPE dropped")
	}
}

func TestAccuracyTracker_ShortSustain(t *testing.T) {
	tr, clk := newTestTracker(
		WithMAPEThreshold(10.0),
		WithSustainDuration(10*time.Second),
	)
	tr.Record("svc", 150.0, 100.0)
	clk.Advance(15 * time.Second)
	tr.Record("svc", 150.0, 100.0)
	if !tr.IsFallbackActive("svc") {
		t.Error("fallback should activate with short sustain")
	}
}

// ── multi-service ─────────────────────────────────────────────────────────────

func TestAccuracyTracker_ServicesIsolated(t *testing.T) {
	tr, clk := newTestTracker()
	tr.Record("svc-a", 110.0, 100.0)
	clk.Advance(time.Second)
	tr.Record("svc-b", 200.0, 100.0)

	if !approx(tr.MAE("svc-a"), 10.0, 0.001) {
		t.Errorf("svc-a MAE=%f, want 10", tr.MAE("svc-a"))
	}
	if !approx(tr.MAE("svc-b"), 100.0, 0.001) {
		t.Errorf("svc-b MAE=%f, want 100", tr.MAE("svc-b"))
	}
}

func TestAccuracyTracker_ServicesList(t *testing.T) {
	tr, clk := newTestTracker()
	tr.Record("svc-a", 100.0, 100.0)
	clk.Advance(time.Second)
	tr.Record("svc-b", 100.0, 100.0)
	names := tr.Services()
	if len(names) != 2 {
		t.Errorf("expected 2 services, got %d", len(names))
	}
}

func TestAccuracyTracker_FallbackPerService(t *testing.T) {
	tr, clk := newTestTracker(WithSustainDuration(10 * time.Second))
	// svc-a gets bad predictions
	tr.Record("svc-a", 200.0, 100.0)
	clk.Advance(20 * time.Second)
	tr.Record("svc-a", 200.0, 100.0)
	// svc-b is accurate
	tr.Record("svc-b", 100.0, 100.0)

	if !tr.IsFallbackActive("svc-a") {
		t.Error("svc-a fallback should be active")
	}
	if tr.IsFallbackActive("svc-b") {
		t.Error("svc-b fallback should not be active")
	}
}

// ── window cap ────────────────────────────────────────────────────────────────

func TestAccuracyTracker_WindowCap(t *testing.T) {
	tr, clk := newTestTracker(WithWindowMax(5))
	for i := 0; i < 10; i++ {
		tr.Record("svc", float64(100+i), 100.0)
		clk.Advance(time.Second)
	}
	tr.mu.Lock()
	n := len(tr.services["svc"].records)
	tr.mu.Unlock()
	if n != 5 {
		t.Errorf("records=%d, want 5 (capped)", n)
	}
}

func TestAccuracyTracker_WindowReflectsRecent(t *testing.T) {
	tr, clk := newTestTracker(WithWindowMax(3))
	tr.Record("svc", 200.0, 100.0) // evicted
	clk.Advance(time.Second)
	tr.Record("svc", 200.0, 100.0) // evicted
	clk.Advance(time.Second)
	tr.Record("svc", 100.0, 100.0) // kept
	clk.Advance(time.Second)
	tr.Record("svc", 100.0, 100.0) // kept
	clk.Advance(time.Second)
	tr.Record("svc", 100.0, 100.0) // kept

	if !approx(tr.MAE("svc"), 0.0, 0.001) {
		t.Errorf("MAE=%f, want 0 (recent window all perfect)", tr.MAE("svc"))
	}
}

// ── reset ─────────────────────────────────────────────────────────────────────

func TestAccuracyTracker_Reset(t *testing.T) {
	tr, _ := newTestTracker()
	tr.Record("svc", 200.0, 100.0)
	if tr.MAE("svc") == 0 {
		t.Fatal("expected non-zero MAE before reset")
	}
	tr.Reset("svc")
	if tr.MAE("svc") != 0 {
		t.Error("MAE should be 0 after reset")
	}
	if tr.IsFallbackActive("svc") {
		t.Error("fallback should not be active after reset")
	}
}

func TestAccuracyTracker_ResetNonexistent(t *testing.T) {
	tr, _ := newTestTracker()
	tr.Reset("does-not-exist") // should not panic
}

// ── edge cases ────────────────────────────────────────────────────────────────

func TestAccuracyTracker_NegativePredicted(t *testing.T) {
	tr, _ := newTestTracker()
	tr.Record("svc", -10.0, 10.0)
	if !approx(tr.MAE("svc"), 20.0, 0.001) {
		t.Errorf("MAE=%f, want 20", tr.MAE("svc"))
	}
	if !approx(tr.MAPE("svc"), 200.0, 0.001) {
		t.Errorf("MAPE=%f, want 200", tr.MAPE("svc"))
	}
}
