package actuator_test

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
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

// ── helpers ───────────────────────────────────────────────────────────────────

func buildAppClient(objects ...client.Object) client.Client {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
}

func testConfigMap(ns, name string, data, annotations map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   ns,
			Name:        name,
			Annotations: annotations,
		},
		Data: data,
	}
}

func testDeployment(ns, name string, annotations map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   ns,
			Name:        name,
			Annotations: annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	}
}

// ── CanApply ──────────────────────────────────────────────────────────────────

func TestAppTuner_CanApply(t *testing.T) {
	at := actuator.NewAppTuner(nil)

	if !at.CanApply(engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"WORKERS": "8"}}) {
		t.Error("CanApply=false for tune+params; want true")
	}
	if at.CanApply(engine.ScalingAction{Type: engine.ActionTune}) {
		t.Error("CanApply=true for tune without params; want false")
	}
	if at.CanApply(engine.ScalingAction{Type: engine.ActionScaleUp, TuningParams: map[string]string{"k": "v"}}) {
		t.Error("CanApply=true for scale_up; want false")
	}
}

// ── Missing ConfigMap ─────────────────────────────────────────────────────────

func TestAppTuner_NoConfigMap_NoOp(t *testing.T) {
	cl := buildAppClient()
	at := actuator.NewAppTuner(cl)

	result, err := at.Apply(context.Background(),
		serviceRef("ns", "myapp"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"WORKERS": "8"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Applied {
		t.Error("Applied should be false when ConfigMap not found")
	}
}

// ── Basic patch ───────────────────────────────────────────────────────────────

func TestAppTuner_PatchesConfigMapValues(t *testing.T) {
	cm := testConfigMap("ns", "myapp-config",
		map[string]string{"MAX_WORKERS": "4", "QUEUE_DEPTH": "100"},
		nil,
	)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	result, err := at.Apply(context.Background(),
		serviceRef("ns", "myapp"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"MAX_WORKERS": "8"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}
	if len(result.Changes) == 0 {
		t.Error("expected at least 1 ChangeRecord")
	}

	var updated corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "myapp-config"}, &updated)
	if updated.Data["MAX_WORKERS"] != "8" {
		t.Errorf("MAX_WORKERS = %s, want 8", updated.Data["MAX_WORKERS"])
	}
	if updated.Data["QUEUE_DEPTH"] != "100" {
		t.Error("QUEUE_DEPTH should be unchanged")
	}
}

// ── Bounds clamping ───────────────────────────────────────────────────────────

func TestAppTuner_ClampsToMax(t *testing.T) {
	cm := testConfigMap("ns", "myapp-config",
		map[string]string{"MAX_WORKERS": "4"},
		map[string]string{
			"optipilot.ai/min/MAX_WORKERS": "1",
			"optipilot.ai/max/MAX_WORKERS": "16",
		},
	)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	_, err := at.Apply(context.Background(),
		serviceRef("ns", "myapp"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"MAX_WORKERS": "50"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "myapp-config"}, &updated)
	if updated.Data["MAX_WORKERS"] != "16" {
		t.Errorf("expected max clamp→16, got %s", updated.Data["MAX_WORKERS"])
	}
}

func TestAppTuner_ClampsToMin(t *testing.T) {
	cm := testConfigMap("ns", "myapp-config",
		map[string]string{"MAX_WORKERS": "8"},
		map[string]string{
			"optipilot.ai/min/MAX_WORKERS": "2",
			"optipilot.ai/max/MAX_WORKERS": "32",
		},
	)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	_, err := at.Apply(context.Background(),
		serviceRef("ns", "myapp"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"MAX_WORKERS": "1"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "myapp-config"}, &updated)
	if updated.Data["MAX_WORKERS"] != "2" {
		t.Errorf("expected min clamp→2, got %s", updated.Data["MAX_WORKERS"])
	}
}

func TestAppTuner_WithinBounds_NoClamp(t *testing.T) {
	cm := testConfigMap("ns", "myapp-config",
		map[string]string{"MAX_WORKERS": "4"},
		map[string]string{
			"optipilot.ai/min/MAX_WORKERS": "1",
			"optipilot.ai/max/MAX_WORKERS": "32",
		},
	)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	_, err := at.Apply(context.Background(),
		serviceRef("ns", "myapp"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"MAX_WORKERS": "12"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "myapp-config"}, &updated)
	if updated.Data["MAX_WORKERS"] != "12" {
		t.Errorf("expected 12 (no clamp), got %s", updated.Data["MAX_WORKERS"])
	}
}

// ── Dry-run ───────────────────────────────────────────────────────────────────

func TestAppTuner_DryRun_DoesNotPatch(t *testing.T) {
	cm := testConfigMap("ns", "api-config",
		map[string]string{"CONCURRENCY": "10"},
		nil,
	)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	result, err := at.Apply(context.Background(),
		serviceRef("ns", "api"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"CONCURRENCY": "20"}},
		actuator.ActuationOptions{DryRun: true},
	)
	if err != nil {
		t.Fatalf("Apply dry-run: %v", err)
	}
	if result.Applied {
		t.Error("Applied=true in dry-run; want false")
	}
	if len(result.Changes) == 0 {
		t.Error("dry-run should still report changes")
	}

	var unchanged corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api-config"}, &unchanged)
	if unchanged.Data["CONCURRENCY"] != "10" {
		t.Errorf("ConfigMap mutated in dry-run: %s", unchanged.Data["CONCURRENCY"])
	}
}

// ── Rolling restart ───────────────────────────────────────────────────────────

func TestAppTuner_TriggersRollingRestart(t *testing.T) {
	cm := testConfigMap("ns", "worker-config",
		map[string]string{"BATCH_SIZE": "50"},
		nil,
	)
	dep := testDeployment("ns", "worker", map[string]string{
		actuator.AnnotationRestartOnTuning: "true",
	})
	cl := buildAppClient(cm, dep)
	at := actuator.NewAppTuner(cl)

	_, err := at.Apply(context.Background(),
		serviceRef("ns", "worker"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"BATCH_SIZE": "100"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "worker"}, &updated)
	if updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Error("rolling restart annotation not set on Deployment pod template")
	}
}

func TestAppTuner_NoRestartAnnotation_SkipsRestart(t *testing.T) {
	cm := testConfigMap("ns", "worker-config", map[string]string{"BATCH_SIZE": "50"}, nil)
	dep := testDeployment("ns", "worker", nil)
	cl := buildAppClient(cm, dep)
	at := actuator.NewAppTuner(cl)

	_, err := at.Apply(context.Background(),
		serviceRef("ns", "worker"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"BATCH_SIZE": "75"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated appsv1.Deployment
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "worker"}, &updated)
	if updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] != "" {
		t.Error("restart triggered without AnnotationRestartOnTuning")
	}
}

// ── Previous state annotation ─────────────────────────────────────────────────

func TestAppTuner_PreviousStateWrittenToConfigMap(t *testing.T) {
	cm := testConfigMap("ns", "svc-config", map[string]string{"DB_POOL": "10"}, nil)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	result, err := at.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"DB_POOL": "20"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.PreviousState == "" {
		t.Error("ActuationResult.PreviousState should be set")
	}

	var updated corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc-config"}, &updated)
	if updated.Annotations[actuator.AnnotationPreviousState] == "" {
		t.Error("AnnotationPreviousState not written to ConfigMap")
	}
}

// ── Rollback ──────────────────────────────────────────────────────────────────

func TestAppTuner_Rollback_RestoresData(t *testing.T) {
	prevData := map[string]string{"MAX_WORKERS": "4", "QUEUE_DEPTH": "50"}
	prevJSON, _ := json.Marshal(map[string]interface{}{"data": prevData})

	cm := testConfigMap("ns", "svc-config",
		map[string]string{"MAX_WORKERS": "12", "QUEUE_DEPTH": "200"},
		map[string]string{actuator.AnnotationPreviousState: string(prevJSON)},
	)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	if err := at.Rollback(context.Background(), serviceRef("ns", "svc")); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var restored corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc-config"}, &restored)
	if restored.Data["MAX_WORKERS"] != "4" {
		t.Errorf("MAX_WORKERS after rollback = %s, want 4", restored.Data["MAX_WORKERS"])
	}
	if restored.Data["QUEUE_DEPTH"] != "50" {
		t.Errorf("QUEUE_DEPTH after rollback = %s, want 50", restored.Data["QUEUE_DEPTH"])
	}
	if restored.Annotations[actuator.AnnotationPreviousState] != "" {
		t.Error("AnnotationPreviousState not cleared after rollback")
	}
}

func TestAppTuner_Rollback_NoAnnotation_IsNoOp(t *testing.T) {
	cm := testConfigMap("ns", "svc-config", map[string]string{"MAX_WORKERS": "8"}, nil)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	if err := at.Rollback(context.Background(), serviceRef("ns", "svc")); err != nil {
		t.Fatalf("Rollback with no annotation: %v", err)
	}

	var unchanged corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc-config"}, &unchanged)
	if unchanged.Data["MAX_WORKERS"] != "8" {
		t.Errorf("data changed during no-op rollback: %s", unchanged.Data["MAX_WORKERS"])
	}
}

// ── Explicit ConfigMapName ────────────────────────────────────────────────────

func TestAppTuner_ExplicitConfigMapName(t *testing.T) {
	cm := testConfigMap("ns", "custom-tuning", map[string]string{"THREADS": "4"}, nil)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)
	at.ConfigMapName = "custom-tuning"

	_, err := at.Apply(context.Background(),
		serviceRef("ns", "anyservice"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"THREADS": "16"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply with explicit ConfigMapName: %v", err)
	}

	var updated corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "custom-tuning"}, &updated)
	if updated.Data["THREADS"] != "16" {
		t.Errorf("THREADS = %s, want 16", updated.Data["THREADS"])
	}
}

// ── Non-numeric values are passed through ─────────────────────────────────────

func TestAppTuner_NonNumericValue_PassedThrough(t *testing.T) {
	cm := testConfigMap("ns", "myapp-config", map[string]string{"LOG_LEVEL": "info"}, nil)
	cl := buildAppClient(cm)
	at := actuator.NewAppTuner(cl)

	_, err := at.Apply(context.Background(),
		serviceRef("ns", "myapp"),
		engine.ScalingAction{Type: engine.ActionTune, TuningParams: map[string]string{"LOG_LEVEL": "debug"}},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated corev1.ConfigMap
	cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "myapp-config"}, &updated)
	if updated.Data["LOG_LEVEL"] != "debug" {
		t.Errorf("LOG_LEVEL = %s, want debug", updated.Data["LOG_LEVEL"])
	}
}
