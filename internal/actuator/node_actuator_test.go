package actuator_test

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/optipilot-ai/optipilot/internal/actuator"
	"github.com/optipilot-ai/optipilot/internal/engine"
)

// karpenterGVK mirrors the unexported constant in node_actuator.go.
var karpenterGVK = schema.GroupVersionKind{Group: "karpenter.sh", Version: "v1", Kind: "NodePool"}

// nodeTestScheme adds core types plus registers the Karpenter NodePool GVK.
func nodeTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	// Register Karpenter NodePool as unstructured so fake client can track it.
	s.AddKnownTypeWithName(karpenterGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "karpenter.sh", Version: "v1", Kind: "NodePoolList"},
		&unstructured.UnstructuredList{},
	)
	return s
}

func makeNodePool(name string, requirements []interface{}) *unstructured.Unstructured {
	np := &unstructured.Unstructured{}
	np.SetGroupVersionKind(karpenterGVK)
	np.SetName(name)
	np.SetNamespace("") // NodePool is cluster-scoped
	_ = unstructured.SetNestedSlice(np.Object, requirements, "spec", "requirements")
	return np
}

func initialRequirements() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"key":      "karpenter.sh/capacity-type",
			"operator": "In",
			"values":   []interface{}{"on-demand"},
		},
		map[string]interface{}{
			"key":      "topology.kubernetes.io/zone",
			"operator": "In",
			"values":   []interface{}{"us-east-1a", "us-east-1b"},
		},
	}
}

// ── CanApply ─────────────────────────────────────────────────────────────────

func TestNodeActuator_CanApply(t *testing.T) {
	na := actuator.NewNodeActuator(nil)
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
		got := na.CanApply(engine.ScalingAction{Type: c.typ})
		if got != c.want {
			t.Errorf("CanApply(%s) = %v, want %v", c.typ, got, c.want)
		}
	}
}

// ── NodePool patch ───────────────────────────────────────────────────────────

func TestNodeActuator_PatchesNodePool_SpotRatio(t *testing.T) {
	np := makeNodePool("default-pool", initialRequirements())

	cl := fake.NewClientBuilder().
		WithScheme(nodeTestScheme()).
		WithObjects(np).
		Build()

	na := actuator.NewNodeActuator(cl)
	result, err := na.Apply(context.Background(),
		serviceRef("ns", "api"),
		engine.ScalingAction{Type: engine.ActionTune, TargetReplica: 4, SpotRatio: 0.5},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}
	if result.PreviousState == "" {
		t.Error("expected PreviousState set")
	}

	// Fetch updated NodePool.
	var updated unstructured.Unstructured
	updated.SetGroupVersionKind(karpenterGVK)
	cl.Get(context.Background(), types.NamespacedName{Name: "default-pool"}, &updated)

	reqs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "requirements")
	// Find capacity-type requirement.
	found := false
	for _, r := range reqs {
		req := r.(map[string]interface{})
		if req["key"] == "karpenter.sh/capacity-type" {
			vals, _ := req["values"].([]interface{})
			// With 50% spot, should allow both spot and on-demand.
			if len(vals) < 2 {
				t.Errorf("expected 2 capacity values (spot+on-demand), got %v", vals)
			}
			found = true
		}
	}
	if !found {
		t.Error("capacity-type requirement not found in updated NodePool")
	}
}

func TestNodeActuator_PreservesNonCapacityRequirements(t *testing.T) {
	np := makeNodePool("pool-a", initialRequirements())

	cl := fake.NewClientBuilder().
		WithScheme(nodeTestScheme()).
		WithObjects(np).
		Build()

	na := actuator.NewNodeActuator(cl)
	_, err := na.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionTune, SpotRatio: 0.3},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated unstructured.Unstructured
	updated.SetGroupVersionKind(karpenterGVK)
	cl.Get(context.Background(), types.NamespacedName{Name: "pool-a"}, &updated)

	reqs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "requirements")
	// The zone requirement should still be present.
	found := false
	for _, r := range reqs {
		req := r.(map[string]interface{})
		if req["key"] == "topology.kubernetes.io/zone" {
			found = true
		}
	}
	if !found {
		t.Error("zone requirement was removed — must be preserved")
	}
}

func TestNodeActuator_OnDemandOnly_ZeroSpot(t *testing.T) {
	np := makeNodePool("pool-b", initialRequirements())

	cl := fake.NewClientBuilder().
		WithScheme(nodeTestScheme()).
		WithObjects(np).
		Build()

	na := actuator.NewNodeActuator(cl)
	_, err := na.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionTune, SpotRatio: 0.0},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated unstructured.Unstructured
	updated.SetGroupVersionKind(karpenterGVK)
	cl.Get(context.Background(), types.NamespacedName{Name: "pool-b"}, &updated)

	reqs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "requirements")
	for _, r := range reqs {
		req := r.(map[string]interface{})
		if req["key"] == "karpenter.sh/capacity-type" {
			vals, _ := req["values"].([]interface{})
			if len(vals) != 1 || vals[0] != "on-demand" {
				t.Errorf("with spot=0 expected [on-demand] only, got %v", vals)
			}
		}
	}
}

func TestNodeActuator_FullSpot_StillKeepsOnDemandFallback(t *testing.T) {
	// Safety: even at 100% spot, on-demand fallback must remain.
	np := makeNodePool("pool-c", initialRequirements())

	cl := fake.NewClientBuilder().
		WithScheme(nodeTestScheme()).
		WithObjects(np).
		Build()

	na := actuator.NewNodeActuator(cl)
	_, err := na.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionTune, SpotRatio: 1.0},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated unstructured.Unstructured
	updated.SetGroupVersionKind(karpenterGVK)
	cl.Get(context.Background(), types.NamespacedName{Name: "pool-c"}, &updated)

	reqs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "requirements")
	for _, r := range reqs {
		req := r.(map[string]interface{})
		if req["key"] == "karpenter.sh/capacity-type" {
			vals, _ := req["values"].([]interface{})
			hasOnDemand := false
			for _, v := range vals {
				if v == "on-demand" {
					hasOnDemand = true
				}
			}
			if !hasOnDemand {
				t.Error("on-demand fallback removed at 100% spot — safety violation")
			}
		}
	}
}

func TestNodeActuator_DryRun_DoesNotPatch(t *testing.T) {
	np := makeNodePool("pool-dry", initialRequirements())

	cl := fake.NewClientBuilder().
		WithScheme(nodeTestScheme()).
		WithObjects(np).
		Build()

	na := actuator.NewNodeActuator(cl)
	result, err := na.Apply(context.Background(),
		serviceRef("ns", "svc"),
		engine.ScalingAction{Type: engine.ActionTune, SpotRatio: 0.8},
		actuator.ActuationOptions{DryRun: true},
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Applied {
		t.Error("expected Applied=false in dry-run")
	}
	if len(result.Changes) == 0 {
		t.Error("dry-run should still report changes")
	}

	// NodePool must not be mutated.
	var unchanged unstructured.Unstructured
	unchanged.SetGroupVersionKind(karpenterGVK)
	cl.Get(context.Background(), types.NamespacedName{Name: "pool-dry"}, &unchanged)
	reqs, _, _ := unstructured.NestedSlice(unchanged.Object, "spec", "requirements")
	for _, r := range reqs {
		req := r.(map[string]interface{})
		if req["key"] == "karpenter.sh/capacity-type" {
			vals, _ := req["values"].([]interface{})
			if len(vals) != 1 || vals[0] != "on-demand" {
				t.Errorf("NodePool mutated in dry-run: %v", vals)
			}
		}
	}
}

// ── Rollback ─────────────────────────────────────────────────────────────────

func TestNodeActuator_Rollback_RestoresPreviousRequirements(t *testing.T) {
	prevReqs := initialRequirements()
	prevJSON, _ := json.Marshal(map[string]interface{}{"requirements": prevReqs})

	// Current state has spot enabled (post-actuation).
	currentReqs := []interface{}{
		map[string]interface{}{
			"key": "karpenter.sh/capacity-type", "operator": "In",
			"values": []interface{}{"spot", "on-demand"},
		},
	}
	np := makeNodePool("pool-rb", currentReqs)
	annotations := map[string]string{actuator.AnnotationPreviousState: string(prevJSON)}
	np.SetAnnotations(annotations)

	cl := fake.NewClientBuilder().
		WithScheme(nodeTestScheme()).
		WithObjects(np).
		Build()

	na := actuator.NewNodeActuator(cl)
	if err := na.Rollback(context.Background(), serviceRef("ns", "svc")); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var updated unstructured.Unstructured
	updated.SetGroupVersionKind(karpenterGVK)
	cl.Get(context.Background(), types.NamespacedName{Name: "pool-rb"}, &updated)
	reqs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "requirements")

	// Should have both requirements (capacity-type + zone) from previous state.
	if len(reqs) < 2 {
		t.Errorf("after rollback got %d requirements, want >= 2 (from prev snapshot)", len(reqs))
	}
}

func TestNodeActuator_Rollback_NoAnnotation_IsNoOp(t *testing.T) {
	np := makeNodePool("pool-noop", initialRequirements())

	cl := fake.NewClientBuilder().
		WithScheme(nodeTestScheme()).
		WithObjects(np).
		Build()

	na := actuator.NewNodeActuator(cl)
	if err := na.Rollback(context.Background(), serviceRef("ns", "svc")); err != nil {
		t.Fatalf("Rollback with no annotation: %v", err)
	}
}

// ── Namespace fallback hint ───────────────────────────────────────────────────

func TestNodeActuator_NoNodePool_FallsBackToNamespaceHint(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "production"},
	}

	cl := fake.NewClientBuilder().
		WithScheme(nodeTestScheme()).
		WithObjects(ns).
		Build()

	na := actuator.NewNodeActuator(cl)
	result, err := na.Apply(context.Background(),
		serviceRef("production", "api"),
		engine.ScalingAction{Type: engine.ActionTune, SpotRatio: 0.4},
		actuator.ActuationOptions{},
	)
	if err != nil {
		t.Fatalf("Apply without NodePool: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true for namespace hint fallback")
	}

	// Verify annotation on namespace.
	var updatedNS corev1.Namespace
	cl.Get(context.Background(), types.NamespacedName{Name: "production"}, &updatedNS)
	hint := updatedNS.Annotations[actuator.AnnotationNodeHint]
	if hint == "" {
		t.Error("node hint annotation not set on namespace")
	}

	var h map[string]interface{}
	json.Unmarshal([]byte(hint), &h)
	if h["spotRatio"].(float64) != 0.4 {
		t.Errorf("hint spotRatio = %v, want 0.4", h["spotRatio"])
	}
}
