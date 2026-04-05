package v1alpha1_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
)

// TestTenantProfile_DeepCopy verifies deep copy independence.
func TestTenantProfile_DeepCopy(t *testing.T) {
	tp := &tenantv1alpha1.TenantProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "team-checkout"},
		Spec: tenantv1alpha1.TenantProfileSpec{
			Tier:       tenantv1alpha1.TierGold,
			Weight:     10,
			Namespaces: []string{"ecommerce", "ecommerce-staging"},
			Budgets: &tenantv1alpha1.TenantBudgets{
				MaxMonthlyCostUSD: "5000",
				MaxCores:          200,
				MaxMemoryGiB:      800,
			},
			FairSharePolicy: &tenantv1alpha1.FairSharePolicy{
				GuaranteedCoresPercent: 15,
				Burstable:              true,
				MaxBurstPercent:        30,
			},
		},
		Status: tenantv1alpha1.TenantProfileStatus{
			CurrentCostUSD:   "1234.56",
			AllocationStatus: "guaranteed",
		},
	}

	copy := tp.DeepCopy()

	// Mutate namespaces slice in copy.
	copy.Spec.Namespaces[0] = "other-namespace"
	if tp.Spec.Namespaces[0] != "ecommerce" {
		t.Error("DeepCopy: modifying copy's namespaces slice affected original")
	}

	// Mutate budget pointer in copy.
	copy.Spec.Budgets.MaxCores = 999
	if tp.Spec.Budgets.MaxCores != 200 {
		t.Error("DeepCopy: modifying copy's budgets pointer affected original")
	}

	// Mutate fair-share policy in copy.
	copy.Spec.FairSharePolicy.GuaranteedCoresPercent = 50
	if tp.Spec.FairSharePolicy.GuaranteedCoresPercent != 15 {
		t.Error("DeepCopy: modifying copy's fair-share policy affected original")
	}

	// Nil safety.
	var nilTP *tenantv1alpha1.TenantProfile
	if nilTP.DeepCopy() != nil {
		t.Error("DeepCopy of nil should return nil")
	}
}

// TestTenantProfileList_DeepCopy verifies list deep copy.
func TestTenantProfileList_DeepCopy(t *testing.T) {
	list := &tenantv1alpha1.TenantProfileList{
		Items: []tenantv1alpha1.TenantProfile{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "tenant-1"},
				Spec: tenantv1alpha1.TenantProfileSpec{
					Tier:       tenantv1alpha1.TierSilver,
					Namespaces: []string{"ns-1"},
				},
			},
		},
	}

	copy := list.DeepCopy()
	copy.Items[0].Name = "mutated"
	if list.Items[0].Name != "tenant-1" {
		t.Error("DeepCopyList: modifying copy affected original")
	}
}

// TestTenantProfile_DeepCopyObject verifies runtime.Object interface.
func TestTenantProfile_DeepCopyObject(t *testing.T) {
	tp := &tenantv1alpha1.TenantProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: tenantv1alpha1.TenantProfileSpec{
			Tier:       tenantv1alpha1.TierBronze,
			Namespaces: []string{"ns-test"},
		},
	}
	obj := tp.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*tenantv1alpha1.TenantProfile); !ok {
		t.Errorf("DeepCopyObject returned wrong type: %T", obj)
	}
}

// TestTenantTier_Constants verifies all TenantTier constants.
func TestTenantTier_Constants(t *testing.T) {
	cases := []struct {
		tier     tenantv1alpha1.TenantTier
		expected string
	}{
		{tenantv1alpha1.TierPlatinum, "platinum"},
		{tenantv1alpha1.TierGold, "gold"},
		{tenantv1alpha1.TierSilver, "silver"},
		{tenantv1alpha1.TierBronze, "bronze"},
	}
	for _, tc := range cases {
		if string(tc.tier) != tc.expected {
			t.Errorf("TenantTier: expected %q got %q", tc.expected, string(tc.tier))
		}
	}
}
