package cel

import (
	"fmt"
	"strconv"
	"sync"

	celgo "github.com/google/cel-go/cel"
	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
)

// PolicyEngine compiles and evaluates CEL constraints from OptimizationPolicy resources.
// It is safe for concurrent use.
type PolicyEngine struct {
	env      *celgo.Env
	programs sync.Map // map[string]compiledPolicy — keyed by PolicyKey
}

// CompiledPolicy holds the compiled CEL programs for an OptimizationPolicy generation.
type CompiledPolicy struct {
	Constraints []CompiledConstraint
	Objectives  []policyv1alpha1.PolicyObjective
	DryRun      bool
	Priority    int32
}

// CompiledConstraint holds a compiled CEL program and its metadata.
type CompiledConstraint struct {
	Program celgo.Program
	Expr    string // original expression, used in error messages
	Reason  string
	Hard    bool
}

// EvalResult is the outcome of evaluating all constraints of a policy.
type EvalResult struct {
	Passed     bool
	Violations []ConstraintViolation
	Penalties  float64 // sum of soft-constraint violation penalties
}

// ConstraintViolation records a single failing constraint.
type ConstraintViolation struct {
	Expr   string
	Reason string
	Hard   bool
}

// EvalContext is the data bag passed to every CEL expression.
type EvalContext struct {
	Candidate CandidatePlan
	Current   CurrentState
	SLO       SLOStatus
	Tenant    TenantStatus
	Forecast  ForecastResult
	Metrics   map[string]float64
	Cluster   ClusterState
}

// NewPolicyEngine creates a PolicyEngine backed by the OptiPilot CEL environment.
func NewPolicyEngine() (*PolicyEngine, error) {
	env, err := NewOptiPilotEnv()
	if err != nil {
		return nil, fmt.Errorf("creating CEL environment: %w", err)
	}
	return &PolicyEngine{env: env}, nil
}

// PolicyKey returns the cache key for a given policy generation.
func PolicyKey(policy *policyv1alpha1.OptimizationPolicy) string {
	return string(policy.UID) + "/" + strconv.FormatInt(policy.Generation, 10)
}

// Compile compiles all CEL constraints in the policy and caches the programs.
// Subsequent calls with the same PolicyKey are no-ops (cached result reused).
func (e *PolicyEngine) Compile(policy *policyv1alpha1.OptimizationPolicy) error {
	key := PolicyKey(policy)

	compiled := CompiledPolicy{
		Objectives: policy.Spec.Objectives,
		DryRun:     policy.Spec.DryRun,
		Priority:   policy.Spec.Priority,
	}

	for _, c := range policy.Spec.Constraints {
		ast, issues := e.env.Compile(c.Expr)
		if issues != nil && issues.Err() != nil {
			return fmt.Errorf("constraint %q: %w", c.Expr, issues.Err())
		}
		// Verify that the expression returns bool.
		if ast.OutputType().String() != "bool" {
			return fmt.Errorf("constraint %q must return bool, got %s", c.Expr, ast.OutputType())
		}
		prg, err := e.env.Program(ast)
		if err != nil {
			return fmt.Errorf("constraint %q: creating program: %w", c.Expr, err)
		}
		compiled.Constraints = append(compiled.Constraints, CompiledConstraint{
			Program: prg,
			Expr:    c.Expr,
			Reason:  c.Reason,
			Hard:    c.Hard,
		})
	}

	e.programs.Store(key, compiled)
	return nil
}

// GetCompiled returns the compiled policy for a key, if present.
func (e *PolicyEngine) GetCompiled(key string) (CompiledPolicy, bool) {
	val, ok := e.programs.Load(key)
	if !ok {
		return CompiledPolicy{}, false
	}
	return val.(CompiledPolicy), true
}

// Evaluate runs all compiled constraints for policyKey against ctx.
// Returns EvalResult{Passed: false} on the first hard violation; soft violations
// accumulate a penalty score.
func (e *PolicyEngine) Evaluate(policyKey string, ctx EvalContext) (EvalResult, error) {
	val, ok := e.programs.Load(policyKey)
	if !ok {
		return EvalResult{}, fmt.Errorf("policy %q not compiled; call Compile first", policyKey)
	}
	compiled := val.(CompiledPolicy)

	// Convert metrics map for type-safe map access in CEL.
	metricsMap := make(map[string]interface{}, len(ctx.Metrics))
	for k, v := range ctx.Metrics {
		metricsMap[k] = v
	}

	activation := map[string]interface{}{
		"candidate": ctx.Candidate,
		"current":   ctx.Current,
		"slo":       ctx.SLO,
		"tenant":    ctx.Tenant,
		"forecast":  ctx.Forecast,
		"metrics":   metricsMap,
		"cluster":   ctx.Cluster,
	}

	result := EvalResult{Passed: true}
	for _, constraint := range compiled.Constraints {
		out, _, err := constraint.Program.Eval(activation)
		if err != nil {
			// CEL evaluation error — treat as a violation for fail-safe behaviour.
			result.Violations = append(result.Violations, ConstraintViolation{
				Expr:   constraint.Expr,
				Reason: "evaluation error: " + err.Error(),
				Hard:   constraint.Hard,
			})
			if constraint.Hard {
				result.Passed = false
			}
			continue
		}

		passed, ok := out.Value().(bool)
		if !ok || !passed {
			result.Violations = append(result.Violations, ConstraintViolation{
				Expr:   constraint.Expr,
				Reason: constraint.Reason,
				Hard:   constraint.Hard,
			})
			if constraint.Hard {
				result.Passed = false
			} else {
				result.Penalties += 0.1 // soft constraint penalty unit
			}
		}
	}
	return result, nil
}
