package metrics

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

type stubPrometheus struct {
	values map[string]float64
	err    error
	calls  atomic.Int64
}

func (s *stubPrometheus) Query(_ context.Context, query string) (float64, error) {
	s.calls.Add(1)
	if s.err != nil {
		return 0, s.err
	}
	if v, ok := s.values[query]; ok {
		return v, nil
	}
	return 0, ErrNoData
}

func (s *stubPrometheus) QueryRange(_ context.Context, _ string, _, _ time.Time, _ time.Duration) ([]DataPoint, error) {
	return nil, nil
}

func (s *stubPrometheus) Healthy(_ context.Context) error { return nil }

func cm(name, query string, target, weight float64) slov1alpha1.CustomMetric {
	return slov1alpha1.CustomMetric{Name: name, Query: query, Target: target, Weight: weight}
}

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

func TestFetch_Empty(t *testing.T) {
	a := NewCustomMetricAdapter(&stubPrometheus{})
	m, r := a.Fetch(context.Background(), nil)
	if m != nil || r != nil {
		t.Errorf("expected nils, got %v / %v", m, r)
	}
}

func TestFetch_SingleSuccess(t *testing.T) {
	prom := &stubPrometheus{values: map[string]float64{"up": 1}}
	a := NewCustomMetricAdapter(prom)
	m, r := a.Fetch(context.Background(), []slov1alpha1.CustomMetric{
		cm("healthy", "up", 1, 0),
	})
	if len(m) != 1 || m["healthy"] != 1 {
		t.Errorf("got %v", m)
	}
	if r[0].Err != nil {
		t.Errorf("err=%v", r[0].Err)
	}
}

func TestFetch_MultipleParallel(t *testing.T) {
	prom := &stubPrometheus{values: map[string]float64{
		"q1": 10, "q2": 20, "q3": 30,
	}}
	a := NewCustomMetricAdapter(prom)
	metrics := []slov1alpha1.CustomMetric{
		cm("a", "q1", 10, 1), cm("b", "q2", 20, 1), cm("c", "q3", 30, 1),
	}
	m, r := a.Fetch(context.Background(), metrics)
	if len(m) != 3 {
		t.Errorf("got %d metrics", len(m))
	}
	if prom.calls.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", prom.calls.Load())
	}
	for _, res := range r {
		if res.Err != nil {
			t.Errorf("%s: err=%v", res.Name, res.Err)
		}
	}
}

func TestFetch_PartialFailure(t *testing.T) {
	prom := &stubPrometheus{values: map[string]float64{"q1": 42}}
	a := NewCustomMetricAdapter(prom)
	metrics := []slov1alpha1.CustomMetric{
		cm("good", "q1", 42, 1),
		cm("bad", "q_missing", 0, 1),
	}
	m, r := a.Fetch(context.Background(), metrics)
	if len(m) != 1 || m["good"] != 42 {
		t.Errorf("map=%v", m)
	}
	if r[1].Err == nil {
		t.Error("expected error for missing metric")
	}
}

func TestFetch_AllFail(t *testing.T) {
	prom := &stubPrometheus{err: fmt.Errorf("down")}
	a := NewCustomMetricAdapter(prom)
	m, r := a.Fetch(context.Background(), []slov1alpha1.CustomMetric{
		cm("x", "q", 0, 0),
	})
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
	if r[0].Err == nil {
		t.Error("expected error")
	}
}

func TestFetch_PreservesTargetAndWeight(t *testing.T) {
	prom := &stubPrometheus{values: map[string]float64{"q": 100}}
	a := NewCustomMetricAdapter(prom)
	_, r := a.Fetch(context.Background(), []slov1alpha1.CustomMetric{
		cm("m", "q", 95.5, 1.5),
	})
	if r[0].Target != 95.5 || r[0].Weight != 1.5 {
		t.Errorf("target=%v weight=%v", r[0].Target, r[0].Weight)
	}
}

// ---------------------------------------------------------------------------
// Score
// ---------------------------------------------------------------------------

func TestScore_Empty(t *testing.T) {
	if s := Score(nil); s != 0 {
		t.Errorf("got %f", s)
	}
}

func TestScore_OnTarget(t *testing.T) {
	results := []CustomMetricResult{{Name: "a", Value: 100, Target: 100, Weight: 1}}
	if s := Score(results); s != 0 {
		t.Errorf("got %f", s)
	}
}

func TestScore_OffTarget(t *testing.T) {
	results := []CustomMetricResult{{Name: "a", Value: 120, Target: 100, Weight: 1}}
	s := Score(results)
	expected := 1.0 * (20.0 / 100.0) // 0.2
	if math.Abs(s-expected) > 1e-9 {
		t.Errorf("got %f, want %f", s, expected)
	}
}

func TestScore_Weighted(t *testing.T) {
	results := []CustomMetricResult{
		{Name: "a", Value: 120, Target: 100, Weight: 2},
		{Name: "b", Value: 50, Target: 100, Weight: 1},
	}
	// a: 2 * (20/100)=0.4, b: 1*(50/100)=0.5 → 0.9
	expected := 0.9
	if s := Score(results); math.Abs(s-expected) > 1e-9 {
		t.Errorf("got %f, want %f", s, expected)
	}
}

func TestScore_SkipsErrors(t *testing.T) {
	results := []CustomMetricResult{
		{Name: "good", Value: 100, Target: 100, Weight: 1},
		{Name: "bad", Value: 0, Target: 100, Weight: 1, Err: fmt.Errorf("fail")},
	}
	if s := Score(results); s != 0 {
		t.Errorf("got %f, want 0", s)
	}
}

func TestScore_SkipsZeroWeight(t *testing.T) {
	results := []CustomMetricResult{{Name: "z", Value: 999, Target: 0, Weight: 0}}
	if s := Score(results); s != 0 {
		t.Errorf("got %f", s)
	}
}

func TestScore_SmallTarget(t *testing.T) {
	// target < 1 → denom clamped to 1
	results := []CustomMetricResult{{Name: "a", Value: 0.5, Target: 0.1, Weight: 1}}
	expected := 1.0 * (0.4 / 1.0) // denom=1
	if s := Score(results); math.Abs(s-expected) > 1e-9 {
		t.Errorf("got %f, want %f", s, expected)
	}
}

func TestScore_NegativeTarget(t *testing.T) {
	// |target|=50 → denom=50
	results := []CustomMetricResult{{Name: "a", Value: 0, Target: -50, Weight: 1}}
	expected := 1.0 * (50.0 / 50.0) // 1.0
	if s := Score(results); math.Abs(s-expected) > 1e-9 {
		t.Errorf("got %f, want %f", s, expected)
	}
}

// ---------------------------------------------------------------------------
// MergeIntoMetrics
// ---------------------------------------------------------------------------

func TestMerge_NilDst(t *testing.T) {
	m := MergeIntoMetrics(nil, map[string]float64{"a": 1})
	if m["a"] != 1 || len(m) != 1 {
		t.Errorf("got %v", m)
	}
}

func TestMerge_Overwrites(t *testing.T) {
	dst := map[string]float64{"a": 0, "b": 2}
	m := MergeIntoMetrics(dst, map[string]float64{"a": 99})
	if m["a"] != 99 || m["b"] != 2 {
		t.Errorf("got %v", m)
	}
}

func TestMerge_EmptyFetched(t *testing.T) {
	dst := map[string]float64{"x": 1}
	m := MergeIntoMetrics(dst, nil)
	if len(m) != 1 {
		t.Errorf("got %v", m)
	}
}

func TestMerge_BothNil(t *testing.T) {
	m := MergeIntoMetrics(nil, nil)
	if m == nil {
		t.Error("expected non-nil empty map")
	}
}

// ---------------------------------------------------------------------------
// Integration: Fetch → Score
// ---------------------------------------------------------------------------

func TestFetchThenScore(t *testing.T) {
	prom := &stubPrometheus{values: map[string]float64{
		"rate(http_requests_total[5m])":     100,
		"histogram_quantile(0.99, latency)": 0.05,
	}}
	a := NewCustomMetricAdapter(prom)
	metrics := []slov1alpha1.CustomMetric{
		cm("rps", "rate(http_requests_total[5m])", 100, 1),
		cm("p99", "histogram_quantile(0.99, latency)", 0.05, 2),
	}
	fetched, results := a.Fetch(context.Background(), metrics)
	if len(fetched) != 2 {
		t.Fatalf("expected 2, got %d", len(fetched))
	}
	// Both on-target → score = 0
	if s := Score(results); s != 0 {
		t.Errorf("score=%f, want 0", s)
	}
}
