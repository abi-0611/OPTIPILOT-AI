package tuning

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tuningv1alpha1 "github.com/optipilot-ai/optipilot/api/tuning/v1alpha1"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

type fixedSLO struct {
	value float64
	err   error
}

func (f *fixedSLO) FetchSLO(context.Context, string, string) (float64, error) {
	return f.value, f.err
}

type recordingApplier struct {
	calls []applyCall
	err   error
}
type applyCall struct {
	ns    string
	param string
	value string
}

func (r *recordingApplier) Apply(_ context.Context, ns string, p tuningv1alpha1.TunableParameter, v string) error {
	r.calls = append(r.calls, applyCall{ns: ns, param: p.Name, value: v})
	return r.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func intParam(name, min, max, step, def string) tuningv1alpha1.TunableParameter {
	return tuningv1alpha1.TunableParameter{
		Name: name, Type: tuningv1alpha1.ParamTypeInteger,
		Source: tuningv1alpha1.SourceConfigMap, Min: min, Max: max, Step: step, Default: def,
	}
}

func floatParam(name, min, max, step, def string) tuningv1alpha1.TunableParameter {
	return tuningv1alpha1.TunableParameter{
		Name: name, Type: tuningv1alpha1.ParamTypeFloat,
		Min: min, Max: max, Step: step, Default: def,
	}
}

func stringParam(name string, allowed []string, def string) tuningv1alpha1.TunableParameter {
	return tuningv1alpha1.TunableParameter{
		Name: name, Type: tuningv1alpha1.ParamTypeString,
		AllowedValues: allowed, Default: def,
	}
}

func obs(param, value string, slo float64, t time.Time) tuningv1alpha1.ParameterObservation {
	return tuningv1alpha1.ParameterObservation{
		ParameterName: param, Value: value, SLOValue: slo, ObservedAt: metav1.NewTime(t),
	}
}

func testAT(params []tuningv1alpha1.TunableParameter) *tuningv1alpha1.ApplicationTuning {
	return &tuningv1alpha1.ApplicationTuning{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: tuningv1alpha1.ApplicationTuningSpec{
			TargetRef:          tuningv1alpha1.TuningTargetRef{Kind: "Deployment", Name: "app"},
			Parameters:         params,
			OptimizationTarget: tuningv1alpha1.OptimizationTarget{MetricName: "latency", Objective: "maximize"},
			SafetyPolicy:       &tuningv1alpha1.TuningSafetyPolicy{MaxChangePercent: 50, CooldownMinutes: 1, RollbackOnSLOViolation: false},
		},
	}
}

// ---------------------------------------------------------------------------
// GenerateGrid
// ---------------------------------------------------------------------------

func TestGenerateGrid_Integer(t *testing.T) {
	grid, err := GenerateGrid(intParam("x", "1", "10", "1", "5"), 20)
	if err != nil {
		t.Fatal(err)
	}
	if grid[0].Value != "1" {
		t.Errorf("first=%s, want 1", grid[0].Value)
	}
	if grid[len(grid)-1].Value != "10" {
		t.Errorf("last=%s, want 10", grid[len(grid)-1].Value)
	}
}

func TestGenerateGrid_Float(t *testing.T) {
	grid, err := GenerateGrid(floatParam("x", "0.1", "1.0", "0.1", "0.5"), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(grid) < 2 {
		t.Fatalf("got %d points", len(grid))
	}
}

func TestGenerateGrid_String(t *testing.T) {
	grid, err := GenerateGrid(stringParam("lvl", []string{"debug", "info", "warn"}, "info"), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(grid) != 3 {
		t.Errorf("got %d, want 3", len(grid))
	}
}

func TestGenerateGrid_StringNoAllowed(t *testing.T) {
	_, err := GenerateGrid(tuningv1alpha1.TunableParameter{Name: "x", Type: tuningv1alpha1.ParamTypeString}, 20)
	if err == nil {
		t.Error("expected error")
	}
}

func TestGenerateGrid_InvalidMin(t *testing.T) {
	_, err := GenerateGrid(intParam("x", "NaN", "10", "1", "5"), 20)
	if err == nil {
		t.Error("expected error")
	}
}

func TestGenerateGrid_MaxLEMin(t *testing.T) {
	_, err := GenerateGrid(intParam("x", "10", "5", "1", "7"), 20)
	if err == nil {
		t.Error("expected error")
	}
}

func TestGenerateGrid_MaxPointsCap(t *testing.T) {
	grid, err := GenerateGrid(intParam("x", "1", "1000", "1", "100"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(grid) > 6 { // 5 + possible endpoint
		t.Errorf("got %d, max expected 6", len(grid))
	}
}

func TestGenerateGrid_DefaultStep(t *testing.T) {
	// no step → (max-min)/10
	grid, err := GenerateGrid(intParam("x", "0", "100", "", "50"), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(grid) < 2 {
		t.Errorf("got %d points", len(grid))
	}
}

// ---------------------------------------------------------------------------
// BestFromObservations
// ---------------------------------------------------------------------------

func TestBestFromObservations_Max(t *testing.T) {
	base := time.Now()
	result, ok := BestFromObservations("x", []tuningv1alpha1.ParameterObservation{
		obs("x", "10", 95.0, base),
		obs("x", "20", 99.0, base.Add(time.Minute)),
		obs("x", "30", 97.0, base.Add(2*time.Minute)),
	})
	if !ok || result.BestValue != "20" || result.BestSLO != 99.0 || result.NumObserved != 3 {
		t.Errorf("got %+v", result)
	}
}

func TestBestFromObservations_NoMatch(t *testing.T) {
	_, ok := BestFromObservations("x", []tuningv1alpha1.ParameterObservation{
		obs("other", "1", 90.0, time.Now()),
	})
	if ok {
		t.Error("expected ok=false")
	}
}

func TestBestFromObservations_Nil(t *testing.T) {
	_, ok := BestFromObservations("x", nil)
	if ok {
		t.Error("expected ok=false")
	}
}

// ---------------------------------------------------------------------------
// SelectOptimal
// ---------------------------------------------------------------------------

func TestSelectOptimal_BestObserved_Maximize(t *testing.T) {
	grid := []GridPoint{{Value: "10"}, {Value: "20"}, {Value: "30"}}
	base := time.Now()
	observations := []tuningv1alpha1.ParameterObservation{
		obs("x", "10", 95.0, base),
		obs("x", "20", 99.0, base.Add(time.Minute)),
		obs("x", "30", 97.0, base.Add(2*time.Minute)),
	}
	val, known := SelectOptimal(intParam("x", "10", "30", "10", "10"), grid, observations, "maximize")
	if !known || val != "20" {
		t.Errorf("got %s (known=%v)", val, known)
	}
}

func TestSelectOptimal_Minimize(t *testing.T) {
	grid := []GridPoint{{Value: "10"}, {Value: "20"}, {Value: "30"}}
	base := time.Now()
	observations := []tuningv1alpha1.ParameterObservation{
		obs("x", "10", 50.0, base),
		obs("x", "20", 80.0, base.Add(time.Minute)),
		obs("x", "30", 90.0, base.Add(2*time.Minute)),
	}
	val, known := SelectOptimal(intParam("x", "10", "30", "10", "20"), grid, observations, "minimize")
	if !known || val != "10" {
		t.Errorf("got %s", val)
	}
}

func TestSelectOptimal_NoObs(t *testing.T) {
	grid := []GridPoint{{Value: "5"}, {Value: "10"}}
	val, known := SelectOptimal(intParam("x", "5", "10", "5", "5"), grid, nil, "maximize")
	if known || val != "5" {
		t.Errorf("got %s (known=%v)", val, known)
	}
}

func TestSelectOptimal_EmptyGrid(t *testing.T) {
	val, _ := SelectOptimal(intParam("x", "5", "10", "5", "42"), nil, nil, "maximize")
	if val != "42" {
		t.Errorf("got %s", val)
	}
}

// ---------------------------------------------------------------------------
// SafetyCheck
// ---------------------------------------------------------------------------

func TestSafetyCheck_CooldownActive(t *testing.T) {
	future := metav1.NewTime(time.Now().Add(10 * time.Minute))
	sc := SafetyCheck{Policy: tuningv1alpha1.TuningSafetyPolicy{CooldownMinutes: 5}, NowFn: time.Now}
	ok, reason := sc.CanChange(tuningv1alpha1.ApplicationTuningStatus{CooldownUntil: &future})
	if ok {
		t.Error("expected false")
	}
	if reason == "" {
		t.Error("expected reason")
	}
}

func TestSafetyCheck_CooldownExpired(t *testing.T) {
	past := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	sc := SafetyCheck{
		Policy: tuningv1alpha1.TuningSafetyPolicy{CooldownMinutes: 5, SLOThresholdPercent: 95.0},
		NowFn:  time.Now,
	}
	ok, _ := sc.CanChange(tuningv1alpha1.ApplicationTuningStatus{CooldownUntil: &past, BestSLOValue: 99.0})
	if !ok {
		t.Error("expected true")
	}
}

func TestSafetyCheck_SLOBelowThreshold(t *testing.T) {
	sc := SafetyCheck{
		Policy: tuningv1alpha1.TuningSafetyPolicy{RollbackOnSLOViolation: true, SLOThresholdPercent: 99.0},
		NowFn:  time.Now,
	}
	ok, _ := sc.CanChange(tuningv1alpha1.ApplicationTuningStatus{BestSLOValue: 95.0})
	if ok {
		t.Error("expected false below threshold")
	}
}

func TestSafetyCheck_RollbackDisabled(t *testing.T) {
	sc := SafetyCheck{
		Policy: tuningv1alpha1.TuningSafetyPolicy{RollbackOnSLOViolation: false, SLOThresholdPercent: 99.0},
		NowFn:  time.Now,
	}
	ok, _ := sc.CanChange(tuningv1alpha1.ApplicationTuningStatus{BestSLOValue: 10.0})
	if !ok {
		t.Error("expected true when rollback disabled")
	}
}

// ---------------------------------------------------------------------------
// WithinChangeBounds
// ---------------------------------------------------------------------------

func TestWithinChangeBounds_Within(t *testing.T) {
	if !WithinChangeBounds(intParam("x", "1", "100", "1", "10"), "10", "14", 50) {
		t.Error("14 is 40% of 10, within 50%")
	}
}

func TestWithinChangeBounds_Exceeds(t *testing.T) {
	if WithinChangeBounds(intParam("x", "1", "100", "1", "10"), "10", "25", 50) {
		t.Error("25 is 150% of 10, exceeds 50%")
	}
}

func TestWithinChangeBounds_String(t *testing.T) {
	if !WithinChangeBounds(stringParam("l", []string{"a", "b"}, "a"), "a", "b", 10) {
		t.Error("string always passes")
	}
}

func TestWithinChangeBounds_ZeroPct(t *testing.T) {
	if !WithinChangeBounds(intParam("x", "1", "100", "1", "10"), "10", "14", 0) {
		t.Error("0 → default 50%, 14/10=40% should pass")
	}
}

// ---------------------------------------------------------------------------
// clampToMaxChange
// ---------------------------------------------------------------------------

func TestClamp_Integer(t *testing.T) {
	r := clampToMaxChange(intParam("x", "1", "100", "1", "10"), "10", "25", 50)
	if r != "15" {
		t.Errorf("got %s, want 15", r)
	}
}

func TestClamp_Float(t *testing.T) {
	r := clampToMaxChange(floatParam("x", "0.1", "2.0", "0.1", "1.0"), "1.0", "0.1", 50)
	if r != "0.5" {
		t.Errorf("got %s, want 0.5", r)
	}
}

func TestClamp_String_Passthrough(t *testing.T) {
	r := clampToMaxChange(stringParam("l", []string{"a", "b"}, "a"), "a", "b", 10)
	if r != "b" {
		t.Errorf("got %s", r)
	}
}

func TestClamp_WithinBounds(t *testing.T) {
	r := clampToMaxChange(intParam("x", "1", "100", "1", "10"), "10", "12", 50)
	if r != "12" {
		t.Errorf("got %s, want 12", r)
	}
}

// ---------------------------------------------------------------------------
// NextParameterToTune
// ---------------------------------------------------------------------------

func TestNextParam_Empty(t *testing.T) {
	if NextParameterToTune(nil, "", nil) != -1 {
		t.Error("expected -1")
	}
}

func TestNextParam_SkipsActive(t *testing.T) {
	params := []tuningv1alpha1.TunableParameter{
		intParam("a", "1", "10", "1", "5"),
		intParam("b", "1", "10", "1", "5"),
	}
	if idx := NextParameterToTune(params, "a", nil); idx != 1 {
		t.Errorf("got %d, want 1", idx)
	}
}

func TestNextParam_LeastObserved(t *testing.T) {
	params := []tuningv1alpha1.TunableParameter{
		intParam("a", "1", "10", "1", "5"),
		intParam("b", "1", "10", "1", "5"),
		intParam("c", "1", "10", "1", "5"),
	}
	base := time.Now()
	observations := []tuningv1alpha1.ParameterObservation{
		obs("a", "5", 98.0, base),
		obs("a", "6", 97.0, base.Add(time.Minute)),
		obs("b", "3", 95.0, base.Add(2*time.Minute)),
	}
	idx := NextParameterToTune(params, "", observations)
	if params[idx].Name != "c" {
		t.Errorf("got %s, want c", params[idx].Name)
	}
}

// ---------------------------------------------------------------------------
// resolveSafetyPolicy / isConverged
// ---------------------------------------------------------------------------

func TestResolveSafetyPolicy_Nil(t *testing.T) {
	sp := resolveSafetyPolicy(nil)
	if sp.MaxChangePercent != 50 || sp.CooldownMinutes != 5 || !sp.RollbackOnSLOViolation || sp.SLOThresholdPercent != 95.0 {
		t.Errorf("defaults wrong: %+v", sp)
	}
}

func TestResolveSafetyPolicy_ZeroFills(t *testing.T) {
	sp := resolveSafetyPolicy(&tuningv1alpha1.TuningSafetyPolicy{})
	if sp.MaxChangePercent != 50 || sp.SLOThresholdPercent != 95.0 {
		t.Errorf("zero-fill wrong: %+v", sp)
	}
}

func TestIsConverged_True(t *testing.T) {
	params := []tuningv1alpha1.TunableParameter{intParam("a", "1", "10", "1", "5")}
	base := time.Now()
	observations := []tuningv1alpha1.ParameterObservation{
		obs("a", "1", 95, base), obs("a", "2", 96, base.Add(time.Minute)),
		obs("a", "3", 97, base.Add(2*time.Minute)),
	}
	if !isConverged(params, observations) {
		t.Error("expected converged")
	}
}

func TestIsConverged_False(t *testing.T) {
	params := []tuningv1alpha1.TunableParameter{intParam("a", "1", "10", "1", "5")}
	observations := []tuningv1alpha1.ParameterObservation{
		obs("a", "1", 95, time.Now()), obs("a", "2", 96, time.Now()),
	}
	if isConverged(params, observations) {
		t.Error("expected not converged")
	}
}

func TestIsConverged_Empty(t *testing.T) {
	if isConverged(nil, nil) {
		t.Error("expected false")
	}
}

// ---------------------------------------------------------------------------
// RunCycle integration
// ---------------------------------------------------------------------------

func TestRunCycle_Paused(t *testing.T) {
	at := testAT([]tuningv1alpha1.TunableParameter{intParam("x", "1", "10", "1", "5")})
	at.Spec.Paused = true
	opt := NewOptimizer(&fixedSLO{value: 99}, &recordingApplier{})
	r, err := opt.RunCycle(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	if r.NewPhase != tuningv1alpha1.TuningPaused {
		t.Errorf("phase=%s", r.NewPhase)
	}
}

func TestRunCycle_SLOError(t *testing.T) {
	at := testAT([]tuningv1alpha1.TunableParameter{intParam("x", "1", "10", "1", "5")})
	opt := NewOptimizer(&fixedSLO{err: fmt.Errorf("prom down")}, &recordingApplier{})
	r, _ := opt.RunCycle(context.Background(), at)
	if r.NewPhase != tuningv1alpha1.TuningError {
		t.Errorf("phase=%s", r.NewPhase)
	}
}

func TestRunCycle_FirstExploration(t *testing.T) {
	at := testAT([]tuningv1alpha1.TunableParameter{intParam("x", "1", "10", "1", "5")})
	app := &recordingApplier{}
	opt := NewOptimizer(&fixedSLO{value: 99}, app)
	r, err := opt.RunCycle(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	if r.NewPhase == tuningv1alpha1.TuningError {
		t.Fatalf("error: %s", r.Message)
	}
	if len(app.calls) != 1 {
		t.Fatalf("expected 1 apply, got %d", len(app.calls))
	}
	if app.calls[0].param != "x" {
		t.Errorf("param=%s", app.calls[0].param)
	}
}

func TestRunCycle_RecordsObservation(t *testing.T) {
	at := testAT([]tuningv1alpha1.TunableParameter{intParam("x", "1", "10", "1", "5")})
	at.Status.ActiveParameter = "x"
	at.Status.CurrentValues = map[string]string{"x": "5"}
	opt := NewOptimizer(&fixedSLO{value: 97.5}, &recordingApplier{})
	r, _ := opt.RunCycle(context.Background(), at)
	if r.Observation == nil || r.Observation.SLOValue != 97.5 {
		t.Errorf("observation=%+v", r.Observation)
	}
}

func TestRunCycle_InCooldown(t *testing.T) {
	at := testAT([]tuningv1alpha1.TunableParameter{intParam("x", "1", "10", "1", "5")})
	future := metav1.NewTime(time.Now().Add(10 * time.Minute))
	at.Status.CooldownUntil = &future
	app := &recordingApplier{}
	opt := NewOptimizer(&fixedSLO{value: 99}, app)
	r, _ := opt.RunCycle(context.Background(), at)
	if r.NewPhase != tuningv1alpha1.TuningObserving {
		t.Errorf("phase=%s", r.NewPhase)
	}
	if len(app.calls) != 0 {
		t.Errorf("should not apply during cooldown")
	}
}

func TestRunCycle_Converges(t *testing.T) {
	at := testAT([]tuningv1alpha1.TunableParameter{intParam("x", "1", "10", "1", "5")})
	base := time.Now()
	at.Status.Observations = []tuningv1alpha1.ParameterObservation{
		obs("x", "3", 96, base), obs("x", "5", 99, base.Add(time.Minute)),
		obs("x", "8", 97, base.Add(2*time.Minute)),
	}
	opt := NewOptimizer(&fixedSLO{value: 99}, &recordingApplier{})
	r, _ := opt.RunCycle(context.Background(), at)
	if r.NewPhase != tuningv1alpha1.TuningConverged || !r.Converged {
		t.Errorf("phase=%s converged=%v", r.NewPhase, r.Converged)
	}
}

func TestRunCycle_ApplierError(t *testing.T) {
	at := testAT([]tuningv1alpha1.TunableParameter{intParam("x", "1", "10", "1", "5")})
	opt := NewOptimizer(&fixedSLO{value: 99}, &recordingApplier{err: fmt.Errorf("fail")})
	r, _ := opt.RunCycle(context.Background(), at)
	if r.NewPhase != tuningv1alpha1.TuningError {
		t.Errorf("phase=%s", r.NewPhase)
	}
}

func TestRunCycle_SetsCooldown(t *testing.T) {
	at := testAT([]tuningv1alpha1.TunableParameter{intParam("x", "1", "10", "1", "5")})
	at.Spec.SafetyPolicy.CooldownMinutes = 3
	opt := NewOptimizer(&fixedSLO{value: 99}, &recordingApplier{})
	r, _ := opt.RunCycle(context.Background(), at)
	if r.ParameterChanged != "" && r.NewCooldownUntil == nil {
		t.Error("expected cooldown set")
	}
}

func TestRunCycle_MultipleParams_CyclesThrough(t *testing.T) {
	params := []tuningv1alpha1.TunableParameter{
		intParam("a", "1", "10", "1", "5"),
		intParam("b", "1", "10", "1", "5"),
	}
	at := testAT(params)
	at.Status.ActiveParameter = "a"
	at.Status.CurrentValues = map[string]string{"a": "5"}

	app := &recordingApplier{}
	opt := NewOptimizer(&fixedSLO{value: 99}, app)
	r, _ := opt.RunCycle(context.Background(), at)

	// Should pick b (not a, which is active).
	if r.ParameterChanged != "b" {
		t.Errorf("expected b, got %s", r.ParameterChanged)
	}
}
