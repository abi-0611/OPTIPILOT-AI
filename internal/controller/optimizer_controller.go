package controller

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/actuator"
	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/explainability"
	"github.com/optipilot-ai/optipilot/internal/forecaster"
	prommetrics "github.com/optipilot-ai/optipilot/internal/metrics"
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
	PromClient  prommetrics.PrometheusClient
	Forecaster  *forecaster.Client // optional; nil skips ML but heuristic forecast may still run
	// Forecast tuning (zero = defaults in attachForecast): shorter lookback/step = more responsive.
	ForecastLookback  time.Duration
	ForecastStep      time.Duration
	ForecastMinPoints int
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
	if !serviceObjectiveReadyForOptimization(so) {
		logger.Info("skipping optimization until ServiceObjective has a concrete SLO evaluation")
		return
	}

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
	if len(policies) == 0 {
		logger.Info("no OptimizationPolicy matches this ServiceObjective — create one to control dry-run, constraints, and cooldowns; solver still runs with default weights")
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
			actuationOpts := buildActuationOptions(action, policies)
			o.actuate(ctx, so, action, actuationOpts, int32(input.Current.Replicas))
		}
	}
}

// actuate applies the solver's ScalingAction through the actuator pipeline:
//  1. Safety checks (emergency stop, cooldown, circuit breaker)
//  2. Actuation via CanaryController (if wired) or Registry directly
//  3. Outcome recording for circuit breaker
func (o *OptimizerController) actuate(ctx context.Context, so *slov1alpha1.ServiceObjective, action engine.ScalingAction, opts actuator.ActuationOptions, currentReplicas int32) {
	logger := ctrl.Log.WithName("optimizer").WithValues("namespace", so.Namespace, "service", so.Spec.TargetRef.Name)

	ref := actuator.ServiceRef{
		Namespace:  so.Namespace,
		Name:       so.Spec.TargetRef.Name,
		APIVersion: so.Spec.TargetRef.APIVersion,
		Kind:       so.Spec.TargetRef.Kind,
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
		result, err = o.CanaryCtrl.Apply(ctx, ref, action, opts, currentReplicas)
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
func (o *OptimizerController) buildSolverInput(ctx context.Context, so *slov1alpha1.ServiceObjective) engine.SolverInput {
	input := engine.SolverInput{
		Namespace: so.Namespace,
		Service:   so.Spec.TargetRef.Name,
		Trigger:   "periodic",
		Current: cel.CurrentState{
			Replicas:      1,
			CPURequest:    0.1,
			MemoryRequest: 0.25,
		},
		SLO:           buildSLOStatus(so),
		Region:        "us-east-1",
		InstanceTypes: []string{"m5.large"},
	}

	currentState, err := o.readCurrentState(ctx, so)
	if err != nil {
		ctrl.Log.WithName("optimizer").WithValues("namespace", so.Namespace, "service", so.Spec.TargetRef.Name).Error(err, "failed to read current workload state")
	} else {
		input.Current = currentState
	}

	// Compute right-sized resource recommendations from actual usage.
	if input.Current.CPUUsage > 0 && input.Current.Replicas > 0 {
		perReplicaCPU := (input.Current.CPUUsage / float64(input.Current.Replicas)) * 1.3
		if perReplicaCPU < 0.01 {
			perReplicaCPU = 0.01 // floor: 10m
		}
		if math.Abs(perReplicaCPU-input.Current.CPURequest) > input.Current.CPURequest*0.2 {
			input.RightSizedCPU = &perReplicaCPU
		}
	}
	if input.Current.MemoryUsage > 0 && input.Current.Replicas > 0 {
		perReplicaMemory := (input.Current.MemoryUsage / float64(input.Current.Replicas)) * 1.3
		if perReplicaMemory < 0.03125 {
			perReplicaMemory = 0.03125 // floor: 32Mi
		}
		if math.Abs(perReplicaMemory-input.Current.MemoryRequest) > input.Current.MemoryRequest*0.2 {
			input.RightSizedMemory = &perReplicaMemory
		}
	}

	input.Metrics = map[string]float64{
		"cpu_usage":        input.Current.CPUUsage,
		"memory_usage_gib": input.Current.MemoryUsage,
	}

	o.attachForecast(ctx, &input, so)

	return input
}

func (o *OptimizerController) readCurrentState(ctx context.Context, so *slov1alpha1.ServiceObjective) (cel.CurrentState, error) {
	state := cel.CurrentState{
		Replicas: 1,
	}

	if so.Spec.TargetRef.Kind != "Deployment" {
		return state, nil
	}

	var dep appsv1.Deployment
	if err := o.Get(ctx, types.NamespacedName{Namespace: so.Namespace, Name: so.Spec.TargetRef.Name}, &dep); err != nil {
		return state, err
	}

	if dep.Spec.Replicas != nil && *dep.Spec.Replicas > 0 {
		state.Replicas = int64(*dep.Spec.Replicas)
	}

	for _, container := range dep.Spec.Template.Spec.Containers {
		if qty, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			state.CPURequest += float64(qty.MilliValue()) / 1000.0
		}
		if qty, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			state.MemoryRequest += float64(qty.Value()) / (1024 * 1024 * 1024)
		}
	}

	if state.CPURequest == 0 {
		state.CPURequest = 0.1
	}
	if state.MemoryRequest == 0 {
		state.MemoryRequest = 0.25
	}

	state.CPUUsage = o.queryScalarOrZero(ctx, fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s-.*",container!="",container!="POD"}[5m]))`, so.Namespace, regexp.QuoteMeta(dep.Name)))
	state.MemoryUsage = o.queryScalarOrZero(ctx, fmt.Sprintf(`sum(container_memory_working_set_bytes{namespace="%s",pod=~"%s-.*",container!="",container!="POD"}) / 1073741824`, so.Namespace, regexp.QuoteMeta(dep.Name)))

	return state, nil
}

func (o *OptimizerController) queryScalarOrZero(ctx context.Context, query string) float64 {
	if o.PromClient == nil {
		return 0
	}

	value, err := o.PromClient.Query(ctx, query)
	if err != nil {
		if err == prommetrics.ErrNoData {
			return 0
		}
		ctrl.Log.WithName("optimizer").V(1).Info("prometheus query failed", "query", query, "error", err.Error())
		return 0
	}

	return value
}

func buildSLOStatus(so *slov1alpha1.ServiceObjective) cel.SLOStatus {
	status := cel.SLOStatus{}

	for _, cond := range so.Status.Conditions {
		if cond.Type == conditionSLOCompliant {
			status.Compliant = cond.Status == metav1.ConditionTrue
		}
	}

	status.BudgetRemaining = parsePercentOrZero(so.Status.BudgetRemaining)
	for _, burn := range so.Status.CurrentBurn {
		if value := parseFloatOrZero(burn); value > status.BurnRate {
			status.BurnRate = value
		}
	}

	return status
}

func serviceObjectiveReadyForOptimization(so *slov1alpha1.ServiceObjective) bool {
	targetFound := false
	sloKnown := false
	for _, cond := range so.Status.Conditions {
		switch cond.Type {
		case conditionTargetFound:
			targetFound = cond.Status == metav1.ConditionTrue
		case conditionSLOCompliant:
			sloKnown = cond.Status != metav1.ConditionUnknown
		}
	}
	return targetFound && sloKnown
}

func buildActuationOptions(action engine.ScalingAction, policies []policyv1alpha1.OptimizationPolicy) actuator.ActuationOptions {
	opts := actuator.ActuationOptions{DryRun: action.DryRun}

	for _, policy := range policies {
		if policy.Spec.ScalingBehavior == nil {
			continue
		}

		var rule *policyv1alpha1.ScalingRule
		switch action.Type {
		case engine.ActionScaleUp:
			rule = policy.Spec.ScalingBehavior.ScaleUp
		case engine.ActionScaleDown:
			rule = policy.Spec.ScalingBehavior.ScaleDown
		}

		if rule == nil {
			continue
		}

		if rule.MaxPercent > 0 {
			maxChange := float64(rule.MaxPercent) / 100.0
			if opts.MaxChange == 0 || maxChange < opts.MaxChange {
				opts.MaxChange = maxChange
			}
		}

		if rule.CooldownSeconds > opts.CooldownSeconds {
			opts.CooldownSeconds = rule.CooldownSeconds
		}
	}

	return opts
}

func parsePercentOrZero(raw string) float64 {
	trimmed := strings.TrimSpace(strings.TrimSuffix(raw, "%"))
	if trimmed == "" {
		return 0
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0
	}
	return value / 100.0
}

func parseFloatOrZero(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return value
}
