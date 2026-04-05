package engine

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
)

// Solver orchestrates candidate generation, scoring, CEL filtering,
// Pareto selection, and decision recording.
type Solver struct {
	PolicyEngine  *cel.PolicyEngine
	MaxCandidates int
}

// NewSolver creates a Solver with the given policy engine.
func NewSolver(policyEngine *cel.PolicyEngine, maxCandidates int) *Solver {
	if maxCandidates <= 0 {
		maxCandidates = DefaultMaxCandidates
	}
	return &Solver{
		PolicyEngine:  policyEngine,
		MaxCandidates: maxCandidates,
	}
}

// Solve runs one optimization cycle for a single ServiceObjective.
// Returns the chosen action, a full decision record, and any error.
func (s *Solver) Solve(input *SolverInput) (ScalingAction, DecisionRecord, error) {
	now := time.Now().UTC()
	decisionID := uuid.New().String()

	// 1. Generate candidates.
	candidates := GenerateCandidates(input, s.MaxCandidates)

	// 1b. Forecast-driven candidate injection.
	// Pre-warming: if demand predicted to increase >20%, add higher-replica candidates.
	// Spot reduction: if spot risk >0.6, add lower-spot-ratio candidates.
	// These are injected after pruning to guarantee they reach the scoring phase.
	if input.Forecast != nil {
		if input.Forecast.ChangePercent > PreWarmingChangeThreshold {
			candidates = append(candidates, PreWarmingCandidates(input)...)
		}
		if input.Forecast.SpotRiskScore > SpotRiskThreshold {
			candidates = append(candidates, SpotReductionCandidates(input)...)
		}
	}

	if len(candidates) == 0 {
		return s.noAction(input, decisionID, now, "no candidates generated")
	}

	// 2. Collect objectives from all matching policies.
	objSources := s.collectObjectives(input)
	policyObjectives := make([]policyv1alpha1.PolicyObjective, len(objSources))
	for i, o := range objSources {
		policyObjectives[i] = o.Objective
	}

	// 3. Score all candidates.
	scorer := NewScorer(input, policyObjectives)
	scored := scorer.ScoreAll(candidates)

	// 4. Apply CEL constraints from all policies.
	dryRun := false
	var policyNames []string
	for _, mp := range input.Policies {
		policyNames = append(policyNames, mp.Policy.Name)
		if mp.Policy.Spec.DryRun {
			dryRun = true
		}
		s.applyConstraints(&scored, mp)
	}

	// 5. Filter to viable candidates only.
	viable := filterViable(scored)
	if len(viable) == 0 {
		rec := s.buildRecord(decisionID, now, input, scored, nil, policyNames, objSources, dryRun)
		action := ScalingAction{
			Type:          ActionNoAction,
			TargetReplica: int32(input.Current.Replicas),
			CPURequest:    input.Current.CPURequest,
			MemoryRequest: input.Current.MemoryRequest,
			SpotRatio:     input.Current.SpotRatio,
			DryRun:        dryRun,
			Reason:        "all candidates filtered by constraints",
			Confidence:    0.0,
		}
		rec.SelectedAction = action
		rec.ActionType = ActionNoAction
		return action, rec, nil
	}

	// 6. Pareto front selection.
	front := FindParetoFront(viable)
	best := SelectBest(front, input.Current)

	// 7. Build action.
	action := s.buildAction(input, best, dryRun)

	// 8. Build decision record.
	rec := s.buildRecord(decisionID, now, input, scored, front, policyNames, objSources, dryRun)
	rec.SelectedAction = action
	rec.ActionType = action.Type
	rec.Confidence = action.Confidence

	return action, rec, nil
}

// applyConstraints evaluates CEL constraints from a matched policy against all scored candidates.
func (s *Solver) applyConstraints(scored *[]ScoredCandidate, mp MatchedPolicy) {
	if s.PolicyEngine == nil {
		return
	}

	compiled, ok := s.PolicyEngine.GetCompiled(mp.Key)
	if !ok {
		return
	}

	for i := range *scored {
		sc := &(*scored)[i]
		evalCtx := cel.EvalContext{
			Candidate: sc.Plan,
		}

		result, err := s.PolicyEngine.Evaluate(mp.Key, evalCtx)
		if err != nil {
			sc.Constraints = append(sc.Constraints, ConstraintResult{
				Expr:       "engine error",
				Reason:     err.Error(),
				Hard:       true,
				Passed:     false,
				PolicyName: mp.Policy.Name,
			})
			sc.Viable = false
			continue
		}

		for _, v := range result.Violations {
			sc.Constraints = append(sc.Constraints, ConstraintResult{
				Expr:       v.Expr,
				Reason:     v.Reason,
				Hard:       v.Hard,
				Passed:     false,
				PolicyName: mp.Policy.Name,
			})
		}

		// Record passing constraints too for full explainability.
		for _, cc := range compiled.Constraints {
			violated := false
			for _, v := range result.Violations {
				if v.Expr == cc.Expr {
					violated = true
					break
				}
			}
			if !violated {
				sc.Constraints = append(sc.Constraints, ConstraintResult{
					Expr:       cc.Expr,
					Reason:     cc.Reason,
					Hard:       cc.Hard,
					Passed:     true,
					PolicyName: mp.Policy.Name,
				})
			}
		}

		if !result.Passed {
			sc.Viable = false
		}

		// Apply soft violation penalties to weighted score.
		if result.Penalties > 0 {
			sc.Score.Weighted -= result.Penalties
			if sc.Score.Weighted < 0 {
				sc.Score.Weighted = 0
			}
		}
	}
}

// collectObjectives merges objectives from all policies (highest priority first).
func (s *Solver) collectObjectives(input *SolverInput) []ObjectiveWithSource {
	var all []ObjectiveWithSource
	for _, mp := range input.Policies {
		for _, obj := range mp.Policy.Spec.Objectives {
			all = append(all, ObjectiveWithSource{
				Objective:  obj,
				PolicyName: mp.Policy.Name,
			})
		}
	}
	return all
}

// buildAction determines the action type based on comparing the best candidate to current state.
func (s *Solver) buildAction(input *SolverInput, best ScoredCandidate, dryRun bool) ScalingAction {
	action := ScalingAction{
		TargetReplica: int32(best.Plan.Replicas),
		CPURequest:    best.Plan.CPURequest,
		MemoryRequest: best.Plan.MemoryRequest,
		SpotRatio:     best.Plan.SpotRatio,
		DryRun:        dryRun,
		Confidence:    best.Score.Weighted,
	}

	currentReplicas := input.Current.Replicas
	if best.Plan.Replicas > currentReplicas {
		action.Type = ActionScaleUp
		action.Reason = fmt.Sprintf("scale up: %d → %d replicas (weighted score %.3f)",
			currentReplicas, best.Plan.Replicas, best.Score.Weighted)
	} else if best.Plan.Replicas < currentReplicas {
		action.Type = ActionScaleDown
		action.Reason = fmt.Sprintf("scale down: %d → %d replicas (weighted score %.3f)",
			currentReplicas, best.Plan.Replicas, best.Score.Weighted)
	} else if best.Plan.CPURequest != input.Current.CPURequest || best.Plan.SpotRatio != input.Current.SpotRatio {
		action.Type = ActionTune
		action.Reason = fmt.Sprintf("tune: cpu %.3f→%.3f, spot %.0f%%→%.0f%% (weighted score %.3f)",
			input.Current.CPURequest, best.Plan.CPURequest,
			input.Current.SpotRatio*100, best.Plan.SpotRatio*100,
			best.Score.Weighted)
	} else {
		action.Type = ActionNoAction
		action.Reason = "current state is optimal"
	}

	return action
}

// buildRecord assembles the full decision record for explainability.
func (s *Solver) buildRecord(
	id string, ts time.Time, input *SolverInput,
	scored []ScoredCandidate, front []ScoredCandidate,
	policyNames []string, objectives []ObjectiveWithSource,
	dryRun bool,
) DecisionRecord {
	weights := make(map[string]float64)
	for _, o := range objectives {
		weights[o.Objective.Name] = o.Objective.Weight
	}

	rec := DecisionRecord{
		ID:               id,
		Timestamp:        ts,
		Namespace:        input.Namespace,
		Service:          input.Service,
		Trigger:          input.Trigger,
		CurrentState:     input.Current,
		SLOStatus:        input.SLO,
		ClusterState:     input.Cluster,
		Metrics:          input.Metrics,
		Candidates:       scored,
		ParetoFront:      front,
		PolicyNames:      policyNames,
		ObjectiveWeights: weights,
		DryRun:           dryRun,
	}

	if input.Tenant != nil {
		rec.TenantStatus = input.Tenant
	}
	if input.Forecast != nil {
		rec.ForecastState = input.Forecast
	}

	return rec
}

// noAction is a convenience for returning a no-action decision.
func (s *Solver) noAction(input *SolverInput, id string, ts time.Time, reason string) (ScalingAction, DecisionRecord, error) {
	action := ScalingAction{
		Type:          ActionNoAction,
		TargetReplica: int32(input.Current.Replicas),
		CPURequest:    input.Current.CPURequest,
		MemoryRequest: input.Current.MemoryRequest,
		SpotRatio:     input.Current.SpotRatio,
		Reason:        reason,
	}
	rec := DecisionRecord{
		ID:             id,
		Timestamp:      ts,
		Namespace:      input.Namespace,
		Service:        input.Service,
		Trigger:        input.Trigger,
		CurrentState:   input.Current,
		SLOStatus:      input.SLO,
		SelectedAction: action,
		ActionType:     ActionNoAction,
	}
	return action, rec, nil
}

// filterViable returns only candidates where all hard constraints passed.
func filterViable(scored []ScoredCandidate) []ScoredCandidate {
	var viable []ScoredCandidate
	for _, sc := range scored {
		if sc.Viable {
			viable = append(viable, sc)
		}
	}
	return viable
}

// ObjectiveWithSource pairs an objective with its originating policy name.
type ObjectiveWithSource struct {
	Objective  policyv1alpha1.PolicyObjective
	PolicyName string
}
