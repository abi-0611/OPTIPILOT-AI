package actuator_test

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/optipilot-ai/optipilot/internal/actuator"
	"github.com/optipilot-ai/optipilot/internal/engine"
)

func podTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	return s
}

func makeDeployment(ns, name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: map[string]string{},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: deploymentPodTemplate(name),
		},
	}
}

func deploymentPodTemplate(name string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
	}
}

func makeHPA(ns, name, targetName string, min, max int32) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       targetName,
			},
			MinReplicas: &min,
			MaxReplicas: max,
		},
	}
}

func serviceRef(ns, name string) actuator.ServiceRef {
	return actuator.ServiceRef{
		Namespace:  ns,
		Name:       name,
		APIVersion: "apps/v1",
		Kind:       "Deployment",
	}
}

// ── CanApply ─────────────────────────────────────────────────────────────────

func TestPodActuator_CanApply(t *testing.T) {
	p := actuator.NewPodActuator(nil)
	cases := []struct {
		typ  engine.ActionType
		want bool
	}{
		{engine.ActionScaleUp, true},
		{engine.ActionScaleDown, true},
		{engine.ActionTune, true},
		{engine.ActionNoAction, false},
	}
	for _, c := range cases {
		got := p.CanApply(engine.ScalingAction{Type: c.typ})
		if got != c.want {
			t.Errorf("CanApply(%s) = %v, want %v", c.typ, got, c.want)
		}
	}
}

// ── HPA mode ─────────────────────────────────────────────────────────────────

func TestPodActuator_HPA_ScaleUp(t *testing.T) {
	deploy := makeDeployment("ns", "api", 4)
	hpa := makeHPA("ns", "api-hpa", "api", 2, 4)

	cl := fake.NewClientBuilder().
		WithScheme(podTestScheme()).
		WithObjects(deploy, hpa).
		Build()

	p := actuator.NewPodActuator(cl)
	result, err := p.Apply(context.Background(),
		serviceRef("ns", "api"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 8},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}
	if len(result.Changes) == 0 {
		t.Error("expected at least one change record")
	}

	// Verify HPA was patched.
	var updated autoscalingv2.HorizontalPodAutoscaler
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api-hpa"}, &updated)
	if updated.Spec.MaxReplicas != 8 {
		t.Errorf("HPA maxReplicas = %d, want 8", updated.Spec.MaxReplicas)
	}

	// Verify previous state stored.
	if result.PreviousState == "" {
		t.Error("expected PreviousState populated")
	}
}

func TestPodActuator_HPA_DryRun(t *testing.T) {
	deploy := makeDeployment("ns", "api", 4)
	hpa := makeHPA("ns", "api-hpa", "api", 4, 4)

	cl := fake.NewClientBuilder().
		WithScheme(podTestScheme()).
		WithObjects(deploy, hpa).
		Build()

	p := actuator.NewPodActuator(cl)
	result, err := p.Apply(context.Background(),
		serviceRef("ns", "api"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 10},
		actuator.ActuationOptions{DryRun: true},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Applied {
		t.Error("expected Applied=false in dry-run")
	}

	// HPA should NOT be changed.
	var unchanged autoscalingv2.HorizontalPodAutoscaler
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api-hpa"}, &unchanged)
	if unchanged.Spec.MaxReplicas != 4 {
		t.Errorf("HPA maxReplicas changed in dry-run: got %d", unchanged.Spec.MaxReplicas)
	}

	// But changes should still be reported for observability.
	if len(result.Changes) == 0 {
		t.Error("expected changes reported even in dry-run")
	}
}

func TestPodActuator_HPA_MaxChangeClamp(t *testing.T) {
	deploy := makeDeployment("ns", "api", 10)
	hpa := makeHPA("ns", "api-hpa", "api", 10, 10)

	cl := fake.NewClientBuilder().
		WithScheme(podTestScheme()).
		WithObjects(deploy, hpa).
		Build()

	p := actuator.NewPodActuator(cl)
	// Target=20, current=10, maxChange=0.3 → max allowed = 10+3 = 13
	_, err := p.Apply(context.Background(),
		serviceRef("ns", "api"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 20},
		actuator.ActuationOptions{MaxChange: 0.3},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated autoscalingv2.HorizontalPodAutoscaler
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api-hpa"}, &updated)
	if updated.Spec.MaxReplicas != 13 {
		t.Errorf("HPA maxReplicas = %d, want 13 (clamped)", updated.Spec.MaxReplicas)
	}
}

// ── Direct Deployment mode ───────────────────────────────────────────────────

func TestPodActuator_Deployment_ScaleDown_NoHPA(t *testing.T) {
	deploy := makeDeployment("ns", "worker", 6)

	cl := fake.NewClientBuilder().
		WithScheme(podTestScheme()).
		WithObjects(deploy).
		Build()

	p := actuator.NewPodActuator(cl)
	result, err := p.Apply(context.Background(),
		serviceRef("ns", "worker"),
		engine.ScalingAction{Type: engine.ActionScaleDown, TargetReplica: 3},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}

	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "worker"}, &updated)
	if *updated.Spec.Replicas != 3 {
		t.Errorf("Deployment replicas = %d, want 3", *updated.Spec.Replicas)
	}

	// Previous state annotation must be set.
	prev := updated.Annotations[actuator.AnnotationPreviousState]
	if prev == "" {
		t.Error("previous-state annotation not set")
	}
	var snap map[string]interface{}
	json.Unmarshal([]byte(prev), &snap)
	if snap["replicas"].(float64) != 6 {
		t.Errorf("previous-state replicas = %v, want 6", snap["replicas"])
	}
}

func TestPodActuator_Deployment_DryRun(t *testing.T) {
	deploy := makeDeployment("ns", "svc", 5)

	cl := fake.NewClientBuilder().
		WithScheme(podTestScheme()).
		WithObjects(deploy).
		Build()

	p := actuator.NewPodActuator(cl)
	result, err := p.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 10},
		actuator.ActuationOptions{DryRun: true},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Applied {
		t.Error("dry-run should not apply")
	}

	// Verify no mutation.
	var unchanged appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"}, &unchanged)
	if *unchanged.Spec.Replicas != 5 {
		t.Errorf("replicas changed in dry-run: %d", *unchanged.Spec.Replicas)
	}
}

func TestPodActuator_MinReplicasEnforced(t *testing.T) {
	deploy := makeDeployment("ns", "api", 3)

	cl := fake.NewClientBuilder().
		WithScheme(podTestScheme()).
		WithObjects(deploy).
		Build()

	p := actuator.NewPodActuator(cl)
	p.MinReplicas = 2

	// Solver says 0 replicas — should be clamped to MinReplicas=2.
	_, err := p.Apply(context.Background(),
		serviceRef("ns", "api"),
		engine.ScalingAction{Type: engine.ActionScaleDown, TargetReplica: 0},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api"}, &updated)
	if *updated.Spec.Replicas < 2 {
		t.Errorf("replicas = %d, should be clamped to MinReplicas=2", *updated.Spec.Replicas)
	}
}

// ── Rollback ─────────────────────────────────────────────────────────────────

func TestPodActuator_Rollback_RestoresPreviousReplicas(t *testing.T) {
	prevState := `{"replicas":5}`
	replicas := int32(10)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api",
			Namespace: "ns",
			Annotations: map[string]string{
				actuator.AnnotationPreviousState: prevState,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(podTestScheme()).
		WithObjects(deploy).
		Build()

	p := actuator.NewPodActuator(cl)
	if err := p.Rollback(context.Background(), serviceRef("ns", "api")); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api"}, &updated)
	if *updated.Spec.Replicas != 5 {
		t.Errorf("after rollback replicas = %d, want 5", *updated.Spec.Replicas)
	}
}

func TestPodActuator_Rollback_NoPreviousState_IsNoOp(t *testing.T) {
	deploy := makeDeployment("ns", "api", 4)

	cl := fake.NewClientBuilder().
		WithScheme(podTestScheme()).
		WithObjects(deploy).
		Build()

	p := actuator.NewPodActuator(cl)
	if err := p.Rollback(context.Background(), serviceRef("ns", "api")); err != nil {
		t.Fatalf("Rollback with no annotation: %v", err)
	}
	// Should not have changed replicas.
	var unchanged appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api"}, &unchanged)
	if *unchanged.Spec.Replicas != 4 {
		t.Errorf("replicas changed unexpectedly: %d", *unchanged.Spec.Replicas)
	}
}

func TestPodActuator_Rollback_NotFound_IsNoOp(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(podTestScheme()).Build()
	p := actuator.NewPodActuator(cl)
	// Should not error when deployment doesn't exist.
	if err := p.Rollback(context.Background(), serviceRef("ns", "ghost")); err != nil {
		t.Fatalf("Rollback for missing deployment: %v", err)
	}
}

// ── applyMaxChange helper ─────────────────────────────────────────────────────

func TestApplyMaxChange_NoLimit(t *testing.T) {
	cases := []struct {
		current, target int32
		maxChange       float64
		want            int32
	}{
		{10, 20, 0, 20},   // no limit
		{10, 20, 1.0, 20}, // 100% allowed → no clamp
		{10, 20, 0.5, 15}, // 50% → max 5 increase → 15
		{10, 5, 0.3, 7},   // 30% → max 3 decrease → 7
		{10, 1, 0.2, 8},   // 20% → max 2 decrease → 8 (1 < 8)
		{10, 13, 0.3, 13}, // within limit → no clamp
	}
	// Test via the actuator which calls applyMaxChange internally.
	for _, c := range cases {
		deploy := makeDeployment("ns", "svc", c.current)
		cl := fake.NewClientBuilder().
			WithScheme(podTestScheme()).
			WithObjects(deploy).
			Build()

		p := actuator.NewPodActuator(cl)
		_, err := p.Apply(context.Background(),
			serviceRef("ns", "svc"),
			engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: c.target},
			actuator.ActuationOptions{MaxChange: c.maxChange},
		)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}

		var updated appsv1.Deployment
		cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"}, &updated)
		got := *updated.Spec.Replicas
		if got != c.want {
			t.Errorf("current=%d target=%d maxChange=%.1f → got %d, want %d",
				c.current, c.target, c.maxChange, got, c.want)
		}
	}
}
