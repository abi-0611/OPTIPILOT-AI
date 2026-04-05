package v1alpha1_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
)

// TestServiceObjective_DeepCopy verifies that DeepCopy produces an independent copy.
func TestServiceObjective_DeepCopy(t *testing.T) {
	original := &slov1alpha1.ServiceObjective{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-slo",
			Namespace: "default",
		},
		Spec: slov1alpha1.ServiceObjectiveSpec{
			TargetRef: slov1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "my-app",
			},
			Objectives: []slov1alpha1.Objective{
				{Metric: slov1alpha1.MetricLatencyP99, Target: "200ms", Window: "5m"},
				{Metric: slov1alpha1.MetricErrorRate, Target: "0.1%", Window: "5m"},
			},
			ErrorBudget: &slov1alpha1.ErrorBudget{
				Total: "0.05%",
				BurnRateAlerts: []slov1alpha1.BurnRateAlert{
					{Severity: "warning", ShortWindow: "5m", LongWindow: "1h", Factor: 14.4},
				},
			},
			CostConstraint:     &slov1alpha1.CostConstraint{MaxHourlyCostUSD: "50.00"},
			TenantRef:          &slov1alpha1.TenantReference{Name: "team-checkout"},
			EvaluationInterval: "30s",
			CustomMetrics: []slov1alpha1.CustomMetric{
				{Name: "custom_metric", Query: "sum(rate(...[5m]))", Target: 0.99, Weight: 1.0},
			},
		},
		Status: slov1alpha1.ServiceObjectiveStatus{
			BudgetRemaining: "98.5%",
			CurrentBurn:     map[string]string{"latency_p99": "0.5"},
		},
	}

	copy := original.DeepCopy()

	// Verify independence: mutating copy should not affect original.
	copy.Spec.Objectives[0].Target = "500ms"
	if original.Spec.Objectives[0].Target != "200ms" {
		t.Error("DeepCopy: modifying copy's slice element affected original")
	}

	copy.Spec.ErrorBudget.Total = "1%"
	if original.Spec.ErrorBudget.Total != "0.05%" {
		t.Error("DeepCopy: modifying copy's pointer field affected original")
	}

	copy.Status.CurrentBurn["latency_p99"] = "2.0"
	if original.Status.CurrentBurn["latency_p99"] != "0.5" {
		t.Error("DeepCopy: modifying copy's map affected original")
	}

	// Verify nil safety
	var nilSO *slov1alpha1.ServiceObjective
	if nilSO.DeepCopy() != nil {
		t.Error("DeepCopy of nil should return nil")
	}
}

// TestServiceObjectiveList_DeepCopy verifies list deep copy.
func TestServiceObjectiveList_DeepCopy(t *testing.T) {
	list := &slov1alpha1.ServiceObjectiveList{
		Items: []slov1alpha1.ServiceObjective{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "slo-1"},
				Spec: slov1alpha1.ServiceObjectiveSpec{
					TargetRef:  slov1alpha1.CrossVersionObjectReference{Name: "app-1"},
					Objectives: []slov1alpha1.Objective{{Metric: slov1alpha1.MetricAvailability, Target: "99.9%"}},
				},
			},
		},
	}

	copy := list.DeepCopy()
	copy.Items[0].Name = "mutated"
	if list.Items[0].Name != "slo-1" {
		t.Error("DeepCopyList: modifying copy affected original")
	}
}

// TestServiceObjective_DeepCopyObject verifies runtime.Object interface.
func TestServiceObjective_DeepCopyObject(t *testing.T) {
	so := &slov1alpha1.ServiceObjective{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: slov1alpha1.ServiceObjectiveSpec{
			TargetRef:  slov1alpha1.CrossVersionObjectReference{Name: "app"},
			Objectives: []slov1alpha1.Objective{{Metric: slov1alpha1.MetricThroughput, Target: "1000rps"}},
		},
	}
	obj := so.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*slov1alpha1.ServiceObjective); !ok {
		t.Errorf("DeepCopyObject returned wrong type: %T", obj)
	}
}

// TestMetricType_Constants verifies all MetricType constants are defined correctly.
func TestMetricType_Constants(t *testing.T) {
	cases := []struct {
		name     string
		val      slov1alpha1.MetricType
		expected string
	}{
		{"LatencyP50", slov1alpha1.MetricLatencyP50, "latency_p50"},
		{"LatencyP90", slov1alpha1.MetricLatencyP90, "latency_p90"},
		{"LatencyP95", slov1alpha1.MetricLatencyP95, "latency_p95"},
		{"LatencyP99", slov1alpha1.MetricLatencyP99, "latency_p99"},
		{"ErrorRate", slov1alpha1.MetricErrorRate, "error_rate"},
		{"Availability", slov1alpha1.MetricAvailability, "availability"},
		{"Throughput", slov1alpha1.MetricThroughput, "throughput"},
		{"Custom", slov1alpha1.MetricCustom, "custom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.val) != tc.expected {
				t.Errorf("expected %q got %q", tc.expected, string(tc.val))
			}
		})
	}
}

// TestServiceObjectiveSpec_Defaults verifies default values declared with kubebuilder defaults.
func TestServiceObjectiveSpec_Defaults(t *testing.T) {
	// The +kubebuilder:default marker is applied by the API server admission webhook.
	// This test verifies the Go zero value behaviour before defaulting.
	spec := slov1alpha1.ServiceObjectiveSpec{}
	if spec.EvaluationInterval != "" {
		t.Errorf("expected zero value empty string, got %q", spec.EvaluationInterval)
	}
	// Default value is "30s" — assigned by admission webhook; not Go zero value.
}
