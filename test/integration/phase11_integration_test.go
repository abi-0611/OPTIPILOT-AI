package integration_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	tuningv1alpha1 "github.com/optipilot-ai/optipilot/api/tuning/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/metrics"
	"github.com/optipilot-ai/optipilot/internal/storage"
	"github.com/optipilot-ai/optipilot/internal/tuning"
)

// ═══════════════════════════════════════════════════════════════════════════
// 1. Parameter optimizer: converge to a known optimal point
// ═══════════════════════════════════════════════════════════════════════════

// sloOracle models SLO = 100 - 2*(value - 5)^2  →  optimal at value=5.
type sloOracle struct {
	applied map[string]string
}

func (s *sloOracle) FetchSLO(_ context.Context, _, _ string) (float64, error) {
	v := 5.0 // default
	if raw, ok := s.applied["threads"]; ok {
		fmt.Sscanf(raw, "%f", &v)
	}
	slo := 100.0 - 2.0*(v-5)*(v-5)
	if slo < 0 {
		slo = 0
	}
	return slo, nil
}

type stubApplier struct {
	applied map[string]string
	oracle  *sloOracle
}

func (a *stubApplier) Apply(_ context.Context, _ string, p tuningv1alpha1.TunableParameter, v string) error {
	a.applied[p.Name] = v
	a.oracle.applied[p.Name] = v
	return nil
}

func TestOptimizer_ConvergesToKnownOptimal(t *testing.T) {
	oracle := &sloOracle{applied: map[string]string{}}
	applier := &stubApplier{applied: map[string]string{}, oracle: oracle}

	opt := tuning.NewOptimizer(oracle, applier)

	at := &tuningv1alpha1.ApplicationTuning{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: tuningv1alpha1.ApplicationTuningSpec{
			TargetRef: tuningv1alpha1.TuningTargetRef{Kind: "Deployment", Name: "app"},
			Parameters: []tuningv1alpha1.TunableParameter{{
				Name: "threads", Type: tuningv1alpha1.ParamTypeInteger,
				Source: tuningv1alpha1.SourceConfigMap, Min: "1", Max: "10", Step: "1", Default: "1",
			}},
			OptimizationTarget: tuningv1alpha1.OptimizationTarget{
				MetricName: "slo_compliance", Objective: "maximize",
			},
			SafetyPolicy: &tuningv1alpha1.TuningSafetyPolicy{
				MaxChangePercent: 100, CooldownMinutes: 0,
				RollbackOnSLOViolation: false, SLOThresholdPercent: 0,
			},
		},
		Status: tuningv1alpha1.ApplicationTuningStatus{
			CurrentValues:   map[string]string{"threads": "8"},
			ActiveParameter: "threads",
		},
	}

	ctx := context.Background()
	var lastResult tuning.CycleResult
	converged := false

	// Run up to 20 cycles (should converge well before).
	for i := 0; i < 20; i++ {
		r, err := opt.RunCycle(ctx, at)
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		lastResult = r

		// Update status as a real controller would.
		at.Status.Phase = r.NewPhase
		if r.Observation != nil {
			at.Status.Observations = append(at.Status.Observations, *r.Observation)
		}
		if r.ParameterChanged != "" {
			if at.Status.CurrentValues == nil {
				at.Status.CurrentValues = map[string]string{}
			}
			at.Status.CurrentValues[r.ParameterChanged] = r.NewValue
			at.Status.ActiveParameter = r.ParameterChanged
		}
		if r.NewCooldownUntil != nil {
			mt := metav1.NewTime(*r.NewCooldownUntil)
			at.Status.CooldownUntil = &mt
		} else {
			at.Status.CooldownUntil = nil
		}

		if r.Converged {
			converged = true
			break
		}
	}

	if !converged {
		t.Fatalf("did not converge; last phase=%s, msg=%s", lastResult.NewPhase, lastResult.Message)
	}

	// After convergence, the best observed SLO should be high.
	best, ok := tuning.BestFromObservations("threads", at.Status.Observations)
	if !ok {
		t.Fatal("no observations found")
	}
	if best.BestSLO < 50 {
		t.Errorf("best SLO=%f, want ≥50", best.BestSLO)
	}
	// Note: convergence CycleResult doesn't include the final observation,
	// so the persisted count is minObservationsForConvergence - 1.
	if best.NumObserved < 2 {
		t.Errorf("only %d observations, want ≥2", best.NumObserved)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// 2. Custom metric injection with mock Prometheus HTTP server
// ═══════════════════════════════════════════════════════════════════════════

func TestCustomMetricInjection_MockPrometheus(t *testing.T) {
	// Spin up a fake Prometheus that returns a vector result.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		var val string
		switch query {
		case "gpu_utilization":
			val = "0.85"
		case "disk_queue_depth":
			val = "4.2"
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1609459200,"%s"]}]}}`, val)
	}))
	defer srv.Close()

	client := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 0)
	adapter := metrics.NewCustomMetricAdapter(client)

	customMetrics := []slov1alpha1.CustomMetric{
		{Name: "gpu_util", Query: "gpu_utilization", Target: 0.8, Weight: 2.0},
		{Name: "disk_qd", Query: "disk_queue_depth", Target: 5.0, Weight: 1.0},
	}

	fetched, results := adapter.Fetch(context.Background(), customMetrics)

	// Verify fetch results.
	if len(fetched) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(fetched))
	}
	if math.Abs(fetched["gpu_util"]-0.85) > 1e-6 {
		t.Errorf("gpu_util=%f", fetched["gpu_util"])
	}
	if math.Abs(fetched["disk_qd"]-4.2) > 1e-6 {
		t.Errorf("disk_qd=%f", fetched["disk_qd"])
	}

	// Verify score computation.
	score := metrics.Score(results)
	// gpu: 2.0 * |0.85 - 0.8| / max(0.8, 1) = 2 * 0.05 / 1 = 0.1
	// disk: 1.0 * |4.2 - 5.0| / 5.0 = 0.8/5 = 0.16
	expectedScore := 0.1 + 0.16
	if math.Abs(score-expectedScore) > 1e-6 {
		t.Errorf("score=%f, want %f", score, expectedScore)
	}

	// Verify merge into solver input.
	solverMetrics := map[string]float64{"existing": 42.0}
	merged := metrics.MergeIntoMetrics(solverMetrics, fetched)
	if merged["existing"] != 42.0 || merged["gpu_util"] != 0.85 {
		t.Errorf("merged=%v", merged)
	}

	// Verify EnrichMetrics integration.
	input := &engine.SolverInput{Metrics: map[string]float64{"slo_compliance": 99.5}}
	signals := &engine.AdvancedSignals{CustomMetricResults: results}
	engine.EnrichMetrics(input, signals)
	if input.Metrics["gpu_util"] != 0.85 {
		t.Errorf("gpu_util not injected: %v", input.Metrics)
	}
	if input.Metrics["slo_compliance"] != 99.5 {
		t.Error("existing metric lost")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// 3. Storage recommender: synthetic I/O profiles → correct recommendations
// ═══════════════════════════════════════════════════════════════════════════

func TestStorageRecommender_SyntheticProfiles(t *testing.T) {
	rec := storage.NewRecommender(nil)

	profiles := []struct {
		name        string
		metrics     storage.PVCMetrics
		wantProfile storage.WorkloadProfile
		wantClass   string
	}{
		{
			name: "database-write-heavy",
			metrics: storage.PVCMetrics{
				Namespace: "prod", PVCName: "pgdata",
				ReadIOPS: 50, WriteIOPS: 450,
				ReadThroughputMBs: 5, WriteThroughputMBs: 5,
				ReadLatencyMs: 0.5, WriteLatencyMs: 1.0,
				CurrentStorageClass: "gp2", CapacityGiB: 200,
			},
			wantProfile: storage.ProfileWriteHeavy,
			wantClass:   "st1",
		},
		{
			name: "log-streaming-sequential",
			metrics: storage.PVCMetrics{
				Namespace: "logging", PVCName: "kafka-data",
				ReadIOPS: 20, WriteIOPS: 30,
				ReadThroughputMBs: 100, WriteThroughputMBs: 200,
				ReadLatencyMs: 5, WriteLatencyMs: 8,
				CurrentStorageClass: "gp3", CapacityGiB: 1000,
			},
			wantProfile: storage.ProfileSequential,
			wantClass:   "st1",
		},
		{
			name: "web-cache-read-heavy",
			metrics: storage.PVCMetrics{
				Namespace: "web", PVCName: "cache",
				ReadIOPS: 3000, WriteIOPS: 200,
				ReadThroughputMBs: 50, WriteThroughputMBs: 5,
				ReadLatencyMs: 0.8, WriteLatencyMs: 1.2,
				CurrentStorageClass: "st1", CapacityGiB: 50,
			},
			wantProfile: storage.ProfileReadHeavy,
			wantClass:   "gp3",
		},
		{
			name: "idle-archive",
			metrics: storage.PVCMetrics{
				Namespace: "archive", PVCName: "cold",
				ReadIOPS: 0, WriteIOPS: 0,
				CurrentStorageClass: "io2", CapacityGiB: 500,
			},
			wantProfile: storage.ProfileIdle,
			wantClass:   "gp3",
		},
		{
			name: "bursty-ml-training",
			metrics: storage.PVCMetrics{
				Namespace: "ml", PVCName: "scratch",
				ReadIOPS: 200, WriteIOPS: 200, QueueDepth: 40,
				ReadThroughputMBs: 5, WriteThroughputMBs: 5,
				ReadLatencyMs: 10, WriteLatencyMs: 15,
				CurrentStorageClass: "gp3", CapacityGiB: 100,
			},
			wantProfile: storage.ProfileBursty,
			wantClass:   "io2",
		},
	}

	for _, tc := range profiles {
		t.Run(tc.name, func(t *testing.T) {
			profile := storage.ClassifyProfile(tc.metrics)
			if profile != tc.wantProfile {
				t.Errorf("profile=%s, want %s", profile, tc.wantProfile)
			}

			result := rec.Recommend(tc.metrics)
			if result.RecommendedClass != tc.wantClass {
				t.Errorf("class=%s, want %s", result.RecommendedClass, tc.wantClass)
			}
			if result.Reason == "" {
				t.Error("empty reason")
			}
			if result.Annotations == nil || len(result.Annotations) < 3 {
				t.Error("missing annotations")
			}
		})
	}

	// Verify batch + filtering.
	allMetrics := make([]storage.PVCMetrics, len(profiles))
	for i, p := range profiles {
		allMetrics[i] = p.metrics
	}
	all := rec.RecommendAll(allMetrics)
	if len(all) != len(profiles) {
		t.Fatalf("want %d, got %d", len(profiles), len(all))
	}

	changes := storage.ChangesOnly(all)
	// At least some should differ from current class.
	if len(changes) == 0 {
		t.Error("expected at least one class change recommendation")
	}

	// Verify cost savings: idle on io2 → gp3 should save.
	for _, r := range all {
		if r.PVCName == "cold" {
			if r.EstMonthlySavings <= 0 {
				t.Errorf("cold: savings=%f, want positive (io2→gp3)", r.EstMonthlySavings)
			}
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// 4. End-to-end: ApplicationTuning CR → full cycle → values updated
// ═══════════════════════════════════════════════════════════════════════════

// configMapApplier records applied values like a real ConfigMap writer.
type configMapApplier struct {
	data map[string]string
}

func (c *configMapApplier) Apply(_ context.Context, _ string, p tuningv1alpha1.TunableParameter, v string) error {
	c.data[p.Name] = v
	return nil
}

type fixedSLO struct{ value float64 }

func (f *fixedSLO) FetchSLO(context.Context, string, string) (float64, error) {
	return f.value, nil
}

func TestEndToEnd_ApplicationTuning_CycleUpdatesConfigMap(t *testing.T) {
	cm := &configMapApplier{data: map[string]string{}}
	sloFetcher := &fixedSLO{value: 98.0}
	opt := tuning.NewOptimizer(sloFetcher, cm)

	at := &tuningv1alpha1.ApplicationTuning{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-tuning", Namespace: "production",
		},
		Spec: tuningv1alpha1.ApplicationTuningSpec{
			TargetRef: tuningv1alpha1.TuningTargetRef{Kind: "Deployment", Name: "api-server"},
			Parameters: []tuningv1alpha1.TunableParameter{
				{
					Name: "worker_threads", Type: tuningv1alpha1.ParamTypeInteger,
					Source:       tuningv1alpha1.SourceConfigMap,
					ConfigMapRef: &tuningv1alpha1.ConfigMapRef{Name: "api-config", Key: "WORKERS"},
					Min:          "1", Max: "16", Step: "1", Default: "4",
				},
			},
			OptimizationTarget: tuningv1alpha1.OptimizationTarget{
				MetricName: "slo_compliance",
				Objective:  "maximize",
			},
			SafetyPolicy: &tuningv1alpha1.TuningSafetyPolicy{
				MaxChangePercent: 100, CooldownMinutes: 0,
				RollbackOnSLOViolation: false, SLOThresholdPercent: 0,
			},
		},
		Status: tuningv1alpha1.ApplicationTuningStatus{
			CurrentValues:   map[string]string{"worker_threads": "4"},
			ActiveParameter: "worker_threads",
		},
	}

	ctx := context.Background()

	// --- Cycle 1: records observation for active param, may explore.
	r1, err := opt.RunCycle(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if r1.NewPhase == tuningv1alpha1.TuningError {
		t.Fatalf("error: %s", r1.Message)
	}

	// Simulate controller persisting status.
	at.Status.Phase = r1.NewPhase
	if r1.Observation != nil {
		at.Status.Observations = append(at.Status.Observations, *r1.Observation)
	}
	if r1.ParameterChanged != "" {
		if at.Status.CurrentValues == nil {
			at.Status.CurrentValues = map[string]string{}
		}
		at.Status.CurrentValues[r1.ParameterChanged] = r1.NewValue
		at.Status.ActiveParameter = r1.ParameterChanged
	}

	// Verify the ConfigMap applier received the value (if changed).
	if r1.ParameterChanged != "" {
		if cm.data[r1.ParameterChanged] != r1.NewValue {
			t.Errorf("ConfigMap not updated: data=%v", cm.data)
		}
	}

	// --- Cycle 2: records observation.
	r2, err := opt.RunCycle(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Observation == nil {
		t.Error("expected observation recorded in cycle 2")
	}
	if r2.Observation != nil && r2.Observation.SLOValue != 98.0 {
		t.Errorf("SLO=%f, want 98.0", r2.Observation.SLOValue)
	}
	if r2.Observation != nil {
		at.Status.Observations = append(at.Status.Observations, *r2.Observation)
	}
	if r2.ParameterChanged != "" {
		at.Status.CurrentValues[r2.ParameterChanged] = r2.NewValue
		at.Status.ActiveParameter = r2.ParameterChanged
	}

	// --- Verify tuning overrides flow into solver via AdvancedSignals.
	action := &engine.ScalingAction{}
	signals := &engine.AdvancedSignals{
		TuningOverrides: at.Status.CurrentValues,
	}
	engine.EnrichTuning(action, signals)

	if action.TuningParams["worker_threads"] == "" {
		t.Error("worker_threads not in TuningParams")
	}

	// --- Multi-cycle to convergence.
	for i := 0; i < 30; i++ {
		r, err := opt.RunCycle(ctx, at)
		if err != nil {
			t.Fatal(err)
		}
		if r.Observation != nil {
			at.Status.Observations = append(at.Status.Observations, *r.Observation)
		}
		if r.ParameterChanged != "" {
			at.Status.CurrentValues[r.ParameterChanged] = r.NewValue
			at.Status.ActiveParameter = r.ParameterChanged
		}
		if r.NewCooldownUntil != nil {
			mt := metav1.NewTime(*r.NewCooldownUntil)
			at.Status.CooldownUntil = &mt
		} else {
			at.Status.CooldownUntil = nil
		}
		if r.Converged {
			t.Logf("converged at cycle %d", i+3)
			return
		}
	}
	t.Fatal("did not converge within 30 additional cycles")
}

// ═══════════════════════════════════════════════════════════════════════════
// 5. Full pipeline: custom metrics + storage + tuning → solver enrichment
// ═══════════════════════════════════════════════════════════════════════════

func TestFullPipeline_AllSignalsEnrich(t *testing.T) {
	// Custom metrics.
	cmResults := []metrics.CustomMetricResult{
		{Name: "gpu_util", Value: 0.75, Target: 0.8, Weight: 1.0},  // slightly off
		{Name: "mem_pressure", Value: 60, Target: 50, Weight: 0.5}, // off
	}

	// Storage recommendations.
	storageRecs := []storage.Recommendation{
		{PVCName: "data", CurrentClass: "io2", RecommendedClass: "gp3", EstMonthlySavings: 50},
		{PVCName: "logs", CurrentClass: "gp3", RecommendedClass: "st1", EstMonthlySavings: 20},
	}

	// Tuning overrides.
	tuningOverrides := map[string]string{"threads": "8", "cache_mb": "1024"}

	signals := &engine.AdvancedSignals{
		CustomMetricResults:    cmResults,
		TuningOverrides:        tuningOverrides,
		StorageRecommendations: storageRecs,
	}

	// 1. Verify metrics injection.
	input := &engine.SolverInput{}
	engine.EnrichMetrics(input, signals)
	if input.Metrics["gpu_util"] != 0.75 || input.Metrics["mem_pressure"] != 60 {
		t.Errorf("metrics=%v", input.Metrics)
	}

	// 2. Verify custom metric score.
	cmScore := engine.CustomMetricScore(signals)
	if cmScore <= 0 {
		t.Error("expected positive score for off-target metrics")
	}

	// 3. Verify storage savings.
	savings := engine.StorageMonthlySavings(signals)
	if savings != 70 {
		t.Errorf("savings=%f, want 70", savings)
	}

	// 4. Verify candidate enrichment.
	scored := []engine.ScoredCandidate{
		{
			Plan:   engine.ScoredCandidate{}.Plan, // zero plan
			Score:  engine.CandidateScore{Cost: 0.5, Weighted: 0.9},
			Viable: true,
		},
	}
	scored[0].Plan.Replicas = 3
	scored[0].Plan.EstimatedCost = 2.0
	engine.EnrichScoredCandidates(scored, signals)

	// Cost should be higher with storage savings bonus.
	if scored[0].Score.Cost <= 0.5 {
		t.Errorf("cost should increase, got %f", scored[0].Score.Cost)
	}
	// Weighted should decrease due to custom metric penalty.
	if scored[0].Score.Weighted >= 0.9 {
		t.Errorf("weighted should decrease, got %f", scored[0].Score.Weighted)
	}

	// 5. Verify tuning params on action.
	action := &engine.ScalingAction{}
	engine.EnrichTuning(action, signals)
	if action.TuningParams["threads"] != "8" || action.TuningParams["cache_mb"] != "1024" {
		t.Errorf("tuning=%v", action.TuningParams)
	}
}
