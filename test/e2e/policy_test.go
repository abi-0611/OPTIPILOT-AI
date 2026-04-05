//go:build e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
)

// ── OptimizationPolicy tests ──────────────────────────────────────────────────

// TestE2E_Policy_CreateMinimalPolicy verifies a minimal OptimizationPolicy
// (one objective, no constraints) is accepted.
func TestE2E_Policy_CreateMinimalPolicy(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	p := makePolicy("e2e-minimal-policy", testNamespace, false)
	if err := testClient.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), p) })

	got := &policyv1alpha1.OptimizationPolicy{}
	if err := testClient.Get(ctx, namespacedName(p), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Spec.Objectives) == 0 {
		t.Error("objectives is empty after round-trip")
	}
}

// TestE2E_Policy_DryRunFlagPersisted verifies the dryRun flag is stored
// correctly for both true and false values.
func TestE2E_Policy_DryRunFlagPersisted(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	for _, dryRun := range []bool{true, false} {
		dryRun := dryRun
		name := "e2e-dry-run-false"
		if dryRun {
			name = "e2e-dry-run-true"
		}
		t.Run(name, func(t *testing.T) {
			p := makePolicy(name, testNamespace, dryRun)
			if err := testClient.Create(ctx, p); err != nil {
				t.Fatalf("create: %v", err)
			}
			t.Cleanup(func() { _ = testClient.Delete(context.Background(), p) })

			got := &policyv1alpha1.OptimizationPolicy{}
			if err := testClient.Get(ctx, namespacedName(p), got); err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Spec.DryRun != dryRun {
				t.Errorf("dryRun: got %v, want %v", got.Spec.DryRun, dryRun)
			}
		})
	}
}

// TestE2E_Policy_WithConstraints verifies a policy with CEL constraints
// is accepted and constraints are round-tripped correctly.
func TestE2E_Policy_WithConstraints(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	p := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-constrained-policy",
			Namespace: testNamespace,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			DryRun: true,
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "cost", Weight: 1.0, Direction: policyv1alpha1.DirectionMinimize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{Expr: "slo.errorBudgetRemaining > 0.1", Reason: "protect budget", Hard: true},
				{Expr: "slo.burnRate1h < 2.0", Reason: "no active burn", Hard: true},
			},
		},
	}
	if err := testClient.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), p) })

	got := &policyv1alpha1.OptimizationPolicy{}
	if err := testClient.Get(ctx, namespacedName(p), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Spec.Constraints) != 2 {
		t.Errorf("constraint count: got %d, want 2", len(got.Spec.Constraints))
	}
}

// TestE2E_Policy_ReconcilerSetsConditions verifies the controller reconciles
// the policy and sets at least one condition.
func TestE2E_Policy_ReconcilerSetsConditions(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	p := makePolicy("e2e-reconcile-policy", testNamespace, true)
	if err := testClient.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), p) })

	safePoll(t, 60*time.Second, 3*time.Second, func() bool {
		got := &policyv1alpha1.OptimizationPolicy{}
		if err := testClient.Get(ctx, namespacedName(p), got); err != nil {
			return false
		}
		return len(got.Status.Conditions) > 0
	})
}

// TestE2E_Policy_MultipleObjectives verifies multiple weighted objectives
// are stored correctly.
func TestE2E_Policy_MultipleObjectives(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	p := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-multi-obj-policy",
			Namespace: testNamespace,
		},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			DryRun: true,
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "cost", Weight: 0.6, Direction: policyv1alpha1.DirectionMinimize},
				{Name: "slo_compliance", Weight: 0.3, Direction: policyv1alpha1.DirectionMaximize},
				{Name: "carbon", Weight: 0.1, Direction: policyv1alpha1.DirectionMinimize},
			},
		},
	}
	if err := testClient.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), p) })

	got := &policyv1alpha1.OptimizationPolicy{}
	if err := testClient.Get(ctx, namespacedName(p), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Spec.Objectives) != 3 {
		t.Errorf("objectives: got %d, want 3", len(got.Spec.Objectives))
	}
}

// ── TenantProfile tests ───────────────────────────────────────────────────────

// TestE2E_TenantProfile_CreateGoldTenant verifies a gold-tier TenantProfile
// is accepted and stored.
func TestE2E_TenantProfile_CreateGoldTenant(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	tp := &tenantv1alpha1.TenantProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-gold-tenant",
			Namespace: helmNamespace,
		},
		Spec: tenantv1alpha1.TenantProfileSpec{
			Tier:       tenantv1alpha1.TierGold,
			Weight:     10,
			Namespaces: []string{testNamespace},
			Budgets: &tenantv1alpha1.TenantBudgets{
				MaxCores:          32,
				MaxMonthlyCostUSD: "500",
			},
		},
	}
	if err := testClient.Create(ctx, tp); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), tp) })

	got := &tenantv1alpha1.TenantProfile{}
	if err := testClient.Get(ctx, namespacedName(tp), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.Tier != tenantv1alpha1.TierGold {
		t.Errorf("tier: got %q, want gold", got.Spec.Tier)
	}
}

// TestE2E_TenantProfile_FairSharePolicyStored verifies the FairSharePolicy
// sub-object is persisted correctly.
func TestE2E_TenantProfile_FairSharePolicyStored(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	tp := &tenantv1alpha1.TenantProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-fairshare-tenant",
			Namespace: helmNamespace,
		},
		Spec: tenantv1alpha1.TenantProfileSpec{
			Tier:       tenantv1alpha1.TierSilver,
			Weight:     5,
			Namespaces: []string{testNamespace},
			FairSharePolicy: &tenantv1alpha1.FairSharePolicy{
				GuaranteedCoresPercent: 20,
				Burstable:              true,
				MaxBurstPercent:        100,
			},
		},
	}
	if err := testClient.Create(ctx, tp); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), tp) })

	got := &tenantv1alpha1.TenantProfile{}
	if err := testClient.Get(ctx, namespacedName(tp), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.FairSharePolicy == nil {
		t.Fatal("fairSharePolicy is nil after round-trip")
	}
	if !got.Spec.FairSharePolicy.Burstable {
		t.Error("burstable: got false, want true")
	}
}

// TestE2E_TenantProfile_ReconcilerSetsConditions verifies the tenant controller
// reconciles and populates status conditions.
func TestE2E_TenantProfile_ReconcilerSetsConditions(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	tp := &tenantv1alpha1.TenantProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-reconcile-tenant",
			Namespace: helmNamespace,
		},
		Spec: tenantv1alpha1.TenantProfileSpec{
			Tier:       tenantv1alpha1.TierBronze,
			Weight:     1,
			Namespaces: []string{testNamespace},
		},
	}
	if err := testClient.Create(ctx, tp); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), tp) })

	safePoll(t, 60*time.Second, 3*time.Second, func() bool {
		got := &tenantv1alpha1.TenantProfile{}
		if err := testClient.Get(ctx, namespacedName(tp), got); err != nil {
			return false
		}
		return len(got.Status.Conditions) > 0
	})
}

// ── Combined SLO + Policy scenario ───────────────────────────────────────────

// TestE2E_Policy_SLOAndPolicyInSameNamespace verifies that a ServiceObjective
// and OptimizationPolicy can coexist in the same namespace without conflicts.
func TestE2E_Policy_SLOAndPolicyInSameNamespace(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := makeServiceObjective("e2e-coexist-slo", testNamespace,
		slov1alpha1.MetricAvailability, "99.5%", "30d")
	p := makePolicy("e2e-coexist-policy", testNamespace, true)

	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create SLO: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), so) })

	if err := testClient.Create(ctx, p); err != nil {
		t.Fatalf("create Policy: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), p) })

	// Both should be listable.
	soList := &slov1alpha1.ServiceObjectiveList{}
	if err := testClient.List(ctx, soList); err != nil {
		t.Errorf("list SLOs: %v", err)
	}
	pList := &policyv1alpha1.OptimizationPolicyList{}
	if err := testClient.List(ctx, pList); err != nil {
		t.Errorf("list Policies: %v", err)
	}
}

// ── Fixtures ──────────────────────────────────────────────────────────────────

func makePolicy(name, ns string, dryRun bool) *policyv1alpha1.OptimizationPolicy {
	return &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			DryRun: dryRun,
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "cost", Weight: 1.0, Direction: policyv1alpha1.DirectionMinimize},
			},
		},
	}
}
