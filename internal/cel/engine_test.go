package cel_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
)

// newTestEngine creates a PolicyEngine; fatals the test on error.
func newTestEngine(t *testing.T) *cel.PolicyEngine {
	t.Helper()
	e, err := cel.NewPolicyEngine()
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}
	return e
}

// makePolicy wraps a single CEL expression into an OptimizationPolicy for testing.
func makePolicy(expr string, hard bool) *policyv1alpha1.OptimizationPolicy {
	return &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			UID:        types.UID("test-uid-" + expr[:min(len(expr), 30)]),
			Generation: 1,
			Name:       "test-policy",
			Namespace:  "default",
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "cost", Weight: 1.0, Direction: policyv1alpha1.DirectionMinimize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{Expr: expr, Reason: "test constraint", Hard: hard},
			},
		},
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- TestCELEngine_BasicConstraints ---

func TestCELEngine_BasicConstraints(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		candidate  cel.CandidatePlan
		slo        cel.SLOStatus
		wantPassed bool
	}{
		{
			name:       "spot ratio within limit",
			expr:       "candidate.spot_ratio <= 0.8",
			candidate:  cel.CandidatePlan{SpotRatio: 0.5},
			wantPassed: true,
		},
		{
			name:       "spot ratio exceeds limit",
			expr:       "candidate.spot_ratio <= 0.8",
			candidate:  cel.CandidatePlan{SpotRatio: 0.9},
			wantPassed: false,
		},
		{
			name:       "minimum replicas fail",
			expr:       "candidate.replicas >= 2",
			candidate:  cel.CandidatePlan{Replicas: 1},
			wantPassed: false,
		},
		{
			name:       "minimum replicas pass",
			expr:       "candidate.replicas >= 2",
			candidate:  cel.CandidatePlan{Replicas: 3},
			wantPassed: true,
		},
		{
			name:      "complex expression — slo.compliant saves low replicas",
			expr:      "candidate.replicas >= 3 || slo.compliant",
			candidate: cel.CandidatePlan{Replicas: 1},
			slo:       cel.SLOStatus{Compliant: true},
			// slo.compliant = true → passes even though replicas < 3
			wantPassed: true,
		},
		{
			name:       "cost constraint pass",
			expr:       "candidate.estimated_cost < 100.0",
			candidate:  cel.CandidatePlan{EstimatedCost: 50.0},
			wantPassed: true,
		},
		{
			name:       "cost constraint fail",
			expr:       "candidate.estimated_cost < 100.0",
			candidate:  cel.CandidatePlan{EstimatedCost: 150.0},
			wantPassed: false,
		},
		{
			name:       "custom function spotRisk passes",
			expr:       `spotRisk("m5.large", "us-east-1a") < 0.2`,
			candidate:  cel.CandidatePlan{},
			wantPassed: true, // SpotRiskFunc("m5.large", ...) = 0.15 < 0.2
		},
		{
			name:       "custom function spotRisk fails",
			expr:       `spotRisk("m5.large", "us-east-1a") < 0.1`,
			candidate:  cel.CandidatePlan{},
			wantPassed: false, // 0.15 is not < 0.1
		},
		{
			name:       "carbonIntensity function",
			expr:       `carbonIntensity("eu-north-1") < 100.0`,
			candidate:  cel.CandidatePlan{},
			wantPassed: true, // eu-north-1 = 50 gCO2/kWh
		},
		{
			name:       "costRate function spot",
			expr:       `costRate("m5.large", "us-east-1", true) < 0.05`,
			candidate:  cel.CandidatePlan{},
			wantPassed: true, // 0.096 * 0.3 = 0.0288 < 0.05
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := newTestEngine(t)
			policy := makePolicy(tc.expr, true)
			if err := engine.Compile(policy); err != nil {
				t.Fatalf("Compile: %v", err)
			}
			ctx := cel.EvalContext{
				Candidate: tc.candidate,
				SLO:       tc.slo,
				Metrics:   map[string]float64{},
			}
			result, err := engine.Evaluate(cel.PolicyKey(policy), ctx)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if result.Passed != tc.wantPassed {
				t.Errorf("got Passed=%v, want %v (violations: %v)", result.Passed, tc.wantPassed, result.Violations)
			}
		})
	}
}

// --- TestCELEngine_SoftConstraints ---

func TestCELEngine_SoftConstraints(t *testing.T) {
	engine := newTestEngine(t)
	policy := makePolicy("candidate.spot_ratio <= 0.5", false) // soft
	if err := engine.Compile(policy); err != nil {
		t.Fatal(err)
	}
	ctx := cel.EvalContext{
		Candidate: cel.CandidatePlan{SpotRatio: 0.8}, // violates but soft
		Metrics:   map[string]float64{},
	}
	result, err := engine.Evaluate(cel.PolicyKey(policy), ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Soft violations don't set Passed=false
	if !result.Passed {
		t.Error("expected Passed=true for soft constraint violation")
	}
	if result.Penalties != 0.1 {
		t.Errorf("expected Penalties=0.1, got %v", result.Penalties)
	}
	if len(result.Violations) != 1 {
		t.Errorf("expected 1 violation, got %d", len(result.Violations))
	}
}

// --- TestCELEngine_CompilationErrors ---

func TestCELEngine_CompilationErrors(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr string
	}{
		{"invalid syntax", "candidate.replicas >=", "syntax error"},
		{"wrong return type — returns int", "candidate.replicas", "must return bool"},
		{"undefined variable", "unknown_var > 5", "undeclared reference"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := newTestEngine(t)
			policy := makePolicy(tc.expr, true)
			err := engine.Compile(policy)
			if err == nil {
				t.Fatal("expected compilation error, got nil")
			}
			t.Logf("Got expected error: %v", err)
		})
	}
}

// --- TestCELEngine_MultiplePolicies ---

func TestCELEngine_MultiplePolicies(t *testing.T) {
	engine := newTestEngine(t)

	p1 := makePolicy("candidate.replicas >= 2", true)
	p1.UID = "uid-p1"
	p2 := makePolicy("candidate.spot_ratio <= 0.8", true)
	p2.UID = "uid-p2"

	if err := engine.Compile(p1); err != nil {
		t.Fatal(err)
	}
	if err := engine.Compile(p2); err != nil {
		t.Fatal(err)
	}

	ctx := cel.EvalContext{
		Candidate: cel.CandidatePlan{Replicas: 3, SpotRatio: 0.5},
		Metrics:   map[string]float64{},
	}
	r1, err := engine.Evaluate(cel.PolicyKey(p1), ctx)
	if err != nil || !r1.Passed {
		t.Errorf("p1 failed unexpectedly: %v %v", r1, err)
	}
	r2, err := engine.Evaluate(cel.PolicyKey(p2), ctx)
	if err != nil || !r2.Passed {
		t.Errorf("p2 failed unexpectedly: %v %v", r2, err)
	}
}

// --- TestCELEngine_PolicyNotCompiled ---

func TestCELEngine_PolicyNotCompiled(t *testing.T) {
	engine := newTestEngine(t)
	_, err := engine.Evaluate("not-compiled-key", cel.EvalContext{})
	if err == nil {
		t.Error("expected error for uncompiled policy")
	}
}

// --- TestCELEngine_ObjectiveWeights ---

func TestCELEngine_ObjectiveWeights(t *testing.T) {
	engine := newTestEngine(t)
	policy := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{UID: "uid-weights", Generation: 1},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "cost", Weight: 0.4, Direction: policyv1alpha1.DirectionMinimize},
				{Name: "slo", Weight: 0.6, Direction: policyv1alpha1.DirectionMaximize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{Expr: "candidate.replicas >= 1", Reason: "min replicas", Hard: true},
			},
		},
	}
	if err := engine.Compile(policy); err != nil {
		t.Fatal(err)
	}
	compiled, ok := engine.GetCompiled(cel.PolicyKey(policy))
	if !ok {
		t.Fatal("compiled policy not found after Compile")
	}
	if len(compiled.Objectives) != 2 {
		t.Errorf("expected 2 objectives, got %d", len(compiled.Objectives))
	}
}

// --- TestCELEngine_Performance ---

func TestCELEngine_Performance(t *testing.T) {
	engine := newTestEngine(t)

	// Build a policy with 50 constraints.
	constraints := make([]policyv1alpha1.PolicyConstraint, 50)
	for i := range constraints {
		constraints[i] = policyv1alpha1.PolicyConstraint{
			Expr:   "candidate.replicas >= 1",
			Reason: "min replicas",
			Hard:   true,
		}
	}
	policy := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{UID: "uid-perf", Generation: 1},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Objectives:  []policyv1alpha1.PolicyObjective{{Name: "cost", Weight: 1.0, Direction: policyv1alpha1.DirectionMinimize}},
			Constraints: constraints,
		},
	}
	if err := engine.Compile(policy); err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	ctx := cel.EvalContext{
		Candidate: cel.CandidatePlan{Replicas: 5, SpotRatio: 0.3},
		Metrics:   map[string]float64{},
	}

	start := time.Now()
	result, err := engine.Evaluate(cel.PolicyKey(policy), ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected all 50 constraints to pass, got violations: %v", result.Violations)
	}
	if elapsed > 5*time.Millisecond {
		t.Errorf("evaluation took %v, want <5ms", elapsed)
	}
	t.Logf("50 constraints evaluated in %v", elapsed)
}
