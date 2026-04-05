package simulator

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ── Fake providers ──────────────────────────────────────────────────────────

// fakeHistory returns pre-configured time-series data keyed by query substring.
type fakeHistory struct {
	data map[string][]DataPoint
}

func (f *fakeHistory) FetchHistory(_ context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error) {
	// Match by substring to handle parameterized queries.
	for key, points := range f.data {
		if containsSubstr(query, key) {
			// Filter to requested range.
			var filtered []DataPoint
			for _, dp := range points {
				if !dp.Timestamp.Before(start) && !dp.Timestamp.After(end) {
					filtered = append(filtered, dp)
				}
			}
			return filtered, nil
		}
	}
	return nil, nil
}

func containsSubstr(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && findSubstr(s, sub)
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// fakeDecisions returns pre-configured decisions.
type fakeDecisions struct {
	decisions []HistoricalDecision
}

func (f *fakeDecisions) FetchDecisions(_ context.Context, services []string, start, end time.Time) ([]HistoricalDecision, error) {
	svcSet := make(map[string]bool, len(services))
	for _, s := range services {
		svcSet[s] = true
	}
	var result []HistoricalDecision
	for _, d := range f.decisions {
		if svcSet[d.Service] && !d.Timestamp.Before(start) && !d.Timestamp.After(end) {
			result = append(result, d)
		}
	}
	return result, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func makeTimeSeries(svc, metricKey string, start time.Time, count int, step time.Duration, values []float64) (string, []DataPoint) {
	points := make([]DataPoint, count)
	for i := 0; i < count; i++ {
		val := 0.0
		if i < len(values) {
			val = values[i]
		}
		points[i] = DataPoint{
			Timestamp: start.Add(time.Duration(i) * step),
			Value:     val,
		}
	}
	return metricKey, points
}

var (
	testStart = time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
	testEnd   = time.Date(2026, 4, 5, 1, 0, 0, 0, time.UTC)
	testStep  = 5 * time.Minute
)

// simpleSolver is a solver that scales up if CPU > 0.7, down if CPU < 0.3, else no-op.
func simpleSolver(snap SimulationSnapshot) SimulatedAction {
	replicas := snap.Replicas
	if replicas == 0 {
		replicas = 2
	}
	action := "no_action"
	if snap.CPUUsage > 0.7 {
		replicas = replicas + 2
		action = "scale_up"
	} else if snap.CPUUsage < 0.3 {
		if replicas > 2 {
			replicas = replicas - 1
		}
		action = "scale_down"
	}
	cost := float64(replicas) * 0.50  // $0.50/replica/hr
	breached := snap.LatencyP99 > 0.5 // breach if p99 > 500ms
	return SimulatedAction{
		Action:      action,
		Replicas:    replicas,
		CPUCores:    float64(replicas) * 0.25,
		HourlyCost:  cost,
		SLOBreached: breached,
	}
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestSimulator_BasicRun(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("api-server", "cpu_usage_seconds_total", testStart, 12, testStep,
		[]float64{0.3, 0.4, 0.5, 0.6, 0.75, 0.85, 0.9, 0.8, 0.6, 0.4, 0.3, 0.2})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-001",
		Services: []string{"api-server"},
		Start:    testStart,
		End:      testEnd,
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalSteps != 12 {
		t.Errorf("TotalSteps: want 12, got %d", result.TotalSteps)
	}
	if result.ID != "sim-001" {
		t.Errorf("ID: want sim-001, got %q", result.ID)
	}
}

func TestSimulator_DetectsScaleUpAndDown(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 6, testStep,
		[]float64{0.2, 0.5, 0.8, 0.9, 0.4, 0.1})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-002",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(30 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	scaleUps, scaleDowns, noActions := 0, 0, 0
	for _, step := range result.Timeline {
		switch step.Simulated.Action {
		case "scale_up":
			scaleUps++
		case "scale_down":
			scaleDowns++
		case "no_action":
			noActions++
		}
	}
	if scaleUps == 0 {
		t.Error("expected at least one scale_up action")
	}
	if scaleDowns == 0 {
		t.Error("expected at least one scale_down action")
	}
}

func TestSimulator_OriginalDecisionMatching(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 4, testStep,
		[]float64{0.5, 0.6, 0.8, 0.4})

	actualDecisions := []HistoricalDecision{
		{
			ID: "d-001", Timestamp: testStart.Add(10 * time.Minute), Service: "svc",
			Action: "scale_up", Replicas: 6, HourlyCost: 3.0,
		},
	}

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{decisions: actualDecisions}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-003",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(20 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	matched := 0
	for _, step := range result.Timeline {
		if step.Original != nil {
			matched++
		}
	}
	if matched == 0 {
		t.Error("expected at least one step matched to an original decision")
	}
}

func TestSimulator_CostComparison(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 6, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5})

	actualDecisions := []HistoricalDecision{
		{ID: "d-001", Timestamp: testStart, Service: "svc", HourlyCost: 5.0},
		{ID: "d-002", Timestamp: testStart.Add(5 * time.Minute), Service: "svc", HourlyCost: 5.0},
		{ID: "d-003", Timestamp: testStart.Add(10 * time.Minute), Service: "svc", HourlyCost: 5.0},
		{ID: "d-004", Timestamp: testStart.Add(15 * time.Minute), Service: "svc", HourlyCost: 5.0},
		{ID: "d-005", Timestamp: testStart.Add(20 * time.Minute), Service: "svc", HourlyCost: 5.0},
		{ID: "d-006", Timestamp: testStart.Add(25 * time.Minute), Service: "svc", HourlyCost: 5.0},
	}

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{decisions: actualDecisions}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-004",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(30 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.OriginalCost.TotalHourlyCost <= 0 {
		t.Error("original total cost should be positive")
	}
	if result.SimulatedCost.TotalHourlyCost <= 0 {
		t.Error("simulated total cost should be positive")
	}
	// Simulated uses $0.50/replica * 2 = $1.00/step; original is $5.00/step.
	// So simulated should be cheaper.
	if result.CostDeltaPercent >= 0 {
		t.Errorf("simulated should be cheaper than original; delta=%.1f%%", result.CostDeltaPercent)
	}
}

func TestSimulator_SLOBreachCounting(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 4, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5})

	// Inject latency data — some steps breach (>500ms), some don't.
	latKey := "http_request_duration_seconds_bucket"
	latData := []DataPoint{
		{Timestamp: testStart, Value: 0.3},
		{Timestamp: testStart.Add(5 * time.Minute), Value: 0.8},  // > 0.5 → breach
		{Timestamp: testStart.Add(10 * time.Minute), Value: 0.6}, // > 0.5 → breach
		{Timestamp: testStart.Add(15 * time.Minute), Value: 0.2},
	}

	history := &fakeHistory{data: map[string][]DataPoint{
		cpuKey: cpuData,
		latKey: latData,
	}}
	decisions := &fakeDecisions{}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-005",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(20 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.SimulatedSLOBreaches != 2 {
		t.Errorf("SimulatedSLOBreaches: want 2, got %d", result.SimulatedSLOBreaches)
	}
}

func TestSimulator_MultiService(t *testing.T) {
	cpuKey1, cpuData1 := makeTimeSeries("svc-a", "cpu_usage_seconds_total{service=\"svc-a\"}", testStart, 4, testStep,
		[]float64{0.5, 0.6, 0.7, 0.5})
	cpuKey2, cpuData2 := makeTimeSeries("svc-b", "cpu_usage_seconds_total{service=\"svc-b\"}", testStart, 4, testStep,
		[]float64{0.3, 0.3, 0.3, 0.3})

	history := &fakeHistory{data: map[string][]DataPoint{
		cpuKey1: cpuData1,
		cpuKey2: cpuData2,
	}}
	decisions := &fakeDecisions{}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-006",
		Services: []string{"svc-a", "svc-b"},
		Start:    testStart,
		End:      testStart.Add(20 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 4 steps for svc-a + 4 for svc-b = 8 total.
	if result.TotalSteps != 8 {
		t.Errorf("TotalSteps: want 8, got %d", result.TotalSteps)
	}
}

func TestSimulator_EmptyServicesError(t *testing.T) {
	sim := NewSimulator(&fakeHistory{}, &fakeDecisions{}, simpleSolver)
	_, err := sim.Run(context.Background(), SimulationRequest{
		ID:    "sim-err",
		Start: testStart,
		End:   testEnd,
	})
	if err == nil {
		t.Error("expected error for empty services")
	}
}

func TestSimulator_InvalidTimeRangeError(t *testing.T) {
	sim := NewSimulator(&fakeHistory{}, &fakeDecisions{}, simpleSolver)
	_, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-err2",
		Services: []string{"svc"},
		Start:    testEnd,
		End:      testStart, // end before start
	})
	if err == nil {
		t.Error("expected error for end before start")
	}
}

func TestSimulator_DefaultStep(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 2, 5*time.Minute,
		[]float64{0.5, 0.5})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-default-step",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(10 * time.Minute),
		// Step is zero → should default to 5 minutes.
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalSteps != 2 {
		t.Errorf("TotalSteps: want 2, got %d", result.TotalSteps)
	}
}

func TestSimulator_ResultDuration(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 2, testStep,
		[]float64{0.5, 0.5})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-dur",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(1 * time.Hour),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Duration != "1h0m0s" {
		t.Errorf("Duration: want 1h0m0s, got %q", result.Duration)
	}
}

func TestSimulator_PeakCost(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 3, testStep,
		[]float64{0.2, 0.9, 0.2}) // low, high, low

	actualDecisions := []HistoricalDecision{
		{ID: "d-001", Timestamp: testStart, Service: "svc", HourlyCost: 1.0},
		{ID: "d-002", Timestamp: testStart.Add(5 * time.Minute), Service: "svc", HourlyCost: 10.0},
		{ID: "d-003", Timestamp: testStart.Add(10 * time.Minute), Service: "svc", HourlyCost: 1.0},
	}

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{decisions: actualDecisions}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-peak",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(15 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.OriginalCost.PeakHourlyCost != 10.0 {
		t.Errorf("OriginalCost.Peak: want 10.0, got %.1f", result.OriginalCost.PeakHourlyCost)
	}
	if result.SimulatedCost.PeakHourlyCost <= 0 {
		t.Error("SimulatedCost.Peak should be positive")
	}
}

func TestSimulator_DescriptionPassthrough(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 2, testStep,
		[]float64{0.5, 0.5})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:          "sim-desc",
		Services:    []string{"svc"},
		Start:       testStart,
		End:         testStart.Add(10 * time.Minute),
		Step:        testStep,
		Description: "what if we use 100% spot",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Description != "what if we use 100% spot" {
		t.Errorf("Description: want 'what if we use 100%% spot', got %q", result.Description)
	}
}

func TestSimulator_NoHistoryData(t *testing.T) {
	history := &fakeHistory{data: map[string][]DataPoint{}}
	decisions := &fakeDecisions{}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-empty",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testEnd,
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalSteps != 0 {
		t.Errorf("TotalSteps: want 0, got %d", result.TotalSteps)
	}
}

func TestSimulator_SnapshotContainsService(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("checkout", "cpu_usage_seconds_total", testStart, 2, testStep,
		[]float64{0.5, 0.6})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	var capturedService string
	captureSolver := func(snap SimulationSnapshot) SimulatedAction {
		capturedService = snap.Service
		return simpleSolver(snap)
	}

	sim := NewSimulator(history, decisions, captureSolver)
	_, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-svc",
		Services: []string{"checkout"},
		Start:    testStart,
		End:      testStart.Add(10 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if capturedService != "checkout" {
		t.Errorf("solver received service=%q, want 'checkout'", capturedService)
	}
}

func TestSimulator_CustomSolverAlwaysScalesUp(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 4, testStep,
		[]float64{0.1, 0.1, 0.1, 0.1}) // very low CPU

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	aggressiveSolver := func(snap SimulationSnapshot) SimulatedAction {
		return SimulatedAction{
			Action:     "scale_up",
			Replicas:   20,
			HourlyCost: 10.0,
		}
	}

	sim := NewSimulator(history, decisions, aggressiveSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-custom",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(20 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for i, step := range result.Timeline {
		if step.Simulated.Action != "scale_up" {
			t.Errorf("step %d: want scale_up, got %q", i, step.Simulated.Action)
		}
		if step.Simulated.Replicas != 20 {
			t.Errorf("step %d: want 20 replicas, got %d", i, step.Simulated.Replicas)
		}
	}

	wantCost := 10.0 * 4 // 4 steps × $10
	if result.SimulatedCost.TotalHourlyCost != wantCost {
		t.Errorf("SimulatedCost.Total: want %.1f, got %.1f", wantCost, result.SimulatedCost.TotalHourlyCost)
	}
}

func TestSimulator_OriginalSLOBreaches(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 3, testStep,
		[]float64{0.5, 0.5, 0.5})

	actualDecisions := []HistoricalDecision{
		{ID: "d-001", Timestamp: testStart, Service: "svc", HourlyCost: 2.0, SLOBreached: true},
		{ID: "d-002", Timestamp: testStart.Add(5 * time.Minute), Service: "svc", HourlyCost: 2.0, SLOBreached: false},
		{ID: "d-003", Timestamp: testStart.Add(10 * time.Minute), Service: "svc", HourlyCost: 2.0, SLOBreached: true},
	}

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{decisions: actualDecisions}

	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "sim-orig-breach",
		Services: []string{"svc"},
		Start:    testStart,
		End:      testStart.Add(15 * time.Minute),
		Step:     testStep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.OriginalSLOBreaches != 2 {
		t.Errorf("OriginalSLOBreaches: want 2, got %d", result.OriginalSLOBreaches)
	}
}

// Verify unused import suppression.
var _ = fmt.Sprintf
