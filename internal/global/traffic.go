package global

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ---------------------------------------------------------------------------
// Traffic shifting backends
// ---------------------------------------------------------------------------

// TrafficBackend is the type of routing resource being patched.
type TrafficBackend string

const (
	BackendGatewayAPI  TrafficBackend = "gateway-api"
	BackendIstio       TrafficBackend = "istio"
	BackendExternalDNS TrafficBackend = "external-dns"
)

// TrafficShiftPlan describes a single traffic weight change to apply.
type TrafficShiftPlan struct {
	Backend   TrafficBackend
	Namespace string
	Name      string
	// Weights maps cluster/backend ref name → desired integer weight (0-100).
	Weights map[string]int32
}

// TrafficShiftResult captures the outcome of a shift attempt.
type TrafficShiftResult struct {
	Applied     bool
	RolledBack  bool
	OldWeights  map[string]int32
	NewWeights  map[string]int32
	Err         error
	RollbackErr error
}

// SLOChecker validates SLO compliance at a destination cluster.
type SLOChecker interface {
	// CheckSLO returns the SLO compliance % for the named cluster.
	CheckSLO(ctx context.Context, clusterName string) (float64, error)
}

// ---------------------------------------------------------------------------
// Traffic Shifter — orchestrates safe, gradual traffic shifts
// ---------------------------------------------------------------------------

// TrafficShifter applies traffic weight changes to Gateway API HTTPRoutes,
// Istio VirtualServices, or ExternalDNS DNSEndpoints.
type TrafficShifter struct {
	client    client.Client
	checker   SLOChecker
	mu        sync.Mutex
	maxShift  int32         // max % change per cycle (default 25)
	rollbackW time.Duration // rollback observation window (default 5 min)
	sleepFn   func(time.Duration)
	nowFn     func() time.Time

	// History for rollback.
	lastApplied map[string]*appliedShift
}

type appliedShift struct {
	plan      TrafficShiftPlan
	oldW      map[string]int32
	appliedAt time.Time
}

// ShifterOption configures a TrafficShifter.
type ShifterOption func(*TrafficShifter)

// WithMaxShiftPercent overrides the default 25% max shift.
func WithMaxShiftPercent(pct int32) ShifterOption {
	return func(s *TrafficShifter) { s.maxShift = pct }
}

// WithRollbackWindow overrides the default 5-minute rollback observation window.
func WithRollbackWindow(d time.Duration) ShifterOption {
	return func(s *TrafficShifter) { s.rollbackW = d }
}

// WithShifterSleepFn injects a sleep function for testing.
func WithShifterSleepFn(fn func(time.Duration)) ShifterOption {
	return func(s *TrafficShifter) { s.sleepFn = fn }
}

// WithShifterNowFn injects a clock for testing.
func WithShifterNowFn(fn func() time.Time) ShifterOption {
	return func(s *TrafficShifter) { s.nowFn = fn }
}

// NewTrafficShifter creates a TrafficShifter.
func NewTrafficShifter(c client.Client, checker SLOChecker, opts ...ShifterOption) *TrafficShifter {
	ts := &TrafficShifter{
		client:      c,
		checker:     checker,
		maxShift:    25,
		rollbackW:   5 * time.Minute,
		sleepFn:     time.Sleep,
		nowFn:       time.Now,
		lastApplied: make(map[string]*appliedShift),
	}
	for _, o := range opts {
		o(ts)
	}
	return ts
}

// Apply executes a traffic shift plan with safety guards.
// 1. Clamp weights so no single backend changes more than maxShift% from current.
// 2. Validate SLO at destination clusters before shifting.
// 3. Patch the routing resource.
// 4. Monitor SLO for rollbackWindow; auto-rollback if it degrades.
func (ts *TrafficShifter) Apply(ctx context.Context, plan TrafficShiftPlan) *TrafficShiftResult {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	result := &TrafficShiftResult{}

	// Read current weights.
	oldWeights, err := ts.readCurrentWeights(ctx, plan)
	if err != nil {
		result.Err = fmt.Errorf("read current weights: %w", err)
		return result
	}
	result.OldWeights = oldWeights

	// Clamp the shift.
	clamped := ts.clampWeights(oldWeights, plan.Weights)
	result.NewWeights = clamped

	// Pre-shift SLO validation: check all destinations that will see increased traffic.
	for name, newW := range clamped {
		oldW := oldWeights[name]
		if newW > oldW {
			slo, err := ts.checker.CheckSLO(ctx, name)
			if err != nil {
				result.Err = fmt.Errorf("SLO check for %s: %w", name, err)
				return result
			}
			if slo < 90.0 {
				result.Err = fmt.Errorf("SLO at %s is %.1f%% (below 90%%): refusing to increase traffic", name, slo)
				return result
			}
		}
	}

	// Apply the shift.
	if err := ts.patchWeights(ctx, plan, clamped); err != nil {
		result.Err = fmt.Errorf("patch weights: %w", err)
		return result
	}
	result.Applied = true

	// Record for rollback.
	key := shiftKey(plan)
	ts.lastApplied[key] = &appliedShift{
		plan:      plan,
		oldW:      oldWeights,
		appliedAt: ts.nowFn(),
	}

	return result
}

// MonitorAndRollback watches SLO after a shift and rolls back if needed.
// This should be called in a goroutine after Apply.
func (ts *TrafficShifter) MonitorAndRollback(ctx context.Context, plan TrafficShiftPlan, minSLO float64) *TrafficShiftResult {
	result := &TrafficShiftResult{}

	ts.mu.Lock()
	key := shiftKey(plan)
	applied, ok := ts.lastApplied[key]
	ts.mu.Unlock()

	if !ok {
		result.Err = fmt.Errorf("no applied shift found for %s", key)
		return result
	}

	// Wait rollback window then check SLO.
	ts.sleepFn(ts.rollbackW)

	select {
	case <-ctx.Done():
		return result
	default:
	}

	// Check SLO for all destinations.
	for name := range plan.Weights {
		slo, err := ts.checker.CheckSLO(ctx, name)
		if err != nil {
			// Can't check → roll back to be safe.
			result.RolledBack = true
			result.Err = fmt.Errorf("SLO check after shift for %s: %w", name, err)
			break
		}
		if slo < minSLO {
			result.RolledBack = true
			result.Err = fmt.Errorf("SLO at %s degraded to %.1f%% (below %.1f%%)", name, slo, minSLO)
			break
		}
	}

	if result.RolledBack {
		if err := ts.patchWeights(ctx, applied.plan, applied.oldW); err != nil {
			result.RollbackErr = fmt.Errorf("rollback patch: %w", err)
		} else {
			result.OldWeights = applied.oldW
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Weight clamping
// ---------------------------------------------------------------------------

// clampWeights ensures no backend's weight changes more than maxShift% in one cycle.
// Also ensures weights sum to 100.
func (ts *TrafficShifter) clampWeights(current, desired map[string]int32) map[string]int32 {
	clamped := make(map[string]int32, len(desired))
	for name, newW := range desired {
		oldW := current[name]
		delta := newW - oldW
		if delta > ts.maxShift {
			delta = ts.maxShift
		} else if delta < -ts.maxShift {
			delta = -ts.maxShift
		}
		clamped[name] = oldW + delta
		if clamped[name] < 0 {
			clamped[name] = 0
		}
	}

	// Normalise to sum 100.
	total := int32(0)
	for _, w := range clamped {
		total += w
	}
	if total != 100 && total > 0 {
		// Find the entry with the largest weight and adjust.
		bestName := ""
		bestW := int32(0)
		for name, w := range clamped {
			if w > bestW {
				bestW = w
				bestName = name
			}
		}
		clamped[bestName] += (100 - total)
	}

	return clamped
}

// ---------------------------------------------------------------------------
// Read/write weights for each backend
// ---------------------------------------------------------------------------

// GVR constants for supported routing resources.
var (
	gvrHTTPRoute = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}
	gvrVirtualService = schema.GroupVersionResource{
		Group:    "networking.istio.io",
		Version:  "v1beta1",
		Resource: "virtualservices",
	}
	gvrDNSEndpoint = schema.GroupVersionResource{
		Group:    "externaldns.k8s.io",
		Version:  "v1alpha1",
		Resource: "dnsendpoints",
	}
)

// readCurrentWeights reads the current traffic weights from the routing resource.
func (ts *TrafficShifter) readCurrentWeights(ctx context.Context, plan TrafficShiftPlan) (map[string]int32, error) {
	switch plan.Backend {
	case BackendGatewayAPI:
		return ts.readHTTPRouteWeights(ctx, plan.Namespace, plan.Name)
	case BackendIstio:
		return ts.readVirtualServiceWeights(ctx, plan.Namespace, plan.Name)
	case BackendExternalDNS:
		return ts.readDNSEndpointWeights(ctx, plan.Namespace, plan.Name)
	default:
		return nil, fmt.Errorf("unsupported backend: %s", plan.Backend)
	}
}

// patchWeights writes new traffic weights to the routing resource.
func (ts *TrafficShifter) patchWeights(ctx context.Context, plan TrafficShiftPlan, weights map[string]int32) error {
	switch plan.Backend {
	case BackendGatewayAPI:
		return ts.patchHTTPRoute(ctx, plan.Namespace, plan.Name, weights)
	case BackendIstio:
		return ts.patchVirtualService(ctx, plan.Namespace, plan.Name, weights)
	case BackendExternalDNS:
		return ts.patchDNSEndpoint(ctx, plan.Namespace, plan.Name, weights)
	default:
		return fmt.Errorf("unsupported backend: %s", plan.Backend)
	}
}

func shiftKey(plan TrafficShiftPlan) string {
	return fmt.Sprintf("%s/%s/%s", plan.Backend, plan.Namespace, plan.Name)
}

// ---------------------------------------------------------------------------
// Gateway API HTTPRoute
// ---------------------------------------------------------------------------

func (ts *TrafficShifter) readHTTPRouteWeights(ctx context.Context, ns, name string) (map[string]int32, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: gvrHTTPRoute.Group, Version: gvrHTTPRoute.Version, Kind: "HTTPRoute",
	})
	if err := ts.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, obj); err != nil {
		return nil, err
	}

	rules, found, err := unstructured.NestedSlice(obj.Object, "spec", "rules")
	if err != nil || !found || len(rules) == 0 {
		return nil, fmt.Errorf("no rules found in HTTPRoute %s/%s", ns, name)
	}

	// Read from the first rule's backendRefs.
	rule, ok := rules[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid rule format")
	}
	refs, found, err := unstructured.NestedSlice(rule, "backendRefs")
	if err != nil || !found {
		return nil, fmt.Errorf("no backendRefs in first rule")
	}

	weights := make(map[string]int32, len(refs))
	for _, ref := range refs {
		r, _ := ref.(map[string]interface{})
		refName, _, _ := unstructured.NestedString(r, "name")
		w, _, _ := unstructured.NestedInt64(r, "weight")
		weights[refName] = int32(w)
	}
	return weights, nil
}

func (ts *TrafficShifter) patchHTTPRoute(ctx context.Context, ns, name string, weights map[string]int32) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: gvrHTTPRoute.Group, Version: gvrHTTPRoute.Version, Kind: "HTTPRoute",
	})
	if err := ts.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, obj); err != nil {
		return err
	}

	rules, found, _ := unstructured.NestedSlice(obj.Object, "spec", "rules")
	if !found || len(rules) == 0 {
		return fmt.Errorf("no rules in HTTPRoute")
	}

	rule, _ := rules[0].(map[string]interface{})
	refs, _, _ := unstructured.NestedSlice(rule, "backendRefs")

	for i, ref := range refs {
		r, _ := ref.(map[string]interface{})
		refName, _, _ := unstructured.NestedString(r, "name")
		if w, ok := weights[refName]; ok {
			r["weight"] = int64(w)
			refs[i] = r
		}
	}

	if err := unstructured.SetNestedSlice(rule, refs, "backendRefs"); err != nil {
		return err
	}
	rules[0] = rule
	if err := unstructured.SetNestedSlice(obj.Object, rules, "spec", "rules"); err != nil {
		return err
	}

	return ts.client.Update(ctx, obj)
}

// ---------------------------------------------------------------------------
// Istio VirtualService
// ---------------------------------------------------------------------------

func (ts *TrafficShifter) readVirtualServiceWeights(ctx context.Context, ns, name string) (map[string]int32, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: gvrVirtualService.Group, Version: gvrVirtualService.Version, Kind: "VirtualService",
	})
	if err := ts.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, obj); err != nil {
		return nil, err
	}

	http, found, err := unstructured.NestedSlice(obj.Object, "spec", "http")
	if err != nil || !found || len(http) == 0 {
		return nil, fmt.Errorf("no http routes in VirtualService %s/%s", ns, name)
	}

	route0, ok := http[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid http route format")
	}
	routes, found, _ := unstructured.NestedSlice(route0, "route")
	if !found {
		return nil, fmt.Errorf("no route entries")
	}

	weights := make(map[string]int32, len(routes))
	for _, rt := range routes {
		r, _ := rt.(map[string]interface{})
		host, _, _ := unstructured.NestedString(r, "destination", "host")
		w, _, _ := unstructured.NestedInt64(r, "weight")
		weights[host] = int32(w)
	}
	return weights, nil
}

func (ts *TrafficShifter) patchVirtualService(ctx context.Context, ns, name string, weights map[string]int32) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: gvrVirtualService.Group, Version: gvrVirtualService.Version, Kind: "VirtualService",
	})
	if err := ts.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, obj); err != nil {
		return err
	}

	http, found, _ := unstructured.NestedSlice(obj.Object, "spec", "http")
	if !found || len(http) == 0 {
		return fmt.Errorf("no http routes in VirtualService")
	}

	route0, _ := http[0].(map[string]interface{})
	routes, _, _ := unstructured.NestedSlice(route0, "route")

	for i, rt := range routes {
		r, _ := rt.(map[string]interface{})
		host, _, _ := unstructured.NestedString(r, "destination", "host")
		if w, ok := weights[host]; ok {
			r["weight"] = int64(w)
			routes[i] = r
		}
	}

	if err := unstructured.SetNestedSlice(route0, routes, "route"); err != nil {
		return err
	}
	http[0] = route0
	if err := unstructured.SetNestedSlice(obj.Object, http, "spec", "http"); err != nil {
		return err
	}

	return ts.client.Update(ctx, obj)
}

// ---------------------------------------------------------------------------
// ExternalDNS DNSEndpoint
// ---------------------------------------------------------------------------

func (ts *TrafficShifter) readDNSEndpointWeights(ctx context.Context, ns, name string) (map[string]int32, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: gvrDNSEndpoint.Group, Version: gvrDNSEndpoint.Version, Kind: "DNSEndpoint",
	})
	if err := ts.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, obj); err != nil {
		return nil, err
	}

	endpoints, found, err := unstructured.NestedSlice(obj.Object, "spec", "endpoints")
	if err != nil || !found {
		return nil, fmt.Errorf("no endpoints in DNSEndpoint %s/%s", ns, name)
	}

	weights := make(map[string]int32, len(endpoints))
	for _, ep := range endpoints {
		e, _ := ep.(map[string]interface{})
		// Use first target as key.
		targets, _, _ := unstructured.NestedStringSlice(e, "targets")
		if len(targets) == 0 {
			continue
		}
		prov, found, _ := unstructured.NestedMap(e, "providerSpecific")
		if found {
			if wStr, ok := prov["weight"]; ok {
				// Weight is often stored as a string in providerSpecific.
				w := parseWeight(wStr)
				weights[targets[0]] = w
			}
		}
		// Fallback: check setIdentifier as label/key.
		if _, ok := weights[targets[0]]; !ok {
			weights[targets[0]] = 0
		}
	}
	return weights, nil
}

func (ts *TrafficShifter) patchDNSEndpoint(ctx context.Context, ns, name string, weights map[string]int32) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: gvrDNSEndpoint.Group, Version: gvrDNSEndpoint.Version, Kind: "DNSEndpoint",
	})
	if err := ts.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, obj); err != nil {
		return err
	}

	endpoints, found, _ := unstructured.NestedSlice(obj.Object, "spec", "endpoints")
	if !found {
		return fmt.Errorf("no endpoints in DNSEndpoint")
	}

	for i, ep := range endpoints {
		e, _ := ep.(map[string]interface{})
		targets, _, _ := unstructured.NestedStringSlice(e, "targets")
		if len(targets) == 0 {
			continue
		}
		if w, ok := weights[targets[0]]; ok {
			prov := map[string]interface{}{
				"weight": fmt.Sprintf("%d", w),
			}
			e["providerSpecific"] = prov
			endpoints[i] = e
		}
	}

	if err := unstructured.SetNestedSlice(obj.Object, endpoints, "spec", "endpoints"); err != nil {
		return err
	}
	return ts.client.Update(ctx, obj)
}

// parseWeight extracts an int32 from a string or numeric interface.
func parseWeight(v interface{}) int32 {
	switch val := v.(type) {
	case string:
		var w int32
		fmt.Sscanf(val, "%d", &w)
		return w
	case float64:
		return int32(math.Round(val))
	case int64:
		return int32(val)
	case json.Number:
		n, _ := val.Int64()
		return int32(n)
	default:
		return 0
	}
}
