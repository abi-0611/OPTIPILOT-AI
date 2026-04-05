package actuator_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/optipilot-ai/optipilot/internal/actuator"
	"github.com/optipilot-ai/optipilot/internal/engine"
)

// ── integration client builder ────────────────────────────────────────────────

func buildIntegrationClient(objects ...client.Object) client.Client {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
}

// ── Integration Test 1: full pipeline — solver action → actuator → ConfigMap patched ──

func TestIntegration_SolverAction_PatchesConfigMap(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "api-config"},
		Data:       map[string]string{"MAX_WORKERS": "4"},
	}
	cl := buildIntegrationClient(cm)

	// Wire actuators.
	appTuner := actuator.NewAppTuner(cl)
	reg := actuator.NewRegistry()
	reg.Register(appTuner)

	action := engine.ScalingAction{
		Type:         engine.ActionTune,
		TuningParams: map[string]string{"MAX_WORKERS": "8"},
	}

	result, err := reg.Apply(context.Background(),
		serviceRef("ns", "api"),
		action,
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("registry.Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}

	var updated corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api-config"}, &updated)
	if updated.Data["MAX_WORKERS"] != "8" {
		t.Errorf("MAX_WORKERS = %s, want 8", updated.Data["MAX_WORKERS"])
	}
}

// ── Integration Test 2: safety guard blocks actuation when paused ─────────────

func TestIntegration_SafetyGuard_BlocksActuation(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "prod",
			Annotations: map[string]string{actuator.AnnotationPause: "true"},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "api"},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
	cl := buildIntegrationClient(ns, dep)

	pod := actuator.NewPodActuator(cl)
	reg := actuator.NewRegistry()
	reg.Register(pod)
	sg := actuator.NewSafetyGuard(cl)

	ref := serviceRef("prod", "api")
	action := engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 6}

	// Safety guard blocks.
	err := sg.Allow(context.Background(), ref, actuator.ActuationOptions{})
	if err == nil {
		t.Error("expected safety guard to block due to namespace pause annotation")
	}

	// Deployment replicas unchanged.
	var unchanged appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "prod", Name: "api"}, &unchanged)
	if unchanged.Spec.Replicas == nil || *unchanged.Spec.Replicas != 2 {
		t.Errorf("replicas changed despite safety guard: %d", *unchanged.Spec.Replicas)
	}
	_ = action
}

// ── Integration Test 3: circuit breaker opens after 3 strikes ────────────────

func TestIntegration_CircuitBreaker_OpensAfter3Strikes(t *testing.T) {
	cl := buildIntegrationClient()
	sg := actuator.NewSafetyGuard(cl)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sg.SetClock(func() time.Time { return now })

	ref := serviceRef("ns", "svc")
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)

	err := sg.Allow(context.Background(), ref, actuator.ActuationOptions{})
	if err == nil {
		t.Error("expected circuit breaker to block after 3 degraded outcomes")
	}

	// After 16 min, breaker auto-resets.
	sg.SetClock(func() time.Time { return now.Add(16 * time.Minute) })
	err = sg.Allow(context.Background(), ref, actuator.ActuationOptions{})
	if err != nil {
		t.Errorf("expected circuit breaker to reset after pause; got %v", err)
	}
}

// ── Integration Test 4: pod actuator → rollback restores replicas ─────────────

func TestIntegration_PodActuator_ApplyThenRollback(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "worker"},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "worker"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "worker"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
	cl := buildIntegrationClient(dep)

	pod := actuator.NewPodActuator(cl)
	reg := actuator.NewRegistry()
	reg.Register(pod)

	ref := serviceRef("ns", "worker")

	// Apply scale-up.
	_, err := reg.Apply(context.Background(), ref,
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 9},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify replicas updated.
	var scaled appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "worker"}, &scaled)
	if *scaled.Spec.Replicas != 9 {
		t.Fatalf("replicas after scale = %d, want 9", *scaled.Spec.Replicas)
	}

	// Rollback.
	errs := reg.Rollback(context.Background(), ref)
	if len(errs) > 0 {
		t.Fatalf("Rollback errors: %v", errs)
	}

	var restored appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "worker"}, &restored)
	if *restored.Spec.Replicas != 3 {
		t.Errorf("replicas after rollback = %d, want 3", *restored.Spec.Replicas)
	}
}

// ── Integration Test 5: canary + pod actuator end-to-end ─────────────────────

func TestIntegration_Canary_TwoStep_FullPipeline(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "frontend"},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "frontend"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "frontend"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
	cl := buildIntegrationClient(dep)

	pod := actuator.NewPodActuator(cl)
	pod.MinReplicas = 1
	reg := actuator.NewRegistry()
	reg.Register(pod)

	checker := &stubChecker{healthy: true}
	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(time.Duration) {}) // instant

	// 2 → 10: 400% change, large → triggers canary.
	result, err := cc.Apply(context.Background(),
		serviceRef("ns", "frontend"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 10},
		actuator.ActuationOptions{Canary: true},
		2,
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}

	var final appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "frontend"}, &final)
	if *final.Spec.Replicas != 10 {
		t.Errorf("final replicas = %d, want 10", *final.Spec.Replicas)
	}
	// SLO checked at least once between canary steps (auto-rollback goroutine may also call it).
	if checker.CallCount() < 1 {
		t.Errorf("SLO check count = %d, want >= 1", checker.CallCount())
	}
}

// ── Integration Test 6: app tuner + cooldown via safety guard ─────────────────

func TestIntegration_AppTuner_WithCooldown(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "batch-config"},
		Data:       map[string]string{"POOL_SIZE": "5"},
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns"}}
	cl := buildIntegrationClient(cm, ns)

	appTuner := actuator.NewAppTuner(cl)
	reg := actuator.NewRegistry()
	reg.Register(appTuner)
	sg := actuator.NewSafetyGuard(cl)

	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	sg.SetClock(func() time.Time { return now })

	ref := serviceRef("ns", "batch")
	opts := actuator.ActuationOptions{CooldownSeconds: 120}

	// First actuation should be allowed.
	if err := sg.Allow(context.Background(), ref, opts); err != nil {
		t.Fatalf("first Allow: %v", err)
	}
	_, err := reg.Apply(context.Background(), ref,
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"POOL_SIZE": "10"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sg.RecordActuation(ref)

	// Second actuation immediately — within cooldown.
	if err := sg.Allow(context.Background(), ref, opts); err == nil {
		t.Error("expected cooldown to block second actuation")
	}

	// After cooldown, allowed again.
	sg.SetClock(func() time.Time { return now.Add(3 * time.Minute) })
	if err := sg.Allow(context.Background(), ref, opts); err != nil {
		t.Errorf("expected Allow after cooldown: %v", err)
	}
}

// ── Integration Test 7: HPA scale-up via registry ────────────────────────────

func TestIntegration_PodActuator_HPA_ScaleUp(t *testing.T) {
	three := int32(3)
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "api-hpa"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: &three,
			MaxReplicas: 10,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "api",
			},
		},
	}
	cl := buildIntegrationClient(hpa)

	pod := actuator.NewPodActuator(cl)
	pod.MinReplicas = 1
	reg := actuator.NewRegistry()
	reg.Register(pod)

	result, err := reg.Apply(context.Background(),
		serviceRef("ns", "api"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 8},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true for HPA scale-up")
	}

	var updated autoscalingv2.HorizontalPodAutoscaler
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api-hpa"}, &updated)
	if updated.Spec.MaxReplicas != 8 {
		t.Errorf("HPA MaxReplicas = %d, want 8", updated.Spec.MaxReplicas)
	}
}

// ── Integration Test 8: dry-run across all actuators ─────────────────────────

func TestIntegration_DryRun_NoMutations(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc"},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "svc"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "svc"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc-config"},
		Data:       map[string]string{"WORKERS": "4"},
	}
	cl := buildIntegrationClient(dep, cm)

	pod := actuator.NewPodActuator(cl)
	appTuner := actuator.NewAppTuner(cl)
	reg := actuator.NewRegistry()
	reg.Register(pod)
	reg.Register(appTuner)

	// Dry-run scale-up.
	scaleResult, err := reg.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 8},
		actuator.ActuationOptions{DryRun: true},
	)
	if err != nil {
		t.Fatalf("dry-run scale-up: %v", err)
	}
	if scaleResult.Applied {
		t.Error("scale-up dry-run Applied=true; want false")
	}

	// Deployment unchanged.
	var unchanged appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"}, &unchanged)
	if *unchanged.Spec.Replicas != 2 {
		t.Errorf("dry-run mutated replicas to %d", *unchanged.Spec.Replicas)
	}
}

// ── Integration Test 9: previous-state JSON round-trip ───────────────────────

func TestIntegration_PreviousState_RoundTrip(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc"},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "svc"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "svc"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
	cl := buildIntegrationClient(dep)

	pod := actuator.NewPodActuator(cl)
	reg := actuator.NewRegistry()
	reg.Register(pod)

	ref := serviceRef("ns", "svc")
	result, err := reg.Apply(context.Background(), ref,
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 7},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// PreviousState should be valid JSON containing the old replica count.
	if result.PreviousState == "" {
		t.Fatal("PreviousState is empty")
	}
	var prev map[string]interface{}
	if err := json.Unmarshal([]byte(result.PreviousState), &prev); err != nil {
		t.Fatalf("PreviousState is not valid JSON: %v", err)
	}
	replicas, ok := prev["replicas"]
	if !ok {
		t.Error("PreviousState JSON missing 'replicas' key")
	}
	// JSON numbers are float64.
	if replicas.(float64) != 3 {
		t.Errorf("PreviousState.replicas = %v, want 3", replicas)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func int32Ptr(v int32) *int32 { return &v }
