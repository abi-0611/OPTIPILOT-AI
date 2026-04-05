package engine_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/explainability"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestIntegration_Phase2_3_4_EndToEnd wires Phases 2+3+4 together:
//   - Phase 2: SLO state + burn-rate context (mock data as SolverInput)
//   - Phase 3: CEL policy engine with compiled constraints
//   - Phase 4: Solver → Journal → REST API
//
// It verifies:
//  1. Candidates generated, scored, and filtered
//  2. DecisionRecord written to SQLite journal
//  3. Decision queryable via REST API
func TestIntegration_Phase2_3_4_EndToEnd(t *testing.T) {
	// ── Phase 3: CEL policy engine ──────────────────────────────────
	pe, err := cel.NewPolicyEngine()
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	policy := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cost-slo-policy",
			Namespace:  "production",
			UID:        types.UID("integration-uid-001"),
			Generation: 1,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Priority: 200,
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "slo", Weight: 0.5, Direction: policyv1alpha1.DirectionMaximize},
				{Name: "cost", Weight: 0.3, Direction: policyv1alpha1.DirectionMinimize},
				{Name: "carbon", Weight: 0.1, Direction: policyv1alpha1.DirectionMinimize},
				{Name: "fairness", Weight: 0.1, Direction: policyv1alpha1.DirectionMaximize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{
					Expr:   "candidate.spot_ratio <= 0.8",
					Reason: "spot ratio must not exceed 80% for production",
					Hard:   true,
				},
				{
					Expr:   "candidate.replicas >= 2",
					Reason: "minimum 2 replicas for HA",
					Hard:   true,
				},
			},
		},
	}

	if err := pe.Compile(policy); err != nil {
		t.Fatalf("Compile policy: %v", err)
	}

	// ── Phase 2: SLO state (mock Prometheus data) ───────────────────
	// Simulate: service is compliant but burning budget at 0.8x (headroom shrinking)
	input := &engine.SolverInput{
		Namespace: "production",
		Service:   "checkout-api",
		Trigger:   "periodic",
		Current: cel.CurrentState{
			Replicas:      4,
			CPURequest:    0.5,
			MemoryRequest: 1.0,
			CPUUsage:      1.8, // 4 * 0.5 = 2.0 capacity, 1.8 demand → 90% utilization
			MemoryUsage:   0.6,
			SpotRatio:     0.0,
			HourlyCost:    0.384,
		},
		SLO: cel.SLOStatus{
			Compliant:       true,
			BurnRate:        0.8,
			BudgetRemaining: 0.92,
			LatencyP99:      0.180,
			ErrorRate:       0.001,
			Availability:    0.9995,
			Throughput:      500,
		},
		Forecast: &cel.ForecastResult{
			PredictedRPS:  650,
			ChangePercent: 30,
			Confidence:    0.85,
		},
		Tenant: &cel.TenantStatus{
			Name:            "team-checkout",
			Tier:            "gold",
			CurrentCores:    2.0,
			GuaranteedCores: 4.0,
			MaxCores:        8.0,
		},
		Metrics: map[string]float64{
			"queue_depth":    12,
			"cache_hit_rate": 0.94,
		},
		Region:        "us-east-1",
		InstanceTypes: []string{"m5.large"},
		Policies: []engine.MatchedPolicy{
			{Policy: *policy, Key: cel.PolicyKey(policy)},
		},
	}

	// ── Phase 4: Solver ─────────────────────────────────────────────
	solver := engine.NewSolver(pe, engine.DefaultMaxCandidates)

	start := time.Now()
	action, record, err := solver.Solve(input)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// ── Verify performance: <100ms for full cycle ───────────────────
	if elapsed > 100*time.Millisecond {
		t.Errorf("solve took %v, want <100ms", elapsed)
	}

	// ── Verify candidates generated ─────────────────────────────────
	if len(record.Candidates) == 0 {
		t.Fatal("expected candidates to be generated")
	}
	t.Logf("Candidates generated: %d", len(record.Candidates))

	// ── Verify candidates scored on 4 dimensions ────────────────────
	for i, c := range record.Candidates {
		if c.Score.SLO < 0 || c.Score.SLO > 1 {
			t.Errorf("candidate[%d] SLO score %.3f out of [0,1]", i, c.Score.SLO)
		}
		if c.Score.Cost < 0 || c.Score.Cost > 1 {
			t.Errorf("candidate[%d] Cost score %.3f out of [0,1]", i, c.Score.Cost)
		}
		if c.Score.Carbon < 0 || c.Score.Carbon > 1 {
			t.Errorf("candidate[%d] Carbon score %.3f out of [0,1]", i, c.Score.Carbon)
		}
		if c.Score.Fairness < 0 || c.Score.Fairness > 1 {
			t.Errorf("candidate[%d] Fairness score %.3f out of [0,1]", i, c.Score.Fairness)
		}
	}

	// ── Verify CEL constraints filtered candidates ──────────────────
	spotViolations := 0
	replicaViolations := 0
	for _, c := range record.Candidates {
		if c.Plan.SpotRatio > 0.8 && c.Viable {
			t.Errorf("candidate with spot_ratio=%.1f should not be viable", c.Plan.SpotRatio)
		}
		if c.Plan.Replicas < 2 && c.Viable {
			t.Errorf("candidate with replicas=%d should not be viable", c.Plan.Replicas)
		}
		for _, cr := range c.Constraints {
			if !cr.Passed && cr.Expr == "candidate.spot_ratio <= 0.8" {
				spotViolations++
			}
			if !cr.Passed && cr.Expr == "candidate.replicas >= 2" {
				replicaViolations++
			}
		}
	}
	t.Logf("Spot violations: %d, Replica violations: %d", spotViolations, replicaViolations)

	// Some candidates should have been filtered (spot=1.0 candidates exist).
	nonViable := 0
	viable := 0
	for _, c := range record.Candidates {
		if c.Viable {
			viable++
		} else {
			nonViable++
		}
	}
	if nonViable == 0 {
		t.Error("expected some candidates filtered by constraints")
	}
	t.Logf("Viable: %d, Non-viable: %d", viable, nonViable)

	// ── Verify Pareto front selection ───────────────────────────────
	if len(record.ParetoFront) == 0 {
		t.Fatal("expected non-empty Pareto front")
	}
	t.Logf("Pareto front size: %d", len(record.ParetoFront))

	// All Pareto front candidates must be viable.
	for _, pf := range record.ParetoFront {
		if !pf.Viable {
			t.Error("Pareto front candidate must be viable")
		}
	}

	// ── Verify selected action ──────────────────────────────────────
	if action.Type == "" {
		t.Error("expected non-empty action type")
	}
	t.Logf("Selected action: %s, Replicas: %d, Reason: %s",
		action.Type, action.TargetReplica, action.Reason)

	// ── Verify DecisionRecord completeness ──────────────────────────
	if record.ID == "" {
		t.Error("ID missing")
	}
	if record.Namespace != "production" {
		t.Errorf("Namespace = %q", record.Namespace)
	}
	if record.Service != "checkout-api" {
		t.Errorf("Service = %q", record.Service)
	}
	if record.Trigger != "periodic" {
		t.Errorf("Trigger = %q", record.Trigger)
	}
	if len(record.PolicyNames) == 0 || record.PolicyNames[0] != "cost-slo-policy" {
		t.Errorf("PolicyNames = %v", record.PolicyNames)
	}
	if record.TenantStatus == nil {
		t.Error("TenantStatus should be set")
	}
	if record.ForecastState == nil {
		t.Error("ForecastState should be set")
	}
	if record.Metrics["queue_depth"] != 12 {
		t.Error("Metrics not propagated")
	}
	if record.ObjectiveWeights["slo"] != 0.5 {
		t.Errorf("ObjectiveWeights[slo] = %.2f, want 0.5", record.ObjectiveWeights["slo"])
	}

	// ── Phase 4: Write to journal ───────────────────────────────────
	journal, err := explainability.NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer journal.Close()

	if err := journal.Write(record); err != nil {
		t.Fatalf("journal.Write: %v", err)
	}

	// ── Verify journal query ────────────────────────────────────────
	fetched, err := journal.GetByID(record.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if fetched == nil {
		t.Fatal("expected decision record in journal")
	}
	if fetched.Service != "checkout-api" {
		t.Errorf("journal Service = %q", fetched.Service)
	}
	if len(fetched.Candidates) != len(record.Candidates) {
		t.Errorf("journal candidates: %d vs original: %d", len(fetched.Candidates), len(record.Candidates))
	}

	// Query by namespace+service.
	records, err := journal.Query(explainability.QueryFilter{
		Namespace: "production",
		Service:   "checkout-api",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record, got %d", len(records))
	}

	// ── Phase 4: REST API ───────────────────────────────────────────
	handler := explainability.NewAPIHandler(journal)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// GET /api/v1/decisions?namespace=production&service=checkout-api
	req := httptest.NewRequest("GET", "/api/v1/decisions?namespace=production&service=checkout-api", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list API status = %d, want 200", w.Code)
	}

	var listResult []engine.DecisionRecord
	if err := json.NewDecoder(w.Body).Decode(&listResult); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResult) != 1 {
		t.Errorf("list API returned %d records, want 1", len(listResult))
	}
	if listResult[0].Service != "checkout-api" {
		t.Errorf("list API record service = %q", listResult[0].Service)
	}

	// GET /api/v1/decisions/{id}
	req2 := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/decisions/%s", record.ID), nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("get API status = %d, want 200", w2.Code)
	}

	var getResult engine.DecisionRecord
	if err := json.NewDecoder(w2.Body).Decode(&getResult); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if getResult.ID != record.ID {
		t.Errorf("get API ID = %q, want %q", getResult.ID, record.ID)
	}
	if getResult.SelectedAction.Type != action.Type {
		t.Errorf("get API action = %q, want %q", getResult.SelectedAction.Type, action.Type)
	}
	if len(getResult.ParetoFront) == 0 {
		t.Error("get API ParetoFront empty after round-trip")
	}

	t.Logf("Integration test complete — solve: %v, candidates: %d, pareto: %d, action: %s",
		elapsed, len(record.Candidates), len(record.ParetoFront), action.Type)
}

// TestIntegration_MultiPolicy_MergedObjectives tests the solver with
// two overlapping policies whose objectives are merged.
func TestIntegration_MultiPolicy_MergedObjectives(t *testing.T) {
	pe, err := cel.NewPolicyEngine()
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	// Policy 1: SLO-focused, high priority.
	p1 := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "slo-first", UID: types.UID("uid-m1"), Generation: 1,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Priority: 300,
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "slo", Weight: 0.8, Direction: policyv1alpha1.DirectionMaximize},
				{Name: "cost", Weight: 0.2, Direction: policyv1alpha1.DirectionMinimize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{Expr: "candidate.replicas >= 3", Reason: "min 3 for SLO policy", Hard: true},
			},
		},
	}

	// Policy 2: Cost-focused, lower priority.
	p2 := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cost-first", UID: types.UID("uid-m2"), Generation: 1,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Priority: 100,
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "cost", Weight: 0.7, Direction: policyv1alpha1.DirectionMinimize},
				{Name: "carbon", Weight: 0.3, Direction: policyv1alpha1.DirectionMinimize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{Expr: "candidate.spot_ratio <= 0.5", Reason: "cap spot at 50%", Hard: true},
			},
		},
	}

	pe.Compile(p1)
	pe.Compile(p2)

	input := &engine.SolverInput{
		Namespace: "staging",
		Service:   "payment-svc",
		Trigger:   "periodic",
		Current: cel.CurrentState{
			Replicas:   5,
			CPURequest: 0.25,
			CPUUsage:   1.0,
			SpotRatio:  0.0,
			HourlyCost: 0.24,
		},
		SLO: cel.SLOStatus{
			Compliant:       true,
			BurnRate:        0.3,
			BudgetRemaining: 0.98,
		},
		Region:        "eu-west-1",
		InstanceTypes: []string{"c5.large"},
		Policies: []engine.MatchedPolicy{
			{Policy: *p1, Key: cel.PolicyKey(p1)},
			{Policy: *p2, Key: cel.PolicyKey(p2)},
		},
	}

	solver := engine.NewSolver(pe, engine.DefaultMaxCandidates)
	action, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// Verify both policies recorded.
	if len(record.PolicyNames) != 2 {
		t.Errorf("expected 2 policy names, got %d: %v", len(record.PolicyNames), record.PolicyNames)
	}

	// Verify constraints from both policies applied.
	// Candidates with spot_ratio > 0.5 should be non-viable (from p2).
	// Candidates with replicas < 3 should be non-viable (from p1).
	for _, c := range record.Candidates {
		if c.Viable && c.Plan.SpotRatio > 0.5 {
			t.Errorf("candidate with spot=%.1f should be filtered by cost-first policy", c.Plan.SpotRatio)
		}
		if c.Viable && c.Plan.Replicas < 3 {
			t.Errorf("candidate with replicas=%d should be filtered by slo-first policy", c.Plan.Replicas)
		}
	}

	// Merged objectives: slo(0.8), cost(0.2+0.7=0.9), carbon(0.3).
	// Verify all 3 appear in weights.
	for _, name := range []string{"slo", "cost", "carbon"} {
		if _, ok := record.ObjectiveWeights[name]; !ok {
			t.Errorf("missing objective weight: %s", name)
		}
	}

	t.Logf("Multi-policy: action=%s replicas=%d candidates=%d pareto=%d",
		action.Type, action.TargetReplica, len(record.Candidates), len(record.ParetoFront))
}

// TestIntegration_DryRun_NoSideEffects tests that dry-run policies produce
// a full decision record but mark the action as dry-run.
func TestIntegration_DryRun_NoSideEffects(t *testing.T) {
	pe, err := cel.NewPolicyEngine()
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	policy := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dry-run-integration", UID: types.UID("uid-dry"), Generation: 1,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			DryRun: true,
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "slo", Weight: 0.6, Direction: policyv1alpha1.DirectionMaximize},
				{Name: "cost", Weight: 0.4, Direction: policyv1alpha1.DirectionMinimize},
			},
		},
	}
	pe.Compile(policy)

	solver := engine.NewSolver(pe, engine.DefaultMaxCandidates)
	input := &engine.SolverInput{
		Namespace: "staging",
		Service:   "dry-run-svc",
		Trigger:   "manual",
		Current: cel.CurrentState{
			Replicas:   3,
			CPURequest: 0.5,
			CPUUsage:   1.2,
			SpotRatio:  0.0,
			HourlyCost: 0.288,
		},
		SLO: cel.SLOStatus{Compliant: true, BudgetRemaining: 0.90},
		Policies: []engine.MatchedPolicy{
			{Policy: *policy, Key: cel.PolicyKey(policy)},
		},
	}

	action, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if !action.DryRun {
		t.Error("expected action.DryRun = true")
	}
	if !record.DryRun {
		t.Error("expected record.DryRun = true")
	}

	// Full decision record should still be complete.
	if len(record.Candidates) == 0 {
		t.Error("dry-run should still generate candidates")
	}
	if len(record.ParetoFront) == 0 {
		t.Error("dry-run should still produce Pareto front")
	}

	// Write to journal to verify dry-run records are persisted.
	journal, err := explainability.NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer journal.Close()

	if err := journal.Write(record); err != nil {
		t.Fatalf("Write: %v", err)
	}

	fetched, err := journal.GetByID(record.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if fetched == nil {
		t.Fatal("dry-run record not found in journal")
	}
	if !fetched.DryRun {
		t.Error("fetched record should have DryRun=true")
	}

	t.Logf("Dry-run integration: candidates=%d action=%s dryRun=%v",
		len(record.Candidates), action.Type, action.DryRun)
}
