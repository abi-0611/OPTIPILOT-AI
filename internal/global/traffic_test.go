package global

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fakeSLOChecker implements SLOChecker for testing.
type fakeSLOChecker struct {
	slos map[string]float64
	err  error
}

func (f *fakeSLOChecker) CheckSLO(_ context.Context, cluster string) (float64, error) {
	if f.err != nil {
		return 0, f.err
	}
	if slo, ok := f.slos[cluster]; ok {
		return slo, nil
	}
	return 99.9, nil
}

func newHTTPRoute(ns, name string, refs []map[string]interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute",
	})
	obj.SetNamespace(ns)
	obj.SetName(name)

	backendRefs := make([]interface{}, len(refs))
	for i, r := range refs {
		backendRefs[i] = r
	}

	obj.Object["spec"] = map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"backendRefs": backendRefs,
			},
		},
	}
	return obj
}

func newVirtualService(ns, name string, routes []map[string]interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "networking.istio.io", Version: "v1beta1", Kind: "VirtualService",
	})
	obj.SetNamespace(ns)
	obj.SetName(name)

	routeSlice := make([]interface{}, len(routes))
	for i, r := range routes {
		routeSlice[i] = r
	}

	obj.Object["spec"] = map[string]interface{}{
		"http": []interface{}{
			map[string]interface{}{
				"route": routeSlice,
			},
		},
	}
	return obj
}

func newDNSEndpoint(ns, name string, endpoints []map[string]interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "externaldns.k8s.io", Version: "v1alpha1", Kind: "DNSEndpoint",
	})
	obj.SetNamespace(ns)
	obj.SetName(name)

	epSlice := make([]interface{}, len(endpoints))
	for i, e := range endpoints {
		epSlice[i] = e
	}
	obj.Object["spec"] = map[string]interface{}{
		"endpoints": epSlice,
	}
	return obj
}

func buildFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().WithObjects(objs...).Build()
}

// ---------------------------------------------------------------------------
// Tests: Weight Clamping
// ---------------------------------------------------------------------------

func TestClampWeights_NoChangeNeeded(t *testing.T) {
	ts := NewTrafficShifter(nil, nil)
	current := map[string]int32{"a": 50, "b": 50}
	desired := map[string]int32{"a": 50, "b": 50}

	got := ts.clampWeights(current, desired)

	if got["a"] != 50 || got["b"] != 50 {
		t.Errorf("expected no change, got %v", got)
	}
}

func TestClampWeights_ExceedsMaxShift(t *testing.T) {
	ts := NewTrafficShifter(nil, nil, WithMaxShiftPercent(25))
	current := map[string]int32{"a": 50, "b": 50}
	desired := map[string]int32{"a": 90, "b": 10}

	got := ts.clampWeights(current, desired)

	// Delta for a: +40 → clamped to +25 = 75
	// Delta for b: -40 → clamped to -25 = 25
	// Sum = 100, good.
	if got["a"] != 75 || got["b"] != 25 {
		t.Errorf("expected {a:75, b:25}, got %v", got)
	}
}

func TestClampWeights_SmallShift(t *testing.T) {
	ts := NewTrafficShifter(nil, nil, WithMaxShiftPercent(25))
	current := map[string]int32{"a": 50, "b": 50}
	desired := map[string]int32{"a": 60, "b": 40}

	got := ts.clampWeights(current, desired)

	if got["a"] != 60 || got["b"] != 40 {
		t.Errorf("expected {a:60, b:40}, got %v", got)
	}
}

func TestClampWeights_NormalisesToHundred(t *testing.T) {
	ts := NewTrafficShifter(nil, nil, WithMaxShiftPercent(10))
	current := map[string]int32{"a": 34, "b": 33, "c": 33}
	desired := map[string]int32{"a": 50, "b": 50, "c": 0}

	got := ts.clampWeights(current, desired)

	total := int32(0)
	for _, w := range got {
		total += w
	}
	if total != 100 {
		t.Errorf("expected sum 100, got %d (weights: %v)", total, got)
	}
}

func TestClampWeights_NegativeClampedToZero(t *testing.T) {
	ts := NewTrafficShifter(nil, nil, WithMaxShiftPercent(50))
	current := map[string]int32{"a": 10, "b": 90}
	desired := map[string]int32{"a": 0, "b": 100}

	got := ts.clampWeights(current, desired)

	if got["a"] < 0 {
		t.Errorf("weight should not be negative: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Tests: Gateway API HTTPRoute shifting
// ---------------------------------------------------------------------------

func TestApply_GatewayAPI_PatchesWeights(t *testing.T) {
	route := newHTTPRoute("default", "my-route", []map[string]interface{}{
		{"name": "cluster-a", "weight": int64(60)},
		{"name": "cluster-b", "weight": int64(40)},
	})

	cl := buildFakeClient(route)
	checker := &fakeSLOChecker{slos: map[string]float64{
		"cluster-a": 99.5,
		"cluster-b": 99.5,
	}}
	ts := NewTrafficShifter(cl, checker, WithMaxShiftPercent(25))

	plan := TrafficShiftPlan{
		Backend:   BackendGatewayAPI,
		Namespace: "default",
		Name:      "my-route",
		Weights:   map[string]int32{"cluster-a": 70, "cluster-b": 30},
	}

	result := ts.Apply(context.Background(), plan)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}

	// Verify: old weights read correctly.
	if result.OldWeights["cluster-a"] != 60 || result.OldWeights["cluster-b"] != 40 {
		t.Errorf("old weights wrong: %v", result.OldWeights)
	}
	// Shift is ≤25 so no clamping.
	if result.NewWeights["cluster-a"] != 70 || result.NewWeights["cluster-b"] != 30 {
		t.Errorf("new weights wrong: %v", result.NewWeights)
	}

	// Verify the object was actually patched.
	updated := &unstructured.Unstructured{}
	updated.SetGroupVersionKind(route.GroupVersionKind())
	if err := cl.Get(context.Background(), fakeKey("default", "my-route"), updated); err != nil {
		t.Fatalf("get updated route: %v", err)
	}
	rules, _, _ := unstructured.NestedSlice(updated.Object, "spec", "rules")
	rule0 := rules[0].(map[string]interface{})
	refs, _, _ := unstructured.NestedSlice(rule0, "backendRefs")
	for _, ref := range refs {
		r := ref.(map[string]interface{})
		name, _, _ := unstructured.NestedString(r, "name")
		w, _, _ := unstructured.NestedInt64(r, "weight")
		if name == "cluster-a" && w != 70 {
			t.Errorf("cluster-a weight not updated, got %d", w)
		}
		if name == "cluster-b" && w != 30 {
			t.Errorf("cluster-b weight not updated, got %d", w)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: Istio VirtualService shifting
// ---------------------------------------------------------------------------

func TestApply_Istio_PatchesWeights(t *testing.T) {
	vs := newVirtualService("default", "my-vs", []map[string]interface{}{
		{
			"destination": map[string]interface{}{"host": "cluster-a.svc"},
			"weight":      int64(50),
		},
		{
			"destination": map[string]interface{}{"host": "cluster-b.svc"},
			"weight":      int64(50),
		},
	})

	cl := buildFakeClient(vs)
	checker := &fakeSLOChecker{slos: map[string]float64{
		"cluster-a.svc": 99.0,
		"cluster-b.svc": 99.0,
	}}
	ts := NewTrafficShifter(cl, checker, WithMaxShiftPercent(25))

	plan := TrafficShiftPlan{
		Backend:   BackendIstio,
		Namespace: "default",
		Name:      "my-vs",
		Weights:   map[string]int32{"cluster-a.svc": 60, "cluster-b.svc": 40},
	}

	result := ts.Apply(context.Background(), plan)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}
	if result.NewWeights["cluster-a.svc"] != 60 || result.NewWeights["cluster-b.svc"] != 40 {
		t.Errorf("new weights wrong: %v", result.NewWeights)
	}
}

// ---------------------------------------------------------------------------
// Tests: ExternalDNS DNSEndpoint shifting
// ---------------------------------------------------------------------------

func TestApply_ExternalDNS_PatchesWeights(t *testing.T) {
	dns := newDNSEndpoint("default", "my-dns", []map[string]interface{}{
		{
			"targets":          []interface{}{"10.0.1.1"},
			"providerSpecific": map[string]interface{}{"weight": "50"},
		},
		{
			"targets":          []interface{}{"10.0.2.1"},
			"providerSpecific": map[string]interface{}{"weight": "50"},
		},
	})

	cl := buildFakeClient(dns)
	checker := &fakeSLOChecker{slos: map[string]float64{
		"10.0.1.1": 99.0,
		"10.0.2.1": 99.0,
	}}
	ts := NewTrafficShifter(cl, checker, WithMaxShiftPercent(25))

	plan := TrafficShiftPlan{
		Backend:   BackendExternalDNS,
		Namespace: "default",
		Name:      "my-dns",
		Weights:   map[string]int32{"10.0.1.1": 60, "10.0.2.1": 40},
	}

	result := ts.Apply(context.Background(), plan)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}
}

// ---------------------------------------------------------------------------
// Tests: SLO pre-check gating
// ---------------------------------------------------------------------------

func TestApply_SLOBelowThreshold_RefusesShift(t *testing.T) {
	route := newHTTPRoute("default", "my-route", []map[string]interface{}{
		{"name": "cluster-a", "weight": int64(50)},
		{"name": "cluster-b", "weight": int64(50)},
	})

	cl := buildFakeClient(route)
	checker := &fakeSLOChecker{slos: map[string]float64{
		"cluster-a": 85.0, // Below 90%
		"cluster-b": 99.0,
	}}
	ts := NewTrafficShifter(cl, checker, WithMaxShiftPercent(25))

	plan := TrafficShiftPlan{
		Backend:   BackendGatewayAPI,
		Namespace: "default",
		Name:      "my-route",
		Weights:   map[string]int32{"cluster-a": 70, "cluster-b": 30},
	}

	result := ts.Apply(context.Background(), plan)
	if result.Applied {
		t.Fatal("expected shift to be refused due to low SLO")
	}
	if result.Err == nil {
		t.Fatal("expected error about SLO")
	}
}

func TestApply_SLOCheckError_RefusesShift(t *testing.T) {
	route := newHTTPRoute("default", "my-route", []map[string]interface{}{
		{"name": "cluster-a", "weight": int64(50)},
		{"name": "cluster-b", "weight": int64(50)},
	})

	cl := buildFakeClient(route)
	checker := &fakeSLOChecker{err: fmt.Errorf("connection refused")}
	ts := NewTrafficShifter(cl, checker, WithMaxShiftPercent(25))

	plan := TrafficShiftPlan{
		Backend:   BackendGatewayAPI,
		Namespace: "default",
		Name:      "my-route",
		Weights:   map[string]int32{"cluster-a": 70, "cluster-b": 30},
	}

	result := ts.Apply(context.Background(), plan)
	if result.Applied {
		t.Fatal("expected shift to be refused due to SLO error")
	}
}

// ---------------------------------------------------------------------------
// Tests: SLO validation only on destinations gaining traffic
// ---------------------------------------------------------------------------

func TestApply_SLOCheckOnlyForIncreasedTraffic(t *testing.T) {
	route := newHTTPRoute("default", "my-route", []map[string]interface{}{
		{"name": "cluster-a", "weight": int64(70)},
		{"name": "cluster-b", "weight": int64(30)},
	})

	cl := buildFakeClient(route)
	// cluster-a has low SLO but traffic is DECREASING to it.
	checker := &fakeSLOChecker{slos: map[string]float64{
		"cluster-a": 80.0, // Low, but traffic is decreasing
		"cluster-b": 99.0, // Fine, traffic is increasing
	}}
	ts := NewTrafficShifter(cl, checker, WithMaxShiftPercent(25))

	plan := TrafficShiftPlan{
		Backend:   BackendGatewayAPI,
		Namespace: "default",
		Name:      "my-route",
		Weights:   map[string]int32{"cluster-a": 50, "cluster-b": 50},
	}

	result := ts.Apply(context.Background(), plan)
	if result.Err != nil {
		t.Fatalf("should allow shift since cluster-a traffic is decreasing: %v", result.Err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}
}

// ---------------------------------------------------------------------------
// Tests: Rollback on SLO degradation
// ---------------------------------------------------------------------------

func TestMonitorAndRollback_SLODegrades_RollsBack(t *testing.T) {
	route := newHTTPRoute("default", "my-route", []map[string]interface{}{
		{"name": "cluster-a", "weight": int64(50)},
		{"name": "cluster-b", "weight": int64(50)},
	})

	cl := buildFakeClient(route)

	var clusterBCalls int32
	checker := &dynamicSLOChecker{
		checkFn: func(_ context.Context, cluster string) (float64, error) {
			if cluster == "cluster-b" {
				n := atomic.AddInt32(&clusterBCalls, 1)
				// First call is pre-shift validation (Apply skips it since traffic decreases).
				// MonitorAndRollback calls CheckSLO → this is call #1 for cluster-b.
				if n >= 1 {
					return 85.0, nil // Degraded!
				}
			}
			return 99.0, nil
		},
	}

	ts := NewTrafficShifter(cl, checker,
		WithMaxShiftPercent(25),
		WithRollbackWindow(10*time.Millisecond),
		WithShifterSleepFn(func(d time.Duration) {}), // No-op sleep
	)

	plan := TrafficShiftPlan{
		Backend:   BackendGatewayAPI,
		Namespace: "default",
		Name:      "my-route",
		Weights:   map[string]int32{"cluster-a": 60, "cluster-b": 40},
	}

	// Apply shift first.
	applyResult := ts.Apply(context.Background(), plan)
	if applyResult.Err != nil {
		t.Fatalf("apply failed: %v", applyResult.Err)
	}

	// Monitor — should detect degradation and roll back.
	monResult := ts.MonitorAndRollback(context.Background(), plan, 90.0)
	if !monResult.RolledBack {
		t.Fatal("expected rollback due to SLO degradation")
	}
	if monResult.RollbackErr != nil {
		t.Fatalf("rollback error: %v", monResult.RollbackErr)
	}
}

// dynamicSLOChecker allows per-call SLO behaviour.
type dynamicSLOChecker struct {
	checkFn func(ctx context.Context, cluster string) (float64, error)
}

func (d *dynamicSLOChecker) CheckSLO(ctx context.Context, cluster string) (float64, error) {
	return d.checkFn(ctx, cluster)
}

func TestMonitorAndRollback_SLOStaysHealthy_NoRollback(t *testing.T) {
	route := newHTTPRoute("default", "my-route", []map[string]interface{}{
		{"name": "cluster-a", "weight": int64(50)},
		{"name": "cluster-b", "weight": int64(50)},
	})

	cl := buildFakeClient(route)
	checker := &fakeSLOChecker{slos: map[string]float64{
		"cluster-a": 99.0,
		"cluster-b": 99.0,
	}}
	ts := NewTrafficShifter(cl, checker,
		WithMaxShiftPercent(25),
		WithRollbackWindow(10*time.Millisecond),
		WithShifterSleepFn(func(d time.Duration) {}),
	)

	plan := TrafficShiftPlan{
		Backend:   BackendGatewayAPI,
		Namespace: "default",
		Name:      "my-route",
		Weights:   map[string]int32{"cluster-a": 60, "cluster-b": 40},
	}

	applyResult := ts.Apply(context.Background(), plan)
	if applyResult.Err != nil {
		t.Fatalf("apply failed: %v", applyResult.Err)
	}

	monResult := ts.MonitorAndRollback(context.Background(), plan, 90.0)
	if monResult.RolledBack {
		t.Fatal("should not roll back when SLO is healthy")
	}
}

// ---------------------------------------------------------------------------
// Tests: Edge cases
// ---------------------------------------------------------------------------

func TestApply_UnsupportedBackend_ReturnsError(t *testing.T) {
	ts := NewTrafficShifter(nil, nil)
	plan := TrafficShiftPlan{
		Backend:   TrafficBackend("custom-mesh"),
		Namespace: "default",
		Name:      "test",
		Weights:   map[string]int32{"a": 100},
	}

	result := ts.Apply(context.Background(), plan)
	if result.Err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}

func TestShiftKey(t *testing.T) {
	plan := TrafficShiftPlan{
		Backend:   BackendIstio,
		Namespace: "ns1",
		Name:      "vs1",
	}
	key := shiftKey(plan)
	expected := "istio/ns1/vs1"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestParseWeight_StringValue(t *testing.T) {
	if w := parseWeight("42"); w != 42 {
		t.Errorf("expected 42, got %d", w)
	}
}

func TestParseWeight_Float64Value(t *testing.T) {
	if w := parseWeight(float64(33.7)); w != 34 {
		t.Errorf("expected 34, got %d", w)
	}
}

func TestParseWeight_Int64Value(t *testing.T) {
	if w := parseWeight(int64(55)); w != 55 {
		t.Errorf("expected 55, got %d", w)
	}
}

func TestParseWeight_Unknown(t *testing.T) {
	if w := parseWeight(true); w != 0 {
		t.Errorf("expected 0 for unknown type, got %d", w)
	}
}

func TestMaxShiftOption(t *testing.T) {
	ts := NewTrafficShifter(nil, nil, WithMaxShiftPercent(10))
	if ts.maxShift != 10 {
		t.Errorf("expected maxShift=10, got %d", ts.maxShift)
	}
}

func TestRollbackWindowOption(t *testing.T) {
	ts := NewTrafficShifter(nil, nil, WithRollbackWindow(3*time.Minute))
	if ts.rollbackW != 3*time.Minute {
		t.Errorf("expected 3m, got %v", ts.rollbackW)
	}
}

func TestClampWeights_ThreeCluster_MaxShift(t *testing.T) {
	ts := NewTrafficShifter(nil, nil, WithMaxShiftPercent(10))
	current := map[string]int32{"a": 40, "b": 30, "c": 30}
	desired := map[string]int32{"a": 60, "b": 20, "c": 20}

	got := ts.clampWeights(current, desired)

	// a: +20→clamped to +10 = 50
	// b: -10→ OK = 20
	// c: -10→ OK = 20
	// total = 90 → normalise: largest (a=50) gets +10 = 60... wait.
	// Let me think: total=90, largest=50 (a), so a+=10=60.
	total := int32(0)
	for _, w := range got {
		total += w
	}
	if total != 100 {
		t.Errorf("expected sum 100, got %d (weights: %v)", total, got)
	}
}

func fakeKey(ns, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}
