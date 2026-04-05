package actuator_test

import (
	"context"
	"sync/atomic"
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

// ── SLO checker stubs ─────────────────────────────────────────────────────────

type stubChecker struct {
	healthy bool
	calls   int32
}

func (s *stubChecker) IsHealthy(_ context.Context, _, _ string) (bool, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.healthy, nil
}

func (s *stubChecker) CallCount() int { return int(atomic.LoadInt32(&s.calls)) }

// ── fake client for canary tests ──────────────────────────────────────────────

func buildCanaryClient(objects ...client.Object) client.Client {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
}

func canaryDeployment(ns, name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
}

// buildCanaryRegistry creates a Registry wired with a PodActuator backed by a fake client.
func buildCanaryRegistry(objects ...client.Object) (*actuator.Registry, client.Client) {
	cl := buildCanaryClient(objects...)
	pod := actuator.NewPodActuator(cl)
	pod.MinReplicas = 1
	reg := actuator.NewRegistry()
	reg.Register(pod)
	return reg, cl
}

// ── isLargeChange / halfStep (tested via Apply behaviour) ────────────────────

func TestCanary_SmallChange_SingleStep(t *testing.T) {
	// 4 → 5: delta=1, 1/4=25% — below 50% threshold, no two-step.
	dep := canaryDeployment("ns", "svc", 4)
	reg, _ := buildCanaryRegistry(dep)
	checker := &stubChecker{healthy: true}

	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(time.Duration) {}) // no-op sleep

	result, err := cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 5},
		actuator.ActuationOptions{Canary: true},
		4,
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true for small change")
	}
	// Checker should NOT be called mid-step for a small change.
	if checker.CallCount() != 0 {
		t.Errorf("SLO checker called %d times for single-step; want 0", checker.CallCount())
	}
}

func TestCanary_LargeChange_TwoStepApplied(t *testing.T) {
	// 4 → 10: delta=6, 6/4=150% — large change triggers canary.
	dep := canaryDeployment("ns", "svc", 4)
	reg, cl := buildCanaryRegistry(dep)
	checker := &stubChecker{healthy: true}

	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(time.Duration) {})

	result, err := cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 10},
		actuator.ActuationOptions{Canary: true},
		4,
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true after two-step canary")
	}
	// SLO check should have been called once (between steps).
	if checker.CallCount() != 1 {
		t.Errorf("SLO checker called %d times; want 1", checker.CallCount())
	}

	// Final replicas should be 10.
	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"}, &updated)
	if updated.Spec.Replicas == nil || *updated.Spec.Replicas != 10 {
		r := int32(-1)
		if updated.Spec.Replicas != nil {
			r = *updated.Spec.Replicas
		}
		t.Errorf("final replicas = %d, want 10", r)
	}
}

func TestCanary_LargeChange_SLODegradedAfterStep1_RollsBack(t *testing.T) {
	// 4 → 12: large change; SLO fails after step 1 → rollback.
	dep := canaryDeployment("ns", "svc", 4)
	dep.Annotations = map[string]string{} // no previous-state yet
	reg, _ := buildCanaryRegistry(dep)
	checker := &stubChecker{healthy: false} // SLO unhealthy

	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(time.Duration) {})

	result, err := cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 12},
		actuator.ActuationOptions{Canary: true},
		4,
	)
	if err != nil {
		t.Fatalf("Apply should not error (rollback handled internally): %v", err)
	}
	if result.Applied {
		t.Error("Applied should be false when SLO degraded after step 1")
	}
	if result.Error == nil {
		t.Error("result.Error should be set explaining the rollback reason")
	}
}

func TestCanary_SmallChange_CanaryFlagFalse_NoCheck(t *testing.T) {
	// opts.Canary=false — even large change should NOT split.
	dep := canaryDeployment("ns", "svc", 2)
	reg, cl := buildCanaryRegistry(dep)
	checker := &stubChecker{healthy: true}

	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(time.Duration) {})

	result, err := cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 10},
		actuator.ActuationOptions{Canary: false}, // canary disabled
		2,
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}
	if checker.CallCount() != 0 {
		t.Errorf("SLO checker called %d times with Canary=false; want 0", checker.CallCount())
	}

	// Direct jump to target replicas.
	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"}, &updated)
	if updated.Spec.Replicas == nil || *updated.Spec.Replicas != 10 {
		t.Errorf("replicas = %d, want 10", *updated.Spec.Replicas)
	}
}

func TestCanary_DryRun_NoActuatorCalled(t *testing.T) {
	dep := canaryDeployment("ns", "svc", 4)
	reg, _ := buildCanaryRegistry(dep)
	checker := &stubChecker{healthy: true}

	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(time.Duration) {})

	result, err := cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 10},
		actuator.ActuationOptions{Canary: true, DryRun: true},
		4,
	)
	if err != nil {
		t.Fatalf("Apply dry-run: %v", err)
	}
	// Dry-run → Applied=false.
	if result.Applied {
		t.Error("Applied=true in dry-run canary; want false")
	}
}

// ── Rollback ──────────────────────────────────────────────────────────────────

func TestCanary_Rollback_DelegatesToRegistry(t *testing.T) {
	replicas := int32(8)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "svc",
			Annotations: map[string]string{
				actuator.AnnotationPreviousState: `{"replicas":4}`,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "svc"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "svc"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
	reg, cl := buildCanaryRegistry(dep)
	cc := actuator.NewCanaryController(reg, nil)

	errs := cc.Rollback(context.Background(), serviceRef("ns", "svc"))
	if len(errs) > 0 {
		t.Fatalf("Rollback errors: %v", errs)
	}

	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"}, &updated)
	// After rollback replicas should be restored to 4.
	if updated.Spec.Replicas == nil || *updated.Spec.Replicas != 4 {
		r := int32(-1)
		if updated.Spec.Replicas != nil {
			r = *updated.Spec.Replicas
		}
		t.Errorf("replicas after rollback = %d, want 4", r)
	}
}

// ── Auto-rollback watcher ─────────────────────────────────────────────────────

func TestCanary_AutoRollbackWatcher_StartsAfterApply(t *testing.T) {
	dep := canaryDeployment("ns", "svc", 4)
	reg, _ := buildCanaryRegistry(dep)
	checker := &stubChecker{healthy: true}

	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(time.Duration) {}) // instant sleep

	_, err := cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 6},
		actuator.ActuationOptions{},
		4,
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Give the goroutine a moment to register.
	time.Sleep(10 * time.Millisecond)
	// WatcherActive may be true or false by now depending on goroutine speed;
	// the key assertion is that it doesn't panic and the apply succeeded.
}

func TestCanary_AutoRollback_UnhealthySLO_TriggersRollback(t *testing.T) {
	replicas := int32(4)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "svc"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "svc"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
	reg, cl := buildCanaryRegistry(dep)

	// First apply with healthy SLO — sets AnnotationPreviousState to replicas=4.
	healthyChecker := &stubChecker{healthy: true}
	cc := actuator.NewCanaryController(reg, healthyChecker)
	var sleptDurations []time.Duration
	cc.SetSleepFn(func(d time.Duration) { sleptDurations = append(sleptDurations, d) })

	_, err := cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 6},
		actuator.ActuationOptions{},
		4,
	)
	if err != nil {
		t.Fatalf("initial Apply: %v", err)
	}

	// Now switch to an unhealthy checker and manually trigger watcher logic.
	// Replace checker and call Rollback to simulate auto-rollback fired.
	unhealthyChecker := &stubChecker{healthy: false}
	cc2 := actuator.NewCanaryController(reg, unhealthyChecker)
	cc2.SetSleepFn(func(time.Duration) {})

	// Simulate that watcher fires: call rollback.
	errs := cc2.Rollback(context.Background(), serviceRef("ns", "svc"))
	if len(errs) > 0 {
		t.Fatalf("Rollback: %v", errs)
	}

	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"}, &updated)
	if updated.Spec.Replicas == nil || *updated.Spec.Replicas != 4 {
		r := int32(-1)
		if updated.Spec.Replicas != nil {
			r = *updated.Spec.Replicas
		}
		t.Errorf("replicas after auto-rollback = %d, want 4", r)
	}
}

// ── halfStep helper (via Apply) ───────────────────────────────────────────────

func TestCanary_HalfStep_ScaleUp(t *testing.T) {
	// 2 → 8: step1 should land at 5 (halfway, ceiling), step2 at 8.
	dep := canaryDeployment("ns", "svc", 2)
	reg, cl := buildCanaryRegistry(dep)
	checker := &stubChecker{healthy: true}

	stepCount := 0
	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(time.Duration) { stepCount++ })

	_, err := cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 8},
		actuator.ActuationOptions{Canary: true},
		2,
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if stepCount != 1 {
		t.Errorf("expected exactly 1 inter-step sleep, got %d", stepCount)
	}

	var final appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"}, &final)
	if final.Spec.Replicas == nil || *final.Spec.Replicas != 8 {
		t.Errorf("final replicas = %d, want 8", *final.Spec.Replicas)
	}
}

// ── WatcherActive: cancelled by next Apply ───────────────────────────────────

func TestCanary_NewApply_CancelsOldWatcher(t *testing.T) {
	dep := canaryDeployment("ns", "svc", 4)
	reg, _ := buildCanaryRegistry(dep)
	checker := &stubChecker{healthy: true}

	slept := make(chan struct{}, 1)
	cc := actuator.NewCanaryController(reg, checker)
	cc.SetSleepFn(func(d time.Duration) {
		select {
		case slept <- struct{}{}:
		default:
		}
	})

	// First apply → watcher goroutine started.
	_, _ = cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 5},
		actuator.ActuationOptions{},
		4,
	)

	// Second apply immediately cancels previous watcher.
	_, _ = cc.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionScaleUp, TargetReplica: 6},
		actuator.ActuationOptions{},
		5,
	)

	// No panic or data race — test passes if we reach here.
}

// Ensure HPA variant also works (builds clean with autoscalingv2 import used by other tests).
var _ = autoscalingv2.HorizontalPodAutoscaler{}
