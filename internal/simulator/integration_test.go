package simulator

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ── Integration test helpers ─────────────────────────────────────────────────

// buildMultiServiceHistory creates deterministic metric data for N services
// over the given time range. CPU ramps 0.3→0.9 across the window.
func buildMultiServiceHistory(services []string, start time.Time, count int, step time.Duration) *fakeHistory {
	data := make(map[string][]DataPoint)

	for idx, svc := range services {
		// Keys must be substrings of the actual PromQL queries emitted by simulator.Run.
		// CPU:     avg(rate(container_cpu_usage_seconds_total{service="svc"}[5m]))
		// Latency: histogram_quantile(0.99, rate(http_request_duration_seconds_bucket{service="svc"}[5m]))
		// Errors:  rate(http_requests_total{service="svc",code=~"5.."}[5m])
		// RPS:     rate(http_requests_total{service="svc"}[5m])
		cpuKey := `container_cpu_usage_seconds_total{service="` + svc + `"}`
		latKey := `http_request_duration_seconds_bucket{service="` + svc + `"}`
		errKey := `http_requests_total{service="` + svc + `",code=~"5.."}`
		rpsKey := `http_requests_total{service="` + svc + `"}`

		cpuPts := make([]DataPoint, count)
		latPts := make([]DataPoint, count)
		errPts := make([]DataPoint, count)
		rpsPts := make([]DataPoint, count)

		for i := 0; i < count; i++ {
			ts := start.Add(time.Duration(i) * step)
			// CPU oscillates 0.3–0.9 with phase offset per service.
			phase := float64(idx) / float64(len(services))
			cpu := 0.3 + 0.6*float64((i+int(phase*float64(count)))%count)/float64(count)
			cpuPts[i] = DataPoint{Timestamp: ts, Value: cpu}
			latPts[i] = DataPoint{Timestamp: ts, Value: 0.1 + cpu*0.3}
			errPts[i] = DataPoint{Timestamp: ts, Value: cpu * 0.01}
			rpsPts[i] = DataPoint{Timestamp: ts, Value: 100 + cpu*200}
		}

		data[cpuKey] = cpuPts
		data[latKey] = latPts
		data[errKey] = errPts
		data[rpsKey] = rpsPts
	}

	return &fakeHistory{data: data}
}

// buildMultiServiceDecisions creates one actual decision per service per step above 0.7 CPU.
func buildMultiServiceDecisions(services []string, start time.Time, count int, step time.Duration) *fakeDecisions {
	var decisions []HistoricalDecision
	for idx, svc := range services {
		phase := float64(idx) / float64(len(services))
		for i := 0; i < count; i++ {
			cpu := 0.3 + 0.6*float64((i+int(phase*float64(count)))%count)/float64(count)
			if cpu > 0.7 {
				decisions = append(decisions, HistoricalDecision{
					ID:          fmt.Sprintf("%s-d%d", svc, i),
					Timestamp:   start.Add(time.Duration(i) * step),
					Service:     svc,
					Namespace:   "default",
					Action:      "scale_up",
					Replicas:    4,
					CPUCores:    1.0,
					HourlyCost:  2.5,
					SLOBreached: false,
				})
			}
		}
	}
	return &fakeDecisions{decisions: decisions}
}

// ── Integration tests ────────────────────────────────────────────────────────

// TestIntegration_EndToEnd verifies the full pipeline:
// Journal → Narrator → Simulator → SLO-Cost Curve
func TestIntegration_EndToEnd(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	step := 5 * time.Minute
	nSteps := 12

	services := []string{"api-server", "worker"}
	history := buildMultiServiceHistory(services, start, nSteps, step)
	decisions := buildMultiServiceDecisions(services, start, nSteps, step)

	// 1. Simulator: run with a conservative solver that never scales up.
	conservativeSolver := func(snap SimulationSnapshot) SimulatedAction {
		return SimulatedAction{
			Action:      "no_action",
			Replicas:    2,
			CPUCores:    0.5,
			HourlyCost:  0.80,
			SLOBreached: snap.CPUUsage > 0.85,
		}
	}

	sim := NewSimulator(history, decisions, conservativeSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:          "e2e-test",
		Services:    services,
		Start:       start,
		End:         end,
		Step:        step,
		Description: "conservative solver vs actual",
	})
	if err != nil {
		t.Fatalf("simulation failed: %v", err)
	}

	if result.TotalSteps == 0 {
		t.Error("expected at least one simulation step")
	}
	if result.ID != "e2e-test" {
		t.Errorf("ID: want e2e-test, got %s", result.ID)
	}
	if result.Description != "conservative solver vs actual" {
		t.Errorf("Description not propagated: %s", result.Description)
	}
	if result.SimulatedCost.AvgHourlyCost <= 0 {
		t.Error("expected positive simulated cost")
	}

	// 2. SLO-Cost Curve: confirm curve has expected number of points.
	factory := func(sloMetric string, sloTarget float64) SolverFunc {
		return func(snap SimulationSnapshot) SimulatedAction {
			breached := snap.LatencyP99 > sloTarget
			replicas := int32(2)
			if breached {
				replicas = 4
			}
			return SimulatedAction{
				Action:      "no_action",
				Replicas:    replicas,
				CPUCores:    float64(replicas) * 0.25,
				HourlyCost:  float64(replicas) * 0.5,
				SLOBreached: breached,
			}
		}
	}

	gen := NewSLOCurveGenerator(history, decisions, factory)
	for _, svc := range services {
		points, err := gen.Generate(context.Background(), SLOCurveRequest{
			Service:   svc,
			Start:     start,
			End:       end,
			Step:      step,
			SLOMetric: "latency_p99",
			MinTarget: 0.05,
			MaxTarget: 0.50,
			Steps:     5,
		})
		if err != nil {
			t.Fatalf("SLO curve for %s: %v", svc, err)
		}
		if len(points) != 5 {
			t.Errorf("svc %s: expected 5 curve points, got %d", svc, len(points))
		}
		for i, p := range points {
			if p.ProjectedMonthlyCost <= 0 {
				t.Errorf("svc %s point %d: expected positive monthly cost", svc, i)
			}
			if p.ProjectedCompliancePct < 0 || p.ProjectedCompliancePct > 100 {
				t.Errorf("svc %s point %d: compliance %.1f%% out of range", svc, i, p.ProjectedCompliancePct)
			}
		}
	}
}

func TestIntegration_MultiService_CostComparison(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	step := 5 * time.Minute
	nSteps := 24

	services := []string{"frontend", "backend", "cache"}
	history := buildMultiServiceHistory(services, start, nSteps, step)
	decisions := buildMultiServiceDecisions(services, start, nSteps, step)

	// Expensive solver: always adds 4 replicas.
	expensiveSolver := func(snap SimulationSnapshot) SimulatedAction {
		return SimulatedAction{
			Action:     "scale_up",
			Replicas:   6,
			CPUCores:   1.5,
			HourlyCost: 3.0,
		}
	}

	// Cheap solver: only 2 replicas regardless of load.
	cheapSolver := func(snap SimulationSnapshot) SimulatedAction {
		return SimulatedAction{
			Action:     "no_action",
			Replicas:   2,
			CPUCores:   0.5,
			HourlyCost: 0.5,
		}
	}

	ctx := context.Background()

	expResult, err := NewSimulator(history, decisions, expensiveSolver).Run(ctx, SimulationRequest{
		ID: "expensive", Services: services, Start: start, End: end, Step: step,
	})
	if err != nil {
		t.Fatalf("expensive sim: %v", err)
	}

	cheapResult, err := NewSimulator(history, decisions, cheapSolver).Run(ctx, SimulationRequest{
		ID: "cheap", Services: services, Start: start, End: end, Step: step,
	})
	if err != nil {
		t.Fatalf("cheap sim: %v", err)
	}

	if expResult.SimulatedCost.AvgHourlyCost <= cheapResult.SimulatedCost.AvgHourlyCost {
		t.Errorf("expensive solver (avg $%.2f/h) should cost more than cheap ($%.2f/h)",
			expResult.SimulatedCost.AvgHourlyCost, cheapResult.SimulatedCost.AvgHourlyCost)
	}
}

func TestIntegration_SLOBreachPropagation(t *testing.T) {
	start := testStart
	end := testEnd
	step := testStep
	nSteps := 12

	cpuKey, cpuData := makeTimeSeries("svc", "cpu_usage_seconds_total", start, nSteps, step,
		[]float64{0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9})

	history := &fakeHistory{data: map[string][]DataPoint{cpuKey: cpuData}}
	decisions := &fakeDecisions{}

	// Solver that always reports SLO breached.
	breachSolver := func(snap SimulationSnapshot) SimulatedAction {
		return SimulatedAction{
			Action:      "scale_up",
			Replicas:    4,
			CPUCores:    1.0,
			HourlyCost:  2.0,
			SLOBreached: true,
		}
	}

	result, err := NewSimulator(history, decisions, breachSolver).Run(context.Background(), SimulationRequest{
		ID: "breach-test", Services: []string{"svc"}, Start: start, End: end, Step: step,
	})
	if err != nil {
		t.Fatalf("sim: %v", err)
	}
	if result.SimulatedSLOBreaches != result.TotalSteps {
		t.Errorf("expected all %d steps to be SLO breaches, got %d", result.TotalSteps, result.SimulatedSLOBreaches)
	}
}

func TestIntegration_CurveMonotonicity_5Services(t *testing.T) {
	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	step := 5 * time.Minute
	nSteps := 6

	services := []string{"svc-a", "svc-b", "svc-c", "svc-d", "svc-e"}
	history := buildMultiServiceHistory(services, start, nSteps, step)
	decisions := &fakeDecisions{}

	factory := func(sloMetric string, sloTarget float64) SolverFunc {
		return func(snap SimulationSnapshot) SimulatedAction {
			// Tighter target → more replicas.
			replicas := int32(2 + int(0.2/sloTarget))
			if replicas < 2 {
				replicas = 2
			}
			return SimulatedAction{
				Action:      "no_action",
				Replicas:    replicas,
				CPUCores:    float64(replicas) * 0.25,
				HourlyCost:  float64(replicas) * 0.5,
				SLOBreached: snap.LatencyP99 > sloTarget,
			}
		}
	}

	gen := NewSLOCurveGenerator(history, decisions, factory)

	for _, svc := range services {
		points, err := gen.Generate(context.Background(), SLOCurveRequest{
			Service:   svc,
			Start:     start,
			End:       end,
			Step:      step,
			SLOMetric: "latency_p99",
			MinTarget: 0.05,
			MaxTarget: 0.50,
			Steps:     5,
		})
		if err != nil {
			t.Fatalf("%s: curve error: %v", svc, err)
		}

		// Costs should be monotonically non-increasing as target relaxes.
		for i := 1; i < len(points); i++ {
			if points[i].ProjectedMonthlyCost > points[i-1].ProjectedMonthlyCost {
				// Allow tiny floating-point tolerance.
				diff := points[i].ProjectedMonthlyCost - points[i-1].ProjectedMonthlyCost
				if diff > 0.01 {
					t.Errorf("%s: cost not monotonic at step %d: %.4f > %.4f",
						svc, i, points[i].ProjectedMonthlyCost, points[i-1].ProjectedMonthlyCost)
				}
			}
		}
	}
}

// ── Performance benchmark ────────────────────────────────────────────────────

// BenchmarkSimulator_24h_5Services benchmarks simulating 24h of 5-service
// history at 5-minute intervals (288 steps × 5 services = 1440 data points).
func BenchmarkSimulator_24h_5Services(b *testing.B) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	step := 5 * time.Minute
	nSteps := int(end.Sub(start) / step) // 288

	services := []string{"svc-1", "svc-2", "svc-3", "svc-4", "svc-5"}
	history := buildMultiServiceHistory(services, start, nSteps, step)
	decisions := buildMultiServiceDecisions(services, start, nSteps, step)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sim := NewSimulator(history, decisions, simpleSolver)
		_, err := sim.Run(context.Background(), SimulationRequest{
			ID:       fmt.Sprintf("bench-%d", i),
			Services: services,
			Start:    start,
			End:      end,
			Step:     step,
		})
		if err != nil {
			b.Fatalf("bench run failed: %v", err)
		}
	}
}

// TestPerf_Simulation_24h_5Services_Under30s verifies the perf target:
// simulate 24h of 5-service history in under 30 seconds.
func TestPerf_Simulation_24h_5Services_Under30s(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	step := 5 * time.Minute
	nSteps := int(end.Sub(start) / step) // 288

	services := []string{"svc-1", "svc-2", "svc-3", "svc-4", "svc-5"}
	history := buildMultiServiceHistory(services, start, nSteps, step)
	decisions := buildMultiServiceDecisions(services, start, nSteps, step)

	t0 := time.Now()
	sim := NewSimulator(history, decisions, simpleSolver)
	result, err := sim.Run(context.Background(), SimulationRequest{
		ID:       "perf-test",
		Services: services,
		Start:    start,
		End:      end,
		Step:     step,
	})
	elapsed := time.Since(t0)

	if err != nil {
		t.Fatalf("simulation failed: %v", err)
	}
	if result.TotalSteps == 0 {
		t.Error("expected steps > 0")
	}
	if elapsed > 30*time.Second {
		t.Errorf("performance: 24h 5-service simulation took %v, want < 30s", elapsed)
	}
	t.Logf("24h 5-service simulation: %d steps in %v (%.1f steps/ms)",
		result.TotalSteps, elapsed, float64(result.TotalSteps)/float64(elapsed.Milliseconds()+1))
}

// TestPerf_SLOCurve_10Steps_Under10s verifies SLO-cost curve generation
// over 10 sweep points completes in under 10 seconds.
func TestPerf_SLOCurve_10Steps_Under10s(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	step := 5 * time.Minute
	nSteps := int(end.Sub(start) / step)

	history := buildMultiServiceHistory([]string{"perf-svc"}, start, nSteps, step)
	decisions := buildMultiServiceDecisions([]string{"perf-svc"}, start, nSteps, step)

	factory := func(_ string, sloTarget float64) SolverFunc {
		return func(snap SimulationSnapshot) SimulatedAction {
			replicas := int32(2)
			breached := snap.LatencyP99 > sloTarget
			if breached {
				replicas = 4
			}
			return SimulatedAction{
				Action:      "no_action",
				Replicas:    replicas,
				CPUCores:    float64(replicas) * 0.25,
				HourlyCost:  float64(replicas) * 0.5,
				SLOBreached: breached,
			}
		}
	}

	gen := NewSLOCurveGenerator(history, decisions, factory)

	t0 := time.Now()
	points, err := gen.Generate(context.Background(), SLOCurveRequest{
		Service:   "perf-svc",
		Start:     start,
		End:       end,
		Step:      step,
		SLOMetric: "latency_p99",
		MinTarget: 0.05,
		MaxTarget: 0.50,
		Steps:     10,
	})
	elapsed := time.Since(t0)

	if err != nil {
		t.Fatalf("curve generation failed: %v", err)
	}
	if len(points) != 10 {
		t.Errorf("expected 10 points, got %d", len(points))
	}
	if elapsed > 10*time.Second {
		t.Errorf("SLO curve 10 steps took %v, want < 10s", elapsed)
	}
	t.Logf("SLO-cost curve (10 steps, 24h window): %v", elapsed)
}
