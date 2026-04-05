//go:build e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
)

// ── SLO lifecycle tests ───────────────────────────────────────────────────────

// TestE2E_SLO_CreateAvailabilitySLO verifies a ServiceObjective with an
// availability objective is accepted and persisted.
func TestE2E_SLO_CreateAvailabilitySLO(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := makeServiceObjective("e2e-availability-slo", testNamespace,
		slov1alpha1.MetricAvailability, "99.9%", "30d")

	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), so) })

	got := &slov1alpha1.ServiceObjective{}
	if err := testClient.Get(ctx, namespacedName(so), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.Objectives[0].Target != "99.9%" {
		t.Errorf("target: got %q, want 99.9%%", got.Spec.Objectives[0].Target)
	}
}

// TestE2E_SLO_CreateLatencySLO verifies a latency-typed ServiceObjective.
func TestE2E_SLO_CreateLatencySLO(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := makeServiceObjective("e2e-latency-slo", testNamespace,
		slov1alpha1.MetricLatencyP99, "200ms", "5m")

	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), so) })

	got := &slov1alpha1.ServiceObjective{}
	if err := testClient.Get(ctx, namespacedName(so), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.Objectives[0].Metric != slov1alpha1.MetricLatencyP99 {
		t.Errorf("metric: got %q, want latency_p99", got.Spec.Objectives[0].Metric)
	}
}

// TestE2E_SLO_ReconcilerSetsConditions verifies the SLO controller reconciles
// the CR and populates at least one condition within the reconcile timeout.
// (The condition may indicate Prometheus is unreachable — that is fine;
// what matters is that the controller ran and updated status.)
func TestE2E_SLO_ReconcilerSetsConditions(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := makeServiceObjective("e2e-reconcile-slo", testNamespace,
		slov1alpha1.MetricAvailability, "99.5%", "30d")
	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), so) })

	safePoll(t, 60*time.Second, 3*time.Second, func() bool {
		got := &slov1alpha1.ServiceObjective{}
		if err := testClient.Get(ctx, namespacedName(so), got); err != nil {
			return false
		}
		return len(got.Status.Conditions) > 0
	})
}

// TestE2E_SLO_ObservedGenerationAdvances verifies status.observedGeneration is
// set after at least one reconcile pass.
func TestE2E_SLO_ObservedGenerationAdvances(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := makeServiceObjective("e2e-gen-slo", testNamespace,
		slov1alpha1.MetricAvailability, "99.5%", "30d")
	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), so) })

	safePoll(t, 60*time.Second, 3*time.Second, func() bool {
		got := &slov1alpha1.ServiceObjective{}
		if err := testClient.Get(ctx, namespacedName(so), got); err != nil {
			return false
		}
		return got.Status.ObservedGeneration >= 1
	})
}

// TestE2E_SLO_MultipleObjectives verifies that a ServiceObjective with two
// objectives (availability + error rate) is accepted.
func TestE2E_SLO_MultipleObjectives(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := &slov1alpha1.ServiceObjective{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-multi-obj-slo",
			Namespace: testNamespace,
		},
		Spec: slov1alpha1.ServiceObjectiveSpec{
			TargetRef: slov1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "multi-obj-app",
			},
			Objectives: []slov1alpha1.Objective{
				{Metric: slov1alpha1.MetricAvailability, Target: "99.9%", Window: "30d"},
				{Metric: slov1alpha1.MetricLatencyP99, Target: "300ms", Window: "5m"},
			},
		},
	}
	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), so) })

	got := &slov1alpha1.ServiceObjective{}
	if err := testClient.Get(ctx, namespacedName(so), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Spec.Objectives) != 2 {
		t.Errorf("objectives count: got %d, want 2", len(got.Spec.Objectives))
	}
}

// TestE2E_SLO_WithErrorBudget verifies the ErrorBudget field is stored.
func TestE2E_SLO_WithErrorBudget(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := makeServiceObjective("e2e-budget-slo", testNamespace,
		slov1alpha1.MetricAvailability, "99.9%", "30d")
	so.Spec.ErrorBudget = &slov1alpha1.ErrorBudget{Total: "0.1%"}
	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), so) })

	got := &slov1alpha1.ServiceObjective{}
	if err := testClient.Get(ctx, namespacedName(so), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.ErrorBudget == nil {
		t.Error("errorBudget is nil after round-trip")
	} else if got.Spec.ErrorBudget.Total != "0.1%" {
		t.Errorf("errorBudget.total: got %q, want 0.1%%", got.Spec.ErrorBudget.Total)
	}
}

// TestE2E_SLO_DeletePropagatesCleanly verifies that a ServiceObjective can be
// deleted without errors and disappears from the API server.
func TestE2E_SLO_DeletePropagatesCleanly(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := makeServiceObjective("e2e-delete-slo", testNamespace,
		slov1alpha1.MetricAvailability, "99.5%", "30d")
	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := testClient.Delete(ctx, so); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Object should be gone within a few seconds.
	safePoll(t, 15*time.Second, 1*time.Second, func() bool {
		got := &slov1alpha1.ServiceObjective{}
		err := testClient.Get(ctx, namespacedName(so), got)
		return err != nil // not-found error means it's gone
	})
}

// ── Fixtures ──────────────────────────────────────────────────────────────────

func makeServiceObjective(name, ns string, metric slov1alpha1.MetricType, target, window string) *slov1alpha1.ServiceObjective {
	return &slov1alpha1.ServiceObjective{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: slov1alpha1.ServiceObjectiveSpec{
			TargetRef: slov1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       name + "-workload",
			},
			Objectives: []slov1alpha1.Objective{
				{Metric: metric, Target: target, Window: window},
			},
		},
	}
}

func namespacedName(obj metav1.Object) types.NamespacedName {
	return types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
}
