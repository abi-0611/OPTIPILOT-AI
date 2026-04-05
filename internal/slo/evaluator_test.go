package slo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/metrics"
	"github.com/optipilot-ai/optipilot/internal/slo"
)

// --- ParseTarget tests ---

func TestParseTarget_Milliseconds(t *testing.T) {
	cases := []struct {
		input, desc string
		expected    float64
	}{
		{"200ms", "200ms", 0.200},
		{"1500ms", "1500ms", 1.500},
		{"50ms", "50ms", 0.050},
	}
	for _, tc := range cases {
		v, err := slo.ParseTarget(tc.input, slov1alpha1.MetricLatencyP99)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.desc, err)
			continue
		}
		if abs(v-tc.expected) > 1e-9 {
			t.Errorf("%s: expected %v, got %v", tc.desc, tc.expected, v)
		}
	}
}

func TestParseTarget_Percent(t *testing.T) {
	cases := []struct {
		input    string
		expected float64
	}{
		{"0.1%", 0.001},
		{"99.95%", 0.9995},
		{"0.05%", 0.0005},
		{"100%", 1.0},
	}
	for _, tc := range cases {
		v, err := slo.ParseTarget(tc.input, slov1alpha1.MetricErrorRate)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.input, err)
			continue
		}
		if abs(v-tc.expected) > 1e-9 {
			t.Errorf("%s: expected %v, got %v", tc.input, tc.expected, v)
		}
	}
}

func TestParseTarget_RPS(t *testing.T) {
	v, err := slo.ParseTarget("1000rps", slov1alpha1.MetricThroughput)
	if err != nil {
		t.Fatal(err)
	}
	if v != 1000.0 {
		t.Errorf("expected 1000.0, got %v", v)
	}
}

func TestParseTarget_BareFloat(t *testing.T) {
	v, err := slo.ParseTarget("0.5", slov1alpha1.MetricCustom)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0.5 {
		t.Errorf("expected 0.5, got %v", v)
	}
}

func TestParseTarget_Invalid(t *testing.T) {
	_, err := slo.ParseTarget("notanumber%", slov1alpha1.MetricErrorRate)
	if err == nil {
		t.Error("expected error for invalid percent value")
	}
}

// --- SLOEvaluator tests ---

// mockPrometheusClient returns a fixed value for every Query call.
type mockPrometheusClient struct {
	value float64
	err   error
}

func (m *mockPrometheusClient) Query(_ context.Context, _ string) (float64, error) {
	return m.value, m.err
}
func (m *mockPrometheusClient) QueryRange(_ context.Context, _ string, _, _ time.Time, _ time.Duration) ([]metrics.DataPoint, error) {
	return nil, nil
}
func (m *mockPrometheusClient) Healthy(_ context.Context) error { return nil }

func makeSO(metric slov1alpha1.MetricType, target string) *slov1alpha1.ServiceObjective {
	return &slov1alpha1.ServiceObjective{
		Spec: slov1alpha1.ServiceObjectiveSpec{
			TargetRef: slov1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "test-app",
			},
			Objectives: []slov1alpha1.Objective{
				{Metric: metric, Target: target, Window: "5m"},
			},
			ErrorBudget: &slov1alpha1.ErrorBudget{Total: "0.05%"},
		},
	}
}

func TestSLOEvaluator_Latency_Compliant(t *testing.T) {
	// actual=150ms (0.150s), target=200ms (0.200s) → compliant, burnRate=0
	client := &mockPrometheusClient{value: 0.150}
	builder := &slo.PromQLBuilder{MetricPrefix: "http", Labels: `ns="test"`}
	ev := slo.NewSLOEvaluator(client, builder)

	result, err := ev.Evaluate(context.Background(), makeSO(slov1alpha1.MetricLatencyP99, "200ms"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.AllCompliant {
		t.Error("expected AllCompliant=true for latency 150ms vs target 200ms")
	}
	if len(result.Objectives) != 1 {
		t.Fatal("expected 1 objective result")
	}
	obj := result.Objectives[0]
	if !obj.Compliant {
		t.Error("expected objective Compliant=true")
	}
	if obj.BurnRate != 0 {
		t.Errorf("expected BurnRate=0 when latency is compliant, got %v", obj.BurnRate)
	}
	if result.BudgetRemaining < 0.99 {
		t.Errorf("expected BudgetRemaining near 1.0, got %v", result.BudgetRemaining)
	}
}

func TestSLOEvaluator_Latency_Violation(t *testing.T) {
	// actual=350ms (0.350s), target=200ms (0.200s) → violation, burnRate>1
	client := &mockPrometheusClient{value: 0.350}
	builder := &slo.PromQLBuilder{MetricPrefix: "http", Labels: `ns="test"`}
	ev := slo.NewSLOEvaluator(client, builder)

	result, err := ev.Evaluate(context.Background(), makeSO(slov1alpha1.MetricLatencyP99, "200ms"))
	if err != nil {
		t.Fatal(err)
	}
	if result.AllCompliant {
		t.Error("expected AllCompliant=false for latency 350ms vs target 200ms")
	}
	obj := result.Objectives[0]
	if obj.Compliant {
		t.Error("expected objective Compliant=false")
	}
	if obj.BurnRate <= 1.0 {
		t.Errorf("expected BurnRate>1.0 for clear violation, got %v", obj.BurnRate)
	}
}

func TestSLOEvaluator_ErrorRate_Compliant(t *testing.T) {
	// actual=0.05% (0.0005), target=0.1% (0.001) → compliant
	client := &mockPrometheusClient{value: 0.0005}
	builder := &slo.PromQLBuilder{MetricPrefix: "http", Labels: `ns="test"`}
	ev := slo.NewSLOEvaluator(client, builder)

	result, err := ev.Evaluate(context.Background(), makeSO(slov1alpha1.MetricErrorRate, "0.1%"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.AllCompliant {
		t.Error("expected compliant for error rate 0.05% vs target 0.1%")
	}
}

func TestSLOEvaluator_Availability_100Percent(t *testing.T) {
	// actual=100% (1.0) → compliant, burnRate=0
	client := &mockPrometheusClient{value: 1.0}
	builder := &slo.PromQLBuilder{MetricPrefix: "http", Labels: `ns="test"`}
	ev := slo.NewSLOEvaluator(client, builder)

	result, err := ev.Evaluate(context.Background(), makeSO(slov1alpha1.MetricAvailability, "99.95%"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.AllCompliant {
		t.Error("expected compliant for 100% availability")
	}
	if result.Objectives[0].BurnRate != 0 {
		t.Errorf("expected BurnRate=0 for perfect availability, got %v", result.Objectives[0].BurnRate)
	}
}

func TestSLOEvaluator_BudgetExhaustion(t *testing.T) {
	// actual=1.0 error rate (100% errors) with error budget 0.05% → burn rate=2000, budget consumed in window
	client := &mockPrometheusClient{value: 1.0}
	builder := &slo.PromQLBuilder{MetricPrefix: "http", Labels: `ns="test"`}
	ev := slo.NewSLOEvaluator(client, builder)

	result, err := ev.Evaluate(context.Background(), makeSO(slov1alpha1.MetricErrorRate, "0.1%"))
	if err != nil {
		t.Fatal(err)
	}
	// BudgetRemaining should be well below 1.0: burn rate=2000, window=5m → budgetUsed ≈ 0.23
	if result.BudgetRemaining >= 1.0 {
		t.Errorf("expected BudgetRemaining < 1.0 for massive error rate, got %v", result.BudgetRemaining)
	}
	// Burn rate should be extremely high
	if result.Objectives[0].BurnRate < 100 {
		t.Errorf("expected BurnRate >= 100 for 100%% error rate vs 0.05%% budget, got %v", result.Objectives[0].BurnRate)
	}
	if result.AllCompliant {
		t.Error("expected AllCompliant=false for 100% error rate")
	}
}

func TestSLOEvaluator_NoData(t *testing.T) {
	client := &mockPrometheusClient{err: metrics.ErrNoData}
	builder := &slo.PromQLBuilder{MetricPrefix: "http", Labels: `ns="test"`}
	ev := slo.NewSLOEvaluator(client, builder)

	_, err := ev.Evaluate(context.Background(), makeSO(slov1alpha1.MetricLatencyP99, "200ms"))
	if err == nil {
		t.Error("expected error when Prometheus returns ErrNoData")
	}
	if !errors.Is(err, metrics.ErrNoData) {
		t.Errorf("expected error to wrap ErrNoData, got: %v", err)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
