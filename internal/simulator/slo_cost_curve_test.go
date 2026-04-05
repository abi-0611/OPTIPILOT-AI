package simulator

import (
	"context"
	"testing"
	"time"
)

// latencySolverFactory creates solvers parameterized by latency_p99 SLO target.
// Tighter targets (lower latency) → more replicas → higher cost.
// Looser targets (higher latency) → fewer replicas → lower cost.
// Breach occurs when snapshot LatencyP99 > target.
func latencySolverFactory(sloMetric string, sloTarget float64) SolverFunc {
	return func(snap SimulationSnapshot) SimulatedAction {
		breached := snap.LatencyP99 > sloTarget

		// Tighter target → need more replicas to compensate.
		// inverseFactor: lower target → higher replicas.
		replicas := int32(2)
		if sloTarget > 0 {
			factor := 0.200 / sloTarget // baseline: 200ms → 2 replicas
			replicas = int32(2.0 * (1.0 + factor))
			if replicas < 2 {
				replicas = 2
			}
		}

		action := "no_action"
		if snap.CPUUsage > 0.7 {
			replicas += 2
			action = "scale_up"
		}

		cost := float64(replicas) * 0.50
		return SimulatedAction{
			Action:      action,
			Replicas:    replicas,
			CPUCores:    float64(replicas) * 0.25,
			HourlyCost:  cost,
			SLOBreached: breached,
		}
	}
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestSLOCurve_BasicSweep(t *testing.T) {
	// 12 data points over 1 hour.
	cpuKey, cpuData := makeTimeSeries("api-server", "cpu_usage_seconds_total", testStart, 12, testStep,
		[]float64{0.3, 0.4, 0.5, 0.6, 0.75, 0.85, 0.9, 0.8, 0.6, 0.4, 0.3, 0.2})

	latKey := "http_request_duration_seconds_bucket"
	latValues := []float64{0.1, 0.15, 0.2, 0.25, 0.3, 0.35, 0.4, 0.3, 0.2, 0.15, 0.1, 0.08}
	_, latData := makeTimeSeries("api-server", latKey, testStart, 12, testStep, latValues)

	history := &fakeHistory{data: map[string][]DataPoint{
		cpuKey: cpuData,
		latKey: latData,
	}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "api-server",
		Start:     testStart,
		End:       testEnd,
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.050, // 50ms
		MaxTarget: 0.500, // 500ms
		Steps:     5,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(points) != 5 {
		t.Fatalf("expected 5 curve points, got %d", len(points))
	}
}

func TestSLOCurve_TighterTargetCostsMore(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 6, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5})

	latKey := "http_request_duration_seconds_bucket"
	_, latData := makeTimeSeries("svc", latKey, testStart, 6, testStep,
		[]float64{0.2, 0.2, 0.2, 0.2, 0.2, 0.2})

	history := &fakeHistory{data: map[string][]DataPoint{
		cpuKey: cpuData,
		latKey: latData,
	}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testStart.Add(30 * time.Minute),
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.050, // very tight
		MaxTarget: 0.500, // very loose
		Steps:     3,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}

	// Tighter target (first point) should cost more.
	if points[0].ProjectedMonthlyCost <= points[2].ProjectedMonthlyCost {
		t.Errorf("tighter SLO (%.3f) should cost more ($%.0f) than looser (%.3f, $%.0f)",
			points[0].SLOTarget, points[0].ProjectedMonthlyCost,
			points[2].SLOTarget, points[2].ProjectedMonthlyCost)
	}
}

func TestSLOCurve_TighterTargetMoreBreaches(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 6, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5})

	latKey := "http_request_duration_seconds_bucket"
	_, latData := makeTimeSeries("svc", latKey, testStart, 6, testStep,
		[]float64{0.1, 0.2, 0.3, 0.4, 0.3, 0.1})

	history := &fakeHistory{data: map[string][]DataPoint{
		cpuKey: cpuData,
		latKey: latData,
	}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testStart.Add(30 * time.Minute),
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.050, // very tight → many breaches
		MaxTarget: 0.500, // very loose → few/no breaches
		Steps:     3,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Tightest target should have more breaches than loosest.
	if points[0].SLOBreaches < points[2].SLOBreaches {
		t.Errorf("tighter target (%.3f) should have >= breaches (%d) than looser (%.3f, %d)",
			points[0].SLOTarget, points[0].SLOBreaches,
			points[2].SLOTarget, points[2].SLOBreaches)
	}
}

func TestSLOCurve_CompliancePercentInRange(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 4, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5})

	latKey := "http_request_duration_seconds_bucket"
	_, latData := makeTimeSeries("svc", latKey, testStart, 4, testStep,
		[]float64{0.2, 0.2, 0.2, 0.2})

	history := &fakeHistory{data: map[string][]DataPoint{
		cpuKey: cpuData,
		latKey: latData,
	}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testStart.Add(20 * time.Minute),
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.100,
		MaxTarget: 0.500,
		Steps:     3,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for i, p := range points {
		if p.ProjectedCompliancePct < 0 || p.ProjectedCompliancePct > 100 {
			t.Errorf("point %d: compliance %.1f%% out of [0, 100] range", i, p.ProjectedCompliancePct)
		}
	}
}

func TestSLOCurve_AvgReplicasPositive(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 4, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testStart.Add(20 * time.Minute),
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.100,
		MaxTarget: 0.500,
		Steps:     2,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for i, p := range points {
		if p.AvgReplicas < 2 {
			t.Errorf("point %d: AvgReplicas %.1f should be >= 2", i, p.AvgReplicas)
		}
	}
}

func TestSLOCurve_SingleStep(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 4, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testStart.Add(20 * time.Minute),
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.200,
		MaxTarget: 0.200,
		Steps:     1,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	if points[0].SLOTarget != 0.200 {
		t.Errorf("SLOTarget: want 0.200, got %.3f", points[0].SLOTarget)
	}
}

func TestSLOCurve_SLOTargetsAscending(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 4, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testStart.Add(20 * time.Minute),
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.050,
		MaxTarget: 0.500,
		Steps:     5,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for i := 1; i < len(points); i++ {
		if points[i].SLOTarget <= points[i-1].SLOTarget {
			t.Errorf("targets not ascending: point %d (%.3f) <= point %d (%.3f)",
				i, points[i].SLOTarget, i-1, points[i-1].SLOTarget)
		}
	}
}

func TestSLOCurve_ErrorEmptyService(t *testing.T) {
	gen := NewSLOCurveGenerator(&fakeHistory{}, &fakeDecisions{}, latencySolverFactory)
	_, err := gen.Generate(context.Background(), SLOCurveRequest{
		Start:     testStart,
		End:       testEnd,
		SLOMetric: "latency_p99",
		MinTarget: 0.050,
		MaxTarget: 0.500,
		Steps:     5,
	})
	if err == nil {
		t.Error("expected error for empty service")
	}
}

func TestSLOCurve_ErrorInvalidTimeRange(t *testing.T) {
	gen := NewSLOCurveGenerator(&fakeHistory{}, &fakeDecisions{}, latencySolverFactory)
	_, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testEnd,
		End:       testStart,
		SLOMetric: "latency_p99",
		MinTarget: 0.050,
		MaxTarget: 0.500,
		Steps:     5,
	})
	if err == nil {
		t.Error("expected error for end before start")
	}
}

func TestSLOCurve_ErrorMinGteMax(t *testing.T) {
	gen := NewSLOCurveGenerator(&fakeHistory{}, &fakeDecisions{}, latencySolverFactory)
	_, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testEnd,
		SLOMetric: "latency_p99",
		MinTarget: 0.500,
		MaxTarget: 0.050,
		Steps:     5,
	})
	if err == nil {
		t.Error("expected error for min >= max")
	}
}

func TestSLOCurve_DefaultSteps(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 4, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testStart.Add(20 * time.Minute),
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.050,
		MaxTarget: 0.500,
		// Steps: 0 → should default to 10
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(points) != 10 {
		t.Errorf("expected 10 points (default), got %d", len(points))
	}
}

func TestSLOCurve_TotalStepsPerPoint(t *testing.T) {
	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", testStart, 6, testStep,
		[]float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	gen := NewSLOCurveGenerator(history, decisions, latencySolverFactory)
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "svc",
		Start:     testStart,
		End:       testStart.Add(30 * time.Minute),
		Step:      testStep,
		SLOMetric: "latency_p99",
		MinTarget: 0.100,
		MaxTarget: 0.500,
		Steps:     3,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Each point should have same number of steps (same data, same time range).
	for i, p := range points {
		if p.TotalSteps != 6 {
			t.Errorf("point %d: TotalSteps want 6, got %d", i, p.TotalSteps)
		}
	}
}
