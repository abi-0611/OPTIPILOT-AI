package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func sampleAT() *ApplicationTuning {
	return &ApplicationTuning{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "ApplicationTuning",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "default",
		},
		Spec: ApplicationTuningSpec{
			TargetRef: TuningTargetRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "my-app",
			},
			Parameters: []TunableParameter{
				{
					Name:   "concurrency",
					Type:   ParamTypeInteger,
					Source: SourceConfigMap,
					ConfigMapRef: &ConfigMapRef{
						Name: "app-config",
						Key:  "MAX_CONCURRENCY",
					},
					Min:     "1",
					Max:     "100",
					Step:    "5",
					Default: "10",
				},
			},
			OptimizationTarget: OptimizationTarget{
				MetricName: "latency_p99",
				Objective:  "minimize",
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Enum constants
// ---------------------------------------------------------------------------

func TestParameterType_Values(t *testing.T) {
	for _, tc := range []struct {
		got  ParameterType
		want string
	}{
		{ParamTypeInteger, "integer"},
		{ParamTypeFloat, "float"},
		{ParamTypeString, "string"},
	} {
		if string(tc.got) != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestParameterSource_Values(t *testing.T) {
	if string(SourceConfigMap) != "configmap" {
		t.Errorf("got %s", SourceConfigMap)
	}
	if string(SourceEnv) != "env" {
		t.Errorf("got %s", SourceEnv)
	}
}

func TestTuningPhase_Values(t *testing.T) {
	phases := []TuningPhase{
		TuningIdle, TuningExploring, TuningObserving,
		TuningConverged, TuningRolledBack, TuningPaused, TuningError,
	}
	seen := make(map[TuningPhase]bool, len(phases))
	for _, p := range phases {
		if p == "" {
			t.Error("empty phase constant")
		}
		if seen[p] {
			t.Errorf("duplicate phase: %s", p)
		}
		seen[p] = true
	}
	if len(phases) != 7 {
		t.Errorf("expected 7 phases, got %d", len(phases))
	}
}

// ---------------------------------------------------------------------------
// Spec round-trip
// ---------------------------------------------------------------------------

func TestApplicationTuningSpec_ConfigMapParam(t *testing.T) {
	at := sampleAT()
	p := at.Spec.Parameters[0]
	if p.Name != "concurrency" || p.Type != ParamTypeInteger {
		t.Errorf("unexpected param: %+v", p)
	}
	if p.ConfigMapRef == nil || p.ConfigMapRef.Key != "MAX_CONCURRENCY" {
		t.Error("ConfigMapRef not set properly")
	}
}

func TestApplicationTuningSpec_EnvParam(t *testing.T) {
	p := TunableParameter{
		Name:       "batch-size",
		Type:       ParamTypeInteger,
		Source:     SourceEnv,
		EnvVarName: "BATCH_SIZE",
		Min:        "1",
		Max:        "512",
		Default:    "32",
	}
	if p.EnvVarName != "BATCH_SIZE" {
		t.Errorf("EnvVarName: %s", p.EnvVarName)
	}
}

func TestTunableParameter_AllowedValues(t *testing.T) {
	p := TunableParameter{
		Name:          "log-level",
		Type:          ParamTypeString,
		AllowedValues: []string{"debug", "info", "warn", "error"},
		Default:       "info",
	}
	if len(p.AllowedValues) != 4 {
		t.Errorf("AllowedValues: %d", len(p.AllowedValues))
	}
}

func TestOptimizationTarget_Both(t *testing.T) {
	for _, obj := range []string{"minimize", "maximize"} {
		ot := OptimizationTarget{MetricName: "m", Objective: obj}
		if ot.Objective != obj {
			t.Errorf("expected %s, got %s", obj, ot.Objective)
		}
	}
}

func TestOptimizationTarget_PromQL(t *testing.T) {
	ot := OptimizationTarget{
		MetricName: "custom",
		PromQLExpr: `histogram_quantile(0.99, rate(http_duration_bucket[5m]))`,
		Objective:  "minimize",
	}
	if ot.PromQLExpr == "" {
		t.Error("PromQLExpr should be set")
	}
}

func TestSafetyPolicy_Fields(t *testing.T) {
	sp := TuningSafetyPolicy{
		MaxChangePercent:       50,
		CooldownMinutes:        5,
		RollbackOnSLOViolation: true,
		SLOThresholdPercent:    95.0,
	}
	if sp.MaxChangePercent != 50 || sp.CooldownMinutes != 5 {
		t.Errorf("safety policy: %+v", sp)
	}
	if !sp.RollbackOnSLOViolation {
		t.Error("RollbackOnSLOViolation should be true")
	}
}

func TestSpec_PausedField(t *testing.T) {
	at := sampleAT()
	at.Spec.Paused = true
	if !at.Spec.Paused {
		t.Error("Paused should be true")
	}
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

func TestStatus_Fields(t *testing.T) {
	now := metav1.NewTime(time.Now())
	s := ApplicationTuningStatus{
		Phase:                TuningExploring,
		CurrentValues:        map[string]string{"concurrency": "20"},
		BestValues:           map[string]string{"concurrency": "15"},
		BestSLOValue:         99.5,
		ActiveParameter:      "concurrency",
		LastOptimizationTime: &now,
		Message:              "exploring",
	}
	if s.Phase != TuningExploring {
		t.Errorf("phase: %s", s.Phase)
	}
	if s.CurrentValues["concurrency"] != "20" {
		t.Error("CurrentValues mismatch")
	}
	if s.BestSLOValue != 99.5 {
		t.Errorf("BestSLOValue: %f", s.BestSLOValue)
	}
}

func TestParameterObservation_Fields(t *testing.T) {
	o := ParameterObservation{
		ParameterName: "concurrency",
		Value:         "50",
		SLOValue:      98.7,
		ObservedAt:    metav1.NewTime(time.Now()),
	}
	if o.SLOValue != 98.7 {
		t.Errorf("SLOValue: %f", o.SLOValue)
	}
}

// ---------------------------------------------------------------------------
// DeepCopy isolation
// ---------------------------------------------------------------------------

func TestDeepCopy_Spec_Isolation(t *testing.T) {
	at := sampleAT()
	at.Spec.SafetyPolicy = &TuningSafetyPolicy{MaxChangePercent: 25}

	cp := at.DeepCopy()
	cp.Spec.SafetyPolicy.MaxChangePercent = 99
	cp.Spec.Parameters[0].Name = "modified"

	if at.Spec.SafetyPolicy.MaxChangePercent != 25 {
		t.Error("SafetyPolicy leaked")
	}
	if at.Spec.Parameters[0].Name != "concurrency" {
		t.Error("Parameters slice leaked")
	}
}

func TestDeepCopy_Status_MapIsolation(t *testing.T) {
	at := sampleAT()
	at.Status.CurrentValues = map[string]string{"k": "v"}
	at.Status.BestValues = map[string]string{"k": "v"}

	cp := at.DeepCopy()
	cp.Status.CurrentValues["k"] = "mutated"
	cp.Status.BestValues["k"] = "mutated"

	if at.Status.CurrentValues["k"] != "v" {
		t.Error("CurrentValues map leaked")
	}
	if at.Status.BestValues["k"] != "v" {
		t.Error("BestValues map leaked")
	}
}

func TestDeepCopy_Observations_Isolation(t *testing.T) {
	at := sampleAT()
	at.Status.Observations = []ParameterObservation{
		{ParameterName: "x", Value: "1", SLOValue: 99.0, ObservedAt: metav1.Now()},
	}
	cp := at.DeepCopy()
	cp.Status.Observations[0].Value = "mutated"

	if at.Status.Observations[0].Value != "1" {
		t.Error("Observations leaked")
	}
}

func TestDeepCopy_AllowedValues_Isolation(t *testing.T) {
	p := &TunableParameter{AllowedValues: []string{"a", "b"}}
	cp := p.DeepCopy()
	cp.AllowedValues[0] = "z"
	if p.AllowedValues[0] != "a" {
		t.Error("AllowedValues leaked")
	}
}

func TestDeepCopy_NilReceiver(t *testing.T) {
	var at *ApplicationTuning
	if at.DeepCopy() != nil {
		t.Error("nil DeepCopy should return nil")
	}
	var spec *ApplicationTuningSpec
	if spec.DeepCopy() != nil {
		t.Error("nil spec DeepCopy should return nil")
	}
	var status *ApplicationTuningStatus
	if status.DeepCopy() != nil {
		t.Error("nil status DeepCopy should return nil")
	}
}

// ---------------------------------------------------------------------------
// runtime.Object interface
// ---------------------------------------------------------------------------

func TestRuntimeObject_ApplicationTuning(t *testing.T) {
	var _ runtime.Object = &ApplicationTuning{}
	var _ runtime.Object = &ApplicationTuningList{}
}

func TestDeepCopyObject_ReturnType(t *testing.T) {
	at := sampleAT()
	obj := at.DeepCopyObject()
	if _, ok := obj.(*ApplicationTuning); !ok {
		t.Error("DeepCopyObject should return *ApplicationTuning")
	}
}

func TestApplicationTuningList_DeepCopy(t *testing.T) {
	list := &ApplicationTuningList{
		Items: []ApplicationTuning{*sampleAT(), *sampleAT()},
	}
	cp := list.DeepCopy()
	if len(cp.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(cp.Items))
	}
	cp.Items[0].Name = "mutated"
	if list.Items[0].Name != "demo" {
		t.Error("list items leaked")
	}
}

// ---------------------------------------------------------------------------
// Scheme registration
// ---------------------------------------------------------------------------

func TestScheme_Registration(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	gvk := schema.GroupVersionKind{
		Group: "tuning.optipilot.ai", Version: "v1alpha1", Kind: "ApplicationTuning",
	}
	obj, err := s.New(gvk)
	if err != nil {
		t.Fatalf("scheme.New: %v", err)
	}
	if _, ok := obj.(*ApplicationTuning); !ok {
		t.Error("wrong type from scheme")
	}
}

func TestGroupVersion_Values(t *testing.T) {
	if GroupVersion.Group != "tuning.optipilot.ai" {
		t.Errorf("group: %s", GroupVersion.Group)
	}
	if GroupVersion.Version != "v1alpha1" {
		t.Errorf("version: %s", GroupVersion.Version)
	}
}
