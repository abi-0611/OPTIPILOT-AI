package tenant

import (
	"strings"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newTestDetector(clk *fakeClock) *NoisyNeighborDetector {
	return NewNoisyNeighborDetector(
		WithNoisyClock(clk),
		WithNoisyWindow(5*time.Minute),
		WithNoisyThreshold(0.50),
	)
}

func newClk() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// ── Basic detection ──────────────────────────────────────────────────────────

func TestNoisyNeighbor_GrowthTriggersAlert(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	// Record baseline for aggressor and victim.
	d.RecordUsage("agg", 10)
	d.RecordUsage("victim", 8)

	// Advance 3 minutes, grow aggressor by 60%.
	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 16) // 60% growth
	d.RecordUsage("victim", 8)

	guaranteed := map[string]float64{"agg": 10, "victim": 10}
	current := map[string]float64{"agg": 16, "victim": 8}

	alerts := d.Detect(guaranteed, current)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Aggressor != "agg" {
		t.Errorf("aggressor=%s, want agg", alerts[0].Aggressor)
	}
	if len(alerts[0].Victims) != 1 || alerts[0].Victims[0] != "victim" {
		t.Errorf("victims=%v, want [victim]", alerts[0].Victims)
	}
	if alerts[0].AggressorGrowth < 0.5 {
		t.Errorf("growth=%f, want >=0.5", alerts[0].AggressorGrowth)
	}
}

func TestNoisyNeighbor_NoAlertBelowThreshold(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("agg", 10)
	d.RecordUsage("victim", 8)

	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 14) // 40% growth — below threshold
	d.RecordUsage("victim", 8)

	alerts := d.Detect(
		map[string]float64{"agg": 10, "victim": 10},
		map[string]float64{"agg": 14, "victim": 8},
	)
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(alerts))
	}
}

func TestNoisyNeighbor_NoAlertWhenVictimAboveGuaranteed(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("agg", 10)
	d.RecordUsage("other", 12)

	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 20) // 100% growth
	d.RecordUsage("other", 12)

	// "other" is ABOVE guaranteed — not a victim.
	alerts := d.Detect(
		map[string]float64{"agg": 10, "other": 10},
		map[string]float64{"agg": 20, "other": 12},
	)
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts (no victim below guaranteed), got %d", len(alerts))
	}
}

func TestNoisyNeighbor_AggressorNotSelfVictim(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	// Aggressor is also below guaranteed, but should not appear as its own victim.
	d.RecordUsage("agg", 10)
	d.RecordUsage("victim", 5)

	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 16) // 60% growth
	d.RecordUsage("victim", 5)

	alerts := d.Detect(
		map[string]float64{"agg": 20, "victim": 10}, // agg below own guaranteed too
		map[string]float64{"agg": 16, "victim": 5},
	)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	for _, v := range alerts[0].Victims {
		if v == "agg" {
			t.Error("aggressor should not be in victims list")
		}
	}
}

// ── Multiple aggressors ──────────────────────────────────────────────────────

func TestNoisyNeighbor_MultipleAggressors(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("a", 10)
	d.RecordUsage("b", 10)
	d.RecordUsage("victim", 5)

	clk.Advance(3 * time.Minute)
	d.RecordUsage("a", 16)
	d.RecordUsage("b", 18)
	d.RecordUsage("victim", 5)

	alerts := d.Detect(
		map[string]float64{"a": 10, "b": 10, "victim": 10},
		map[string]float64{"a": 16, "b": 18, "victim": 5},
	)
	if len(alerts) < 2 {
		t.Errorf("expected 2 alerts, got %d", len(alerts))
	}
}

func TestNoisyNeighbor_MultipleVictims(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("agg", 10)
	d.RecordUsage("v1", 5)
	d.RecordUsage("v2", 3)

	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 20)
	d.RecordUsage("v1", 5)
	d.RecordUsage("v2", 3)

	alerts := d.Detect(
		map[string]float64{"agg": 10, "v1": 10, "v2": 10},
		map[string]float64{"agg": 20, "v1": 5, "v2": 3},
	)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if len(alerts[0].Victims) != 2 {
		t.Errorf("expected 2 victims, got %d", len(alerts[0].Victims))
	}
}

// ── Solver signals ───────────────────────────────────────────────────────────

func TestNoisyNeighbor_IsNoisySignal(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("agg", 10)
	d.RecordUsage("victim", 5)
	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 16)

	d.Detect(
		map[string]float64{"agg": 10, "victim": 10},
		map[string]float64{"agg": 16, "victim": 5},
	)

	if !d.IsNoisy("agg") {
		t.Error("agg should be flagged as noisy")
	}
	if d.IsNoisy("victim") {
		t.Error("victim should not be flagged as noisy")
	}
}

func TestNoisyNeighbor_IsVictimSignal(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("agg", 10)
	d.RecordUsage("victim", 5)
	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 16)

	d.Detect(
		map[string]float64{"agg": 10, "victim": 10},
		map[string]float64{"agg": 16, "victim": 5},
	)

	if !d.IsVictim("victim") {
		t.Error("victim should be flagged")
	}
	if d.IsVictim("agg") {
		t.Error("agg should not be a victim")
	}
}

func TestNoisyNeighbor_SignalExpires(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("agg", 10)
	d.RecordUsage("victim", 5)
	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 16)

	d.Detect(
		map[string]float64{"agg": 10, "victim": 10},
		map[string]float64{"agg": 16, "victim": 5},
	)

	// Advance past 2× window (10 min) → signals expire.
	clk.Advance(11 * time.Minute)

	if d.IsNoisy("agg") {
		t.Error("noisy signal should have expired")
	}
	if d.IsVictim("victim") {
		t.Error("victim signal should have expired")
	}
}

func TestNoisyNeighbor_UnknownTenantNotNoisy(t *testing.T) {
	d := NewNoisyNeighborDetector()
	if d.IsNoisy("nobody") {
		t.Error("unknown tenant should not be noisy")
	}
	if d.IsVictim("nobody") {
		t.Error("unknown tenant should not be victim")
	}
}

// ── Edge cases ───────────────────────────────────────────────────────────────

func TestNoisyNeighbor_NoHistory(t *testing.T) {
	d := NewNoisyNeighborDetector()
	alerts := d.Detect(
		map[string]float64{"a": 10},
		map[string]float64{"a": 5},
	)
	if len(alerts) != 0 {
		t.Error("no history should produce no alerts")
	}
}

func TestNoisyNeighbor_SingleSnapshot(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)
	d.RecordUsage("a", 10)

	alerts := d.Detect(
		map[string]float64{"a": 10, "b": 10},
		map[string]float64{"a": 10, "b": 5},
	)
	if len(alerts) != 0 {
		t.Error("single snapshot should produce no alerts (need at least 2)")
	}
}

func TestNoisyNeighbor_ZeroBaseline(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("a", 0)
	d.RecordUsage("victim", 5)
	clk.Advance(3 * time.Minute)
	d.RecordUsage("a", 10)

	// Growth from 0 → can't compute percentage.
	alerts := d.Detect(
		map[string]float64{"a": 10, "victim": 10},
		map[string]float64{"a": 10, "victim": 5},
	)
	if len(alerts) != 0 {
		t.Error("zero baseline should not trigger alert")
	}
}

func TestNoisyNeighbor_DecreasingUsage(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("a", 20)
	d.RecordUsage("victim", 5)
	clk.Advance(3 * time.Minute)
	d.RecordUsage("a", 10) // decreasing

	alerts := d.Detect(
		map[string]float64{"a": 10, "victim": 10},
		map[string]float64{"a": 10, "victim": 5},
	)
	if len(alerts) != 0 {
		t.Error("decreasing usage should not trigger alert")
	}
}

// ── RecentAlerts ─────────────────────────────────────────────────────────────

func TestNoisyNeighbor_RecentAlertsEmpty(t *testing.T) {
	d := NewNoisyNeighborDetector()
	if len(d.RecentAlerts()) != 0 {
		t.Error("expected empty alerts")
	}
}

func TestNoisyNeighbor_RecentAlertsAccumulate(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	d.RecordUsage("agg", 10)
	d.RecordUsage("victim", 5)
	clk.Advance(3 * time.Minute)
	d.RecordUsage("agg", 16)

	d.Detect(
		map[string]float64{"agg": 10, "victim": 10},
		map[string]float64{"agg": 16, "victim": 5},
	)

	alerts := d.RecentAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
}

// ── FormatAlert ──────────────────────────────────────────────────────────────

func TestFormatAlert(t *testing.T) {
	a := NoisyNeighborAlert{
		Aggressor:       "team-a",
		AggressorGrowth: 0.65,
		Victims:         []string{"team-b", "team-c"},
	}
	s := FormatAlert(a)
	if !strings.Contains(s, "team-a") {
		t.Error("should contain aggressor name")
	}
	if !strings.Contains(s, "65%") {
		t.Error("should contain growth percentage")
	}
	if !strings.Contains(s, "team-b") {
		t.Error("should contain victim names")
	}
}

// ── Window expiry for history ────────────────────────────────────────────────

func TestNoisyNeighbor_OldSnapshotsIgnored(t *testing.T) {
	clk := newClk()
	d := newTestDetector(clk)

	// Record baseline long ago.
	d.RecordUsage("agg", 10)
	d.RecordUsage("victim", 5)

	// Advance past detection window.
	clk.Advance(10 * time.Minute)
	d.RecordUsage("agg", 16)
	d.RecordUsage("victim", 5)

	// Now the oldest snapshot within the window is 16 (just recorded).
	// Only 1 snapshot within window → no growth measurable.
	alerts := d.Detect(
		map[string]float64{"agg": 10, "victim": 10},
		map[string]float64{"agg": 16, "victim": 5},
	)
	if len(alerts) != 0 {
		t.Error("old snapshots outside window should be ignored")
	}
}
