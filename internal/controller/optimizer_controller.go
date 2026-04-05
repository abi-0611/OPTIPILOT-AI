package controller

import (
	"context"
	"time"

	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/actuator"
	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/explainability"
)

// OptimizerController runs a periodic optimization loop.
// It is registered as a manager.Runnable, NOT a Kubebuilder reconciler.
type OptimizerController struct {
	client.Client
	Interval    time.Duration
	Solver      *engine.Solver
	PolicyRecon *OptimizationPolicyReconciler
	Journal     *explainability.Journal
	Recorder    record.EventRecorder
	ActuatorReg *actuator.Registry         // optional; nil disables actuation
	SafetyGuard *actuator.SafetyGuard      // optional; nil skips safety checks
	CanaryCtrl  *actuator.CanaryController // optional; nil disables canary
}

// Start implements manager.Runnable. It runs the optimization loop until ctx is cancelled.
func (o *OptimizerController) Start(ctx context.Context) error {
	logger := ctrl.Log.WithName("optimizer")
	logger.Info("starting optimization loop", "interval", o.Interval.String())

	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("optimization loop stopped")
			return nil
		case <-ticker.C:
			o.runOptimizationCycle(ctx)
		}
	}
}

// runOptimizationCycle runs one full optimization pass across all ServiceObjectives.
func (o *OptimizerController) runOptimizationCycle(ctx context.Context) {
	logger := ctrl.Log.WithName("optimizer")

	var soList slov1alpha1.ServiceObjectiveList
	if err := o.List(ctx, &soList); err != nil {
		logger.Error(err, "failed to list ServiceObjectives")
		return
	}

	for i := range soList.Items {
		so := &soList.Items[i]
		o.optimizeService(ctx, so)
	}
}

// optimizeService runs the solver for a single ServiceObjective.
func (o *OptimizerController) optimizeService(ctx context.Context, so *slov1alpha1.ServiceObjective) {
	logger := ctrl.Log.WithName("optimizer").WithValues("namespace", so.Namespace, "service", so.Spec.TargetRef.Name)

	// 1. Build solver input from current state.
	input := o.buildSolverInput(ctx, so)

	// 2. Get matching policies.
	policies, err := o.PolicyRecon.FindPoliciesForService(ctx, so)
	if err != nil {
		logger.Error(err, "failed to find matching policies")
		return
	}

	for _, p := range policies {
		key := cel.PolicyKey(&p)
		input.Policies = append(input.Policies, engine.MatchedPolicy{
			Policy: p,
			Key:    key,
		})
	}

	// 3. Solve.
	action, record, err := o.Solver.Solve(&input)
	if err != nil {
		logger.Error(err, "solver failed")
		return
	}

	// 4. Write to journal.
	if o.Journal != nil {
		if err := o.Journal.Write(record); err != nil {
			logger.Error(err, "failed to write decision record")
		}
	}

	// 5. Emit events.
	if action.Type != engine.ActionNoAction {
		eventType := "Normal"
		if action.DryRun {
			o.Recorder.Eventf(so, eventType, "DryRunDecision",
				"[dry-run] %s: %s", action.Type, action.Reason)
		} else {
			o.Recorder.Eventf(so, eventType, "OptimizationDecision",
				"%s: %s", action.Type, action.Reason)
		}

		logger.Info("optimization decision",
			"action", action.Type,
			"targetReplica", action.TargetReplica,
			"dryRun", action.DryRun,
			"confidence", action.Confidence,
			"reason", action.Reason,
		)

		// Actuate if registry is wired.
		if o.ActuatorReg != nil {
			o.actuate(ctx, so, action)
		}
	}
}

// actuate applies the solver's ScalingAction through the actuator pipeline:
//  1. Safety checks (emergency stop, cooldown, circuit breaker)
//  2. Actuation via CanaryController (if wired) or Registry directly
//  3. Outcome recording for circuit breaker
func (o *OptimizerController) actuate(ctx context.Context, so *slov1alpha1.ServiceObjective, action engine.ScalingAction) {
	logger := ctrl.Log.WithName("optimizer").WithValues("namespace", so.Namespace, "service", so.Spec.TargetRef.Name)

	ref := actuator.ServiceRef{
		Namespace: so.Namespace,
		Name:      so.Spec.TargetRef.Name,
	}

	opts := actuator.ActuationOptions{
		DryRun: action.DryRun,
	}

	// 1. Safety checks.
	if o.SafetyGuard != nil {
		if err := o.SafetyGuard.Allow(ctx, ref, opts); err != nil {
			logger.Info("actuation blocked by safety guard", "reason", err.Error())
			o.Recorder.Eventf(so, "Warning", "ActuationBlocked", "safety guard: %v", err)
			return
		}
	}

	// 2. Apply via canary controller or direct registry.
	var result actuator.ActuationResult
	var err error

	if o.CanaryCtrl != nil {
		result, err = o.CanaryCtrl.Apply(ctx, ref, action, opts, action.TargetReplica)
	} else {
		result, err = o.ActuatorReg.Apply(ctx, ref, action, opts)
	}

	if err != nil {
		logger.Error(err, "actuation failed")
		o.Recorder.Eventf(so, "Warning", "ActuationFailed", "actuation error: %v", err)
		if o.SafetyGuard != nil {
			o.SafetyGuard.RecordOutcome(ref, actuator.OutcomeDegraded)
		}
		return
	}

	if result.Applied {
		logger.Info("actuation applied", "changes", len(result.Changes))
		o.Recorder.Eventf(so, "Normal", "Actuated",
			"%s applied (%d changes)", action.Type, len(result.Changes))
		if o.SafetyGuard != nil {
			o.SafetyGuard.RecordActuation(ref)
			// Outcome will be updated later when SLO is re-evaluated.
			// For now, optimistically record as improved.
			o.SafetyGuard.RecordOutcome(ref, actuator.OutcomeImproved)
		}
	}
}

// buildSolverInput constructs the SolverInput from a ServiceObjective's current state.
// In the future, this will fetch real metrics from Prometheus and cross-reference
// TenantProfiles and ForecastResults. For now: populate from status + defaults.
func (o *OptimizerController) buildSolverInput(ctx context.Context, so *slov1alpha1.ServiceObjective) engine.SolverInput {
	input := engine.SolverInput{
		Namespace: so.Namespace,
		Service:   so.Spec.TargetRef.Name,
		Trigger:   "periodic",
		Current: cel.CurrentState{
			Replicas:   2, // default; will be populated from workload status in Phase 5
			CPURequest: 0.5,
		},
		SLO: cel.SLOStatus{
			Compliant: true, // default; populated from SO status conditions
		},
		Region:        "us-east-1",
		InstanceTypes: []string{"m5.large"},
	}

	// Populate SLO compliance from status conditions.
	for _, cond := range so.Status.Conditions {
		if cond.Type == "SLOCompliant" {
			input.SLO.Compliant = cond.Status == "True"
		}
	}

	return input
}
