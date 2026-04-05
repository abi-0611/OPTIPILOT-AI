package v1alpha1_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
)

// TestOptimizationPolicy_DeepCopy verifies deep copy independence.
func TestOptimizationPolicy_DeepCopy(t *testing.T) {
	op := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "test-policy"},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"tier": "non-critical"},
			},
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "cost", Weight: 0.6, Direction: policyv1alpha1.DirectionMinimize},
				{Name: "slo_compliance", Weight: 0.3, Direction: policyv1alpha1.DirectionMaximize},
			},
			Constraints: []policyv1alpha1.PolicyConstraint{
				{Expr: "candidate.replicas >= 2", Reason: "minimum replicas", Hard: true},
			},
			ScalingBehavior: &policyv1alpha1.ScalingBehavior{
				ScaleUp:   &policyv1alpha1.ScalingRule{MaxPercent: 100, CooldownSeconds: 60},
				ScaleDown: &policyv1alpha1.ScalingRule{MaxPercent: 20, CooldownSeconds: 300},
			},
			DryRun:   false,
			Priority: 100,
		},
	}

	copy := op.DeepCopy()

	// Mutate copy and verify original is unchanged.
	copy.Spec.Objectives[0].Weight = 0.9
	if op.Spec.Objectives[0].Weight != 0.6 {
		t.Error("DeepCopy: modifying copy's objectives slice affected original")
	}

	copy.Spec.Selector.MatchLabels["tier"] = "critical"
	if op.Spec.Selector.MatchLabels["tier"] != "non-critical" {
		t.Error("DeepCopy: modifying copy's selector map affected original")
	}

	copy.Spec.ScalingBehavior.ScaleUp.MaxPercent = 200
	if op.Spec.ScalingBehavior.ScaleUp.MaxPercent != 100 {
		t.Error("DeepCopy: modifying copy's scaling behavior affected original")
	}

	// Nil safety
	var nilOP *policyv1alpha1.OptimizationPolicy
	if nilOP.DeepCopy() != nil {
		t.Error("DeepCopy of nil should return nil")
	}
}

// TestOptimizationPolicyList_DeepCopy verifies list deep copy.
func TestOptimizationPolicyList_DeepCopy(t *testing.T) {
	list := &policyv1alpha1.OptimizationPolicyList{
		Items: []policyv1alpha1.OptimizationPolicy{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "policy-1"},
				Spec: policyv1alpha1.OptimizationPolicySpec{
					Objectives: []policyv1alpha1.PolicyObjective{
						{Name: "cost", Weight: 0.5, Direction: policyv1alpha1.DirectionMinimize},
					},
				},
			},
		},
	}

	copy := list.DeepCopy()
	copy.Items[0].Name = "mutated"
	if list.Items[0].Name != "policy-1" {
		t.Error("DeepCopyList: modifying copy affected original")
	}
}

// TestOptimizationPolicy_DeepCopyObject verifies runtime.Object interface.
func TestOptimizationPolicy_DeepCopyObject(t *testing.T) {
	op := &policyv1alpha1.OptimizationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: policyv1alpha1.OptimizationPolicySpec{
			Objectives: []policyv1alpha1.PolicyObjective{
				{Name: "cost", Weight: 0.5, Direction: policyv1alpha1.DirectionMinimize},
			},
		},
	}
	obj := op.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*policyv1alpha1.OptimizationPolicy); !ok {
		t.Errorf("DeepCopyObject returned wrong type: %T", obj)
	}
}

// TestOptimizationDirection_Constants verifies direction constants.
func TestOptimizationDirection_Constants(t *testing.T) {
	if string(policyv1alpha1.DirectionMinimize) != "minimize" {
		t.Errorf("expected 'minimize' got %q", policyv1alpha1.DirectionMinimize)
	}
	if string(policyv1alpha1.DirectionMaximize) != "maximize" {
		t.Errorf("expected 'maximize' got %q", policyv1alpha1.DirectionMaximize)
	}
}
