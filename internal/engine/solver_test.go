package engine

import (
	"testing"
	"time"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func solverInput() *SolverInput {
	return &SolverInput{
		Namespace:     "production",
		Service:       "api-server",
		Trigger:       "periodic",
		InstanceTypes: []string{"m5.large"},
		Region:        "us-east-1",
		Current: cel.CurrentState{
			Replicas:      4,
			CPURequest:    0.5,
			MemoryRequest: 1.0,
			CPUUsage:      1.5,
			MemoryUsage:   0.5,
			SpotRatio:     0.0,
			HourlyCost:    0.384,
		},
		SLO: cel.SLOStatus{
			Compliant:       true,
			BurnRate:        0.5,
			BudgetRemaining: 0.95,
		},
	}
}

func TestSolver_BasicSolve_NoPolicy(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()

	action, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if record.ID == "" {
		t.Error("expected non-empty decision ID")
	}
	if record.Namespace != "production" {
		t.Errorf("namespace = %q, want %q", record.Namespace, "production")
	}
	if record.Service != "api-server" {
		t.Errorf("service = %q, want %q", record.Service, "api-server")
	}
	if len(record.Candidates) == 0 {
		t.Error("expected candidates in record")
	}

	// With 4 replicas, 1.5 CPUUsage demand, some candidates should provide better capacity.
	t.Logf("Action: %s, Replicas: %d, Reason: %s", action.Type, action.TargetReplica, action.Reason)
	t.Logf("Candidates: %d, ParetoFront: %d", len(record.Candidates), len(record.ParetoFront))
}

func TestSolver_WithPolicy_CELFiltering(t *testing.T) {
	// Create a real policy engine with a constraint.
	pe, err := cel.NewPolicyEngine()
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	policy := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cost-policy",
			Namespace:  "production",
			UID:        types.UID("test-uid-001"),
			Generation: 1,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "slo", Weight: 0.5, Direction: policyv1alpha1.DirectionMaximize},
				{Name: "cost", Weight: 0.3, Direction: policyv1alpha1.DirectionMinimize},
				{Name: "carbon", Weight: 0.1, Direction: policyv1alpha1.DirectionMinimize},
				{Name: "fairness", Weight: 0.1, Direction: policyv1alpha1.DirectionMaximize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{
					Expr:   "candidate.spot_ratio <= 0.8",
					Reason: "spot ratio must not exceed 80%",
					Hard:   true,
				},
			},
		},
	}

	if err := pe.Compile(policy); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	solver := NewSolver(pe, DefaultMaxCandidates)
	input := solverInput()
	key := cel.PolicyKey(policy)
	input.Policies = []MatchedPolicy{
		{Policy: *policy, Key: key},
	}

	action, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// Verify spot_ratio=1.0 candidates are filtered out.
	for _, c := range record.Candidates {
		if c.Plan.SpotRatio > 0.8 && c.Viable {
			t.Errorf("candidate with spot_ratio=%.1f should not be viable", c.Plan.SpotRatio)
		}
	}

	// Check policy names recorded.
	found := false
	for _, pn := range record.PolicyNames {
		if pn == "cost-policy" {
			found = true
		}
	}
	if !found {
		t.Error("expected cost-policy in record.PolicyNames")
	}

	t.Logf("Action: %s, Replicas: %d, DryRun: %v", action.Type, action.TargetReplica, action.DryRun)
}

func TestSolver_DryRunPropagation(t *testing.T) {
	pe, err := cel.NewPolicyEngine()
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	policy := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "dry-run-policy",
			UID:        types.UID("test-uid-002"),
			Generation: 1,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			DryRun: true,
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "slo", Weight: 1.0, Direction: policyv1alpha1.DirectionMaximize},
			},
		},
	}
	pe.Compile(policy)

	solver := NewSolver(pe, DefaultMaxCandidates)
	input := solverInput()
	input.Policies = []MatchedPolicy{
		{Policy: *policy, Key: cel.PolicyKey(policy)},
	}

	action, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !action.DryRun {
		t.Error("expected DryRun=true from dry-run policy")
	}
	if !record.DryRun {
		t.Error("expected record.DryRun=true")
	}
}

func TestSolver_NoActionWhenCurrentOptimal(t *testing.T) {
	// If the best candidate IS the current state, action should be no_action.
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()
	// Make current state very generous so no change is needed.
	input.Current.Replicas = 8
	input.Current.CPURequest = 1.0
	input.Current.CPUUsage = 1.0 // 8 * 1.0 = 8.0 capacity vs 1.0 demand → ratio 8.0

	action, _, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// The 1.0 multiplier candidate matches current state.
	// It should score highest (max SLO, good cost).
	t.Logf("Action: %s, Replicas: %d", action.Type, action.TargetReplica)
}

func TestSolver_PerformanceBenchmark(t *testing.T) {
	solver := NewSolver(nil, 100)
	input := solverInput()
	input.Current.Replicas = 20
	rightCPU := 0.3
	rightMem := 0.8
	input.RightSizedCPU = &rightCPU
	input.RightSizedMemory = &rightMem

	start := time.Now()
	action, record, err := solver.Solve(input)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if elapsed > 100*time.Millisecond {
		t.Errorf("solve took %v, want <100ms", elapsed)
	}

	t.Logf("Solve completed in %v: %d candidates, action=%s replicas=%d",
		elapsed, len(record.Candidates), action.Type, action.TargetReplica)
}

func TestSolver_AllConstraintsFail_Fallback(t *testing.T) {
	pe, err := cel.NewPolicyEngine()
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	// Impossible constraint: no candidate can have 0 replicas.
	policy := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "impossible-policy",
			UID:        types.UID("test-uid-003"),
			Generation: 1,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "slo", Weight: 1.0, Direction: policyv1alpha1.DirectionMaximize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{
					Expr:   "candidate.replicas == 0",
					Reason: "impossible: requires 0 replicas",
					Hard:   true,
				},
			},
		},
	}
	pe.Compile(policy)

	solver := NewSolver(pe, DefaultMaxCandidates)
	input := solverInput()
	input.Policies = []MatchedPolicy{
		{Policy: *policy, Key: cel.PolicyKey(policy)},
	}

	action, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if action.Type != ActionNoAction {
		t.Errorf("expected no_action when all candidates filtered, got %s", action.Type)
	}
	if action.Reason != "all candidates filtered by constraints" {
		t.Errorf("unexpected reason: %s", action.Reason)
	}

	// Verify all candidates marked non-viable.
	viableCount := 0
	for _, c := range record.Candidates {
		if c.Viable {
			viableCount++
		}
	}
	if viableCount != 0 {
		t.Errorf("expected 0 viable candidates, got %d", viableCount)
	}
}

func TestSolver_DecisionRecordCompleteness(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()
	input.Metrics = map[string]float64{"custom_metric": 42.0}
	input.Tenant = &cel.TenantStatus{
		Name:            "team-alpha",
		Tier:            "gold",
		CurrentCores:    2.0,
		GuaranteedCores: 5.0,
		MaxCores:        10.0,
	}
	input.Forecast = &cel.ForecastResult{
		PredictedRPS:  200,
		ChangePercent: 25,
		Confidence:    0.85,
	}

	_, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// Check all fields populated.
	if record.Namespace != "production" {
		t.Errorf("Namespace missing")
	}
	if record.Service != "api-server" {
		t.Errorf("Service missing")
	}
	if record.Trigger != "periodic" {
		t.Errorf("Trigger missing")
	}
	if record.TenantStatus == nil {
		t.Error("TenantStatus should not be nil")
	}
	if record.ForecastState == nil {
		t.Error("ForecastState should not be nil")
	}
	if record.Metrics["custom_metric"] != 42.0 {
		t.Error("Metrics not preserved")
	}
	if len(record.Candidates) == 0 {
		t.Error("Candidates missing")
	}
	if len(record.ParetoFront) == 0 {
		t.Error("ParetoFront missing")
	}
	if record.SelectedAction.Type == "" {
		t.Error("SelectedAction.Type missing")
	}
}

// ── Forecast integration tests ────────────────────────────────────────────────

func TestSolver_ForecastPreWarming(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()
	input.Forecast = &cel.ForecastResult{
		PredictedRPS:  300,
		ChangePercent: 25.0, // >20% triggers pre-warming
		Confidence:    0.9,
	}

	action, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// Pre-warming candidates (4×1.3=5, 4×1.5=6) should be in the candidate set.
	foundPreWarm := false
	for _, c := range record.Candidates {
		if c.Plan.Replicas == 6 && c.Plan.CPURequest == input.Current.CPURequest &&
			c.Plan.SpotRatio == input.Current.SpotRatio {
			foundPreWarm = true
			break
		}
	}
	if !foundPreWarm {
		t.Error("expected pre-warming candidate with 6 replicas (4×1.5)")
	}

	// ForecastState should be recorded in the decision record.
	if record.ForecastState == nil {
		t.Error("ForecastState should be recorded")
	}

	t.Logf("Action: %s, Replicas: %d, Candidates: %d", action.Type, action.TargetReplica, len(record.Candidates))
}

func TestSolver_ForecastNilFallback(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()
	input.Forecast = nil // no forecast → reactive only

	_, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// Should still produce candidates from the standard cartesian product.
	if len(record.Candidates) == 0 {
		t.Error("expected candidates even without forecast")
	}
	if record.ForecastState != nil {
		t.Error("ForecastState should be nil when no forecast provided")
	}
}

func TestSolver_ForecastLowChange_NoPreWarming(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)

	// Run without forecast to get baseline candidate count.
	baseInput := solverInput()
	_, baseRecord, err := solver.Solve(baseInput)
	if err != nil {
		t.Fatalf("Solve (baseline): %v", err)
	}
	baseCandidateCount := len(baseRecord.Candidates)

	// Run with low-change forecast — should not inject extra candidates.
	input := solverInput()
	input.Forecast = &cel.ForecastResult{
		PredictedRPS:  110,
		ChangePercent: 10.0, // <20% → no pre-warming
		Confidence:    0.9,
	}

	_, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if len(record.Candidates) != baseCandidateCount {
		t.Errorf("candidates=%d, want same as baseline=%d (no pre-warming for <20%% change)",
			len(record.Candidates), baseCandidateCount)
	}
}

func TestSolver_SpotRiskReduction(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()
	input.Current.SpotRatio = 0.8 // high spot ratio
	input.Forecast = &cel.ForecastResult{
		ChangePercent: 5.0, // low change → no pre-warming
		SpotRiskScore: 0.7, // >0.6 → spot reduction
		Confidence:    0.85,
	}

	_, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// Should have spot-reduction candidates (ratio 0.0 and 0.3) at current replica count.
	foundReduced := false
	for _, c := range record.Candidates {
		if c.Plan.SpotRatio == 0.0 && c.Plan.Replicas == input.Current.Replicas &&
			c.Plan.CPURequest == input.Current.CPURequest {
			foundReduced = true
			break
		}
	}
	if !foundReduced {
		t.Error("expected spot-reduction candidate with 0.0 spot ratio at current replicas")
	}
}

func TestSolver_ForecastBothTriggers(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()
	input.Current.SpotRatio = 0.5
	input.Forecast = &cel.ForecastResult{
		PredictedRPS:  400,
		ChangePercent: 30.0, // >20% → pre-warming
		SpotRiskScore: 0.8,  // >0.6 → spot reduction
		Confidence:    0.9,
	}

	_, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	hasPreWarm := false
	hasSpotReduced := false
	for _, c := range record.Candidates {
		// Pre-warming: higher replicas with current config
		if c.Plan.Replicas > input.Current.Replicas &&
			c.Plan.CPURequest == input.Current.CPURequest &&
			c.Plan.SpotRatio == input.Current.SpotRatio {
			hasPreWarm = true
		}
		// Spot reduction: current replicas with lower spot ratio
		if c.Plan.SpotRatio < input.Current.SpotRatio &&
			c.Plan.Replicas == input.Current.Replicas &&
			c.Plan.CPURequest == input.Current.CPURequest {
			hasSpotReduced = true
		}
	}
	if !hasPreWarm {
		t.Error("expected pre-warming candidates when ChangePercent>20")
	}
	if !hasSpotReduced {
		t.Error("expected spot-reduction candidates when SpotRiskScore>0.6")
	}
}

func TestSolver_SpotRiskBelowThreshold_NoReduction(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)

	// Baseline: no forecast
	baseInput := solverInput()
	baseInput.Current.SpotRatio = 0.5
	_, baseRecord, err := solver.Solve(baseInput)
	if err != nil {
		t.Fatalf("Solve (baseline): %v", err)
	}

	// With spot risk below threshold — should not add extra candidates.
	input := solverInput()
	input.Current.SpotRatio = 0.5
	input.Forecast = &cel.ForecastResult{
		ChangePercent: 5.0,
		SpotRiskScore: 0.3, // <0.6 → no spot reduction
		Confidence:    0.8,
	}

	_, record, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if len(record.Candidates) != len(baseRecord.Candidates) {
		t.Errorf("candidates=%d, want same as baseline=%d (no spot reduction for <0.6 risk)",
			len(record.Candidates), len(baseRecord.Candidates))
	}
}

func TestSolver_TuneAction_IncludesMemory(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()

	// Set right-sized recommendations that differ from current.
	rightCPU := 0.3
	rightMem := 0.7
	input.RightSizedCPU = &rightCPU
	input.RightSizedMemory = &rightMem

	// Make replicas match to avoid scale_up/scale_down — force a tune action.
	// The solver will generate candidates with right-sized resources.
	// If the winning candidate has different memory, the tune reason should include it.
	action, _, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	t.Logf("Action: %s, CPU: %.3f, Memory: %.3f, Reason: %s",
		action.Type, action.CPURequest, action.MemoryRequest, action.Reason)

	// The action should carry memory request from the winning candidate.
	if action.MemoryRequest <= 0 {
		t.Error("expected positive MemoryRequest in action")
	}
}

func TestSolver_PrefersRightSizedSameReplicaCandidate(t *testing.T) {
	pe, err := cel.NewPolicyEngine()
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	policy := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "right-size-policy",
			UID:        types.UID("test-uid-right-size"),
			Generation: 1,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "slo", Weight: 0.5, Direction: policyv1alpha1.DirectionMaximize},
				{Name: "cost", Weight: 0.5, Direction: policyv1alpha1.DirectionMinimize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{
					Expr:   "candidate.spot_ratio == 0.0",
					Reason: "keep spot ratio fixed for right-size regression",
					Hard:   true,
				},
			},
		},
	}

	if err := pe.Compile(policy); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	solver := NewSolver(pe, DefaultMaxCandidates)
	input := solverInput()
	input.Current.Replicas = 1
	input.Current.CPURequest = 0.5
	input.Current.MemoryRequest = 1.0
	input.Current.CPUUsage = 0.2
	input.Current.MemoryUsage = 0.3
	rightCPU := 0.3
	rightMem := 0.5
	input.RightSizedCPU = &rightCPU
	input.RightSizedMemory = &rightMem
	input.Policies = []MatchedPolicy{{Policy: *policy, Key: cel.PolicyKey(policy)}}

	action, _, err := solver.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if action.Type != ActionTune {
		t.Fatalf("expected tune action, got %s (%s)", action.Type, action.Reason)
	}
	if action.TargetReplica != 1 {
		t.Fatalf("expected tune to keep replicas at 1, got %d", action.TargetReplica)
	}
	if action.CPURequest != rightCPU || action.MemoryRequest != rightMem {
		t.Fatalf("expected tune to pick right-sized requests cpu=%.3f mem=%.3f, got cpu=%.3f mem=%.3f", rightCPU, rightMem, action.CPURequest, action.MemoryRequest)
	}
}

func TestSolver_BuildAction_SpotOnlyChangeIsNoAction(t *testing.T) {
	solver := NewSolver(nil, DefaultMaxCandidates)
	input := solverInput()

	best := ScoredCandidate{
		Plan: cel.CandidatePlan{
			Replicas:      input.Current.Replicas,
			CPURequest:    input.Current.CPURequest,
			MemoryRequest: input.Current.MemoryRequest,
			SpotRatio:     0.5,
		},
		Score: CandidateScore{Weighted: 0.9},
	}

	action := solver.buildAction(input, best, false)
	if action.Type != ActionNoAction {
		t.Fatalf("expected no_action for spot-only change, got %s", action.Type)
	}
	if action.Reason != "current state is optimal for supported actuators" {
		t.Fatalf("unexpected reason: %s", action.Reason)
	}
}
