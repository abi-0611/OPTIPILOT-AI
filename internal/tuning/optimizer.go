// Package tuning implements the ApplicationTuning parameter optimizer.
//
// It performs grid search over each tunable parameter's range, maintains a
// history of (parameter-value → SLO-compliance) observations, selects the
// optimal point, and applies changes one parameter at a time with a safety
// cooldown between adjustments.
package tuning

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tuningv1alpha1 "github.com/optipilot-ai/optipilot/api/tuning/v1alpha1"
)

// ---------------------------------------------------------------------------
// Public interfaces
// ---------------------------------------------------------------------------

// SLOFetcher retrieves the current SLO compliance value (0.0–100.0) for the
// target workload.
type SLOFetcher interface {
	FetchSLO(ctx context.Context, namespace, name string) (float64, error)
}

// ParameterApplier writes a tuning parameter value to its backing source
// (ConfigMap key or environment variable via Deployment patch).
type ParameterApplier interface {
	Apply(ctx context.Context, namespace string, param tuningv1alpha1.TunableParameter, value string) error
}

// ---------------------------------------------------------------------------
// Optimizer
// ---------------------------------------------------------------------------

// Optimizer drives one optimization cycle for an ApplicationTuning resource.
type Optimizer struct {
	fetcher SLOFetcher
	applier ParameterApplier
	nowFn   func() time.Time
}

// NewOptimizer constructs an Optimizer.
func NewOptimizer(fetcher SLOFetcher, applier ParameterApplier) *Optimizer {
	return &Optimizer{
		fetcher: fetcher,
		applier: applier,
		nowFn:   time.Now,
	}
}

// ---------------------------------------------------------------------------
// Grid generation
// ---------------------------------------------------------------------------

// GridPoint is a candidate value in the parameter search space.
type GridPoint struct {
	Value string
	Float float64
}

// GenerateGrid returns evenly-spaced candidate values for a tunable parameter.
// String parameters with AllowedValues produce one point per allowed value.
// Numeric parameters generate min…max in step increments (capped at maxPoints).
func GenerateGrid(p tuningv1alpha1.TunableParameter, maxPoints int) ([]GridPoint, error) {
	if p.Type == tuningv1alpha1.ParamTypeString {
		if len(p.AllowedValues) == 0 {
			return nil, fmt.Errorf("string parameter %q has no AllowedValues", p.Name)
		}
		pts := make([]GridPoint, len(p.AllowedValues))
		for i, v := range p.AllowedValues {
			pts[i] = GridPoint{Value: v}
		}
		return pts, nil
	}

	minVal, err := strconv.ParseFloat(p.Min, 64)
	if err != nil || math.IsNaN(minVal) || math.IsInf(minVal, 0) {
		return nil, fmt.Errorf("parameter %q: invalid min %q", p.Name, p.Min)
	}
	maxVal, err := strconv.ParseFloat(p.Max, 64)
	if err != nil || math.IsNaN(maxVal) || math.IsInf(maxVal, 0) {
		return nil, fmt.Errorf("parameter %q: invalid max %q", p.Name, p.Max)
	}
	if maxVal <= minVal {
		return nil, fmt.Errorf("parameter %q: max (%v) must be > min (%v)", p.Name, maxVal, minVal)
	}

	step := (maxVal - minVal) / 10.0
	if p.Step != "" {
		if s, err2 := strconv.ParseFloat(p.Step, 64); err2 == nil && s > 0 {
			step = s
		}
	}

	var pts []GridPoint
	for v := minVal; v <= maxVal+1e-9; v += step {
		fv := math.Min(v, maxVal)
		formatted := formatValue(p.Type, fv)
		pts = append(pts, GridPoint{Value: formatted, Float: fv})
		if len(pts) >= maxPoints {
			break
		}
	}
	// Ensure max endpoint is included.
	if len(pts) > 0 && pts[len(pts)-1].Float < maxVal-1e-9 {
		pts = append(pts, GridPoint{Value: formatValue(p.Type, maxVal), Float: maxVal})
	}
	return pts, nil
}

func formatValue(t tuningv1alpha1.ParameterType, v float64) string {
	if t == tuningv1alpha1.ParamTypeInteger {
		return strconv.FormatInt(int64(math.Round(v)), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ---------------------------------------------------------------------------
// Correlation analysis
// ---------------------------------------------------------------------------

// CorrelationResult holds the best-known value for a parameter.
type CorrelationResult struct {
	ParameterName string
	BestValue     string
	BestSLO       float64
	NumObserved   int
}

// BestFromObservations selects the observation with the highest SLO value for
// the given parameter. Always selects maximum SLO (caller negates for minimize).
func BestFromObservations(paramName string, obs []tuningv1alpha1.ParameterObservation) (CorrelationResult, bool) {
	var best *tuningv1alpha1.ParameterObservation
	count := 0
	for i := range obs {
		if obs[i].ParameterName != paramName {
			continue
		}
		count++
		if best == nil || obs[i].SLOValue > best.SLOValue {
			best = &obs[i]
		}
	}
	if best == nil {
		return CorrelationResult{}, false
	}
	return CorrelationResult{
		ParameterName: paramName,
		BestValue:     best.Value,
		BestSLO:       best.SLOValue,
		NumObserved:   count,
	}, true
}

// ---------------------------------------------------------------------------
// Optimal selection
// ---------------------------------------------------------------------------

// SelectOptimal picks the best grid point by correlating candidates against
// existing observations. Returns (bestValue, isKnown); isKnown=false means no
// observations yet — the first grid point is returned for exploration.
func SelectOptimal(
	param tuningv1alpha1.TunableParameter,
	grid []GridPoint,
	obs []tuningv1alpha1.ParameterObservation,
	objective string,
) (string, bool) {
	if len(grid) == 0 {
		return param.Default, false
	}

	// Build index: value → best SLO.
	sloByValue := make(map[string]float64, len(obs))
	for _, o := range obs {
		if o.ParameterName != param.Name {
			continue
		}
		slo := o.SLOValue
		if objective == "minimize" {
			slo = -slo
		}
		if existing, ok := sloByValue[o.Value]; !ok || slo > existing {
			sloByValue[o.Value] = slo
		}
	}

	bestVal := ""
	bestSLO := math.Inf(-1)
	for _, gp := range grid {
		if slo, ok := sloByValue[gp.Value]; ok && slo > bestSLO {
			bestSLO = slo
			bestVal = gp.Value
		}
	}
	if bestVal != "" {
		return bestVal, true
	}
	return grid[0].Value, false
}

// ---------------------------------------------------------------------------
// Safety gate
// ---------------------------------------------------------------------------

// SafetyCheck reports whether it is safe to apply a tuning change.
type SafetyCheck struct {
	Policy tuningv1alpha1.TuningSafetyPolicy
	NowFn  func() time.Time
}

// CanChange returns (true, "") if safe, or (false, reason).
func (s SafetyCheck) CanChange(status tuningv1alpha1.ApplicationTuningStatus) (bool, string) {
	now := s.NowFn()
	if status.CooldownUntil != nil && now.Before(status.CooldownUntil.Time) {
		remaining := status.CooldownUntil.Time.Sub(now).Round(time.Second)
		return false, fmt.Sprintf("in cooldown for %s", remaining)
	}
	if s.Policy.RollbackOnSLOViolation && status.BestSLOValue > 0 {
		threshold := s.Policy.SLOThresholdPercent
		if threshold == 0 {
			threshold = 95.0
		}
		if status.BestSLOValue < threshold {
			return false, fmt.Sprintf("SLO %.2f%% below threshold %.2f%%", status.BestSLOValue, threshold)
		}
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// Change-magnitude guard
// ---------------------------------------------------------------------------

// WithinChangeBounds returns true if the proposed value does not deviate from
// the current value by more than maxChangePercent (numeric params only).
func WithinChangeBounds(param tuningv1alpha1.TunableParameter, currentValue, proposedValue string, maxChangePercent int32) bool {
	if param.Type == tuningv1alpha1.ParamTypeString {
		return true
	}
	cur, err1 := strconv.ParseFloat(currentValue, 64)
	prop, err2 := strconv.ParseFloat(proposedValue, 64)
	if err1 != nil || err2 != nil || cur == 0 {
		return true
	}
	diff := math.Abs(prop-cur) / math.Abs(cur) * 100.0
	limit := float64(maxChangePercent)
	if limit == 0 {
		limit = 50.0
	}
	return diff <= limit
}

// clampToMaxChange adjusts proposedValue so it stays within maxPct of current.
func clampToMaxChange(param tuningv1alpha1.TunableParameter, currentValue, proposedValue string, maxPct int32) string {
	if param.Type == tuningv1alpha1.ParamTypeString {
		return proposedValue
	}
	cur, err1 := strconv.ParseFloat(currentValue, 64)
	prop, err2 := strconv.ParseFloat(proposedValue, 64)
	if err1 != nil || err2 != nil || cur == 0 {
		return proposedValue
	}
	limit := float64(maxPct) / 100.0
	if limit == 0 {
		limit = 0.5
	}
	maxDelta := math.Abs(cur) * limit
	if prop > cur+maxDelta {
		prop = cur + maxDelta
	} else if prop < cur-maxDelta {
		prop = cur - maxDelta
	}
	return formatValue(param.Type, prop)
}

// ---------------------------------------------------------------------------
// Next-parameter selection
// ---------------------------------------------------------------------------

// NextParameterToTune returns the index of the parameter to tune next.
// Picks the least-observed parameter, skipping the currently active one.
func NextParameterToTune(params []tuningv1alpha1.TunableParameter, activeParam string, obs []tuningv1alpha1.ParameterObservation) int {
	if len(params) == 0 {
		return -1
	}
	counts := make(map[string]int, len(params))
	for _, o := range obs {
		counts[o.ParameterName]++
	}
	type ranked struct {
		idx   int
		name  string
		count int
	}
	r := make([]ranked, len(params))
	for i, p := range params {
		r[i] = ranked{idx: i, name: p.Name, count: counts[p.Name]}
	}
	sort.Slice(r, func(a, b int) bool { return r[a].count < r[b].count })

	for _, item := range r {
		if item.name != activeParam {
			return item.idx
		}
	}
	return r[0].idx
}

// ---------------------------------------------------------------------------
// Full cycle
// ---------------------------------------------------------------------------

// CycleResult describes the outcome of one optimization cycle.
type CycleResult struct {
	NewPhase         tuningv1alpha1.TuningPhase
	ParameterChanged string
	NewValue         string
	Observation      *tuningv1alpha1.ParameterObservation
	NewCooldownUntil *time.Time
	Message          string
	Converged        bool
}

const maxGridPoints = 20
const minObservationsForConvergence = 3

// RunCycle executes one optimization cycle. The caller persists status changes.
func (o *Optimizer) RunCycle(ctx context.Context, at *tuningv1alpha1.ApplicationTuning) (CycleResult, error) {
	if at.Spec.Paused {
		return CycleResult{NewPhase: tuningv1alpha1.TuningPaused, Message: "tuning paused"}, nil
	}

	namespace := at.Namespace
	targetName := at.Spec.TargetRef.Name

	// 1. Fetch current SLO.
	sloValue, err := o.fetcher.FetchSLO(ctx, namespace, targetName)
	if err != nil {
		return CycleResult{
			NewPhase: tuningv1alpha1.TuningError,
			Message:  fmt.Sprintf("failed to fetch SLO: %v", err),
		}, nil
	}

	now := o.nowFn()
	result := CycleResult{NewPhase: tuningv1alpha1.TuningExploring}

	// 2. Record observation for active parameter.
	var newObs *tuningv1alpha1.ParameterObservation
	if at.Status.ActiveParameter != "" {
		activeVal := at.Status.CurrentValues[at.Status.ActiveParameter]
		obs := tuningv1alpha1.ParameterObservation{
			ParameterName: at.Status.ActiveParameter,
			Value:         activeVal,
			SLOValue:      sloValue,
			ObservedAt:    metav1.NewTime(now),
		}
		newObs = &obs
		result.Observation = &obs
	}

	// Merge into working copy.
	allObs := make([]tuningv1alpha1.ParameterObservation, len(at.Status.Observations))
	copy(allObs, at.Status.Observations)
	if newObs != nil {
		allObs = append(allObs, *newObs)
	}

	// 3. Safety check.
	policy := resolveSafetyPolicy(at.Spec.SafetyPolicy)
	sc := SafetyCheck{Policy: policy, NowFn: o.nowFn}
	if ok, reason := sc.CanChange(at.Status); !ok {
		result.NewPhase = tuningv1alpha1.TuningObserving
		result.Message = reason
		return result, nil
	}

	// 4. Convergence check.
	if isConverged(at.Spec.Parameters, allObs) {
		return CycleResult{
			NewPhase:  tuningv1alpha1.TuningConverged,
			Converged: true,
			Message:   "all parameters converged",
		}, nil
	}

	// 5. Pick next parameter.
	nextIdx := NextParameterToTune(at.Spec.Parameters, at.Status.ActiveParameter, allObs)
	if nextIdx < 0 {
		return CycleResult{
			NewPhase: tuningv1alpha1.TuningError,
			Message:  "no parameters defined",
		}, nil
	}
	param := at.Spec.Parameters[nextIdx]

	// 6. Build grid and select optimal value.
	grid, err := GenerateGrid(param, maxGridPoints)
	if err != nil {
		return CycleResult{
			NewPhase: tuningv1alpha1.TuningError,
			Message:  fmt.Sprintf("grid generation for %q: %v", param.Name, err),
		}, nil
	}

	objective := at.Spec.OptimizationTarget.Objective
	if objective == "" {
		objective = "minimize"
	}
	newValue, _ := SelectOptimal(param, grid, allObs, objective)

	// 7. Magnitude guard.
	currentVal := at.Status.CurrentValues[param.Name]
	if currentVal == "" {
		currentVal = param.Default
	}
	if !WithinChangeBounds(param, currentVal, newValue, policy.MaxChangePercent) {
		newValue = clampToMaxChange(param, currentVal, newValue, policy.MaxChangePercent)
	}

	// 8. Apply (only if value changed).
	if newValue != currentVal {
		if err := o.applier.Apply(ctx, namespace, param, newValue); err != nil {
			return CycleResult{
				NewPhase: tuningv1alpha1.TuningError,
				Message:  fmt.Sprintf("apply %q=%q: %v", param.Name, newValue, err),
			}, nil
		}
		result.ParameterChanged = param.Name
		result.NewValue = newValue

		cooldownMins := policy.CooldownMinutes
		if cooldownMins == 0 {
			cooldownMins = 5
		}
		cooldownEnd := now.Add(time.Duration(cooldownMins) * time.Minute)
		result.NewCooldownUntil = &cooldownEnd
	}

	result.Message = fmt.Sprintf("exploring %s=%s (SLO=%.2f%%)", param.Name, newValue, sloValue)
	return result, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func resolveSafetyPolicy(sp *tuningv1alpha1.TuningSafetyPolicy) tuningv1alpha1.TuningSafetyPolicy {
	if sp == nil {
		return tuningv1alpha1.TuningSafetyPolicy{
			MaxChangePercent:       50,
			CooldownMinutes:        5,
			RollbackOnSLOViolation: true,
			SLOThresholdPercent:    95.0,
		}
	}
	out := *sp
	if out.MaxChangePercent == 0 {
		out.MaxChangePercent = 50
	}
	if out.CooldownMinutes == 0 {
		out.CooldownMinutes = 5
	}
	if out.SLOThresholdPercent == 0 {
		out.SLOThresholdPercent = 95.0
	}
	return out
}

func isConverged(params []tuningv1alpha1.TunableParameter, obs []tuningv1alpha1.ParameterObservation) bool {
	if len(params) == 0 {
		return false
	}
	counts := make(map[string]int, len(params))
	for _, o := range obs {
		counts[o.ParameterName]++
	}
	for _, p := range params {
		if counts[p.Name] < minObservationsForConvergence {
			return false
		}
	}
	return true
}
