package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OptimizationPolicySpec defines the desired state of OptimizationPolicy
type OptimizationPolicySpec struct {
	// Selector determines which ServiceObjectives this policy applies to
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// Objectives defines optimization objectives with weights
	// +kubebuilder:validation:MinItems=1
	Objectives []PolicyObjective `json:"objectives"`

	// Constraints are CEL expressions that candidate plans must satisfy
	// +optional
	Constraints []PolicyConstraint `json:"constraints,omitempty"`

	// ScalingBehavior controls the rate and safety of scaling actions
	// +optional
	ScalingBehavior *ScalingBehavior `json:"scalingBehavior,omitempty"`

	// DryRun when true logs decisions without actuating them
	// +optional
	// +kubebuilder:default=false
	DryRun bool `json:"dryRun,omitempty"`

	// Priority determines precedence when multiple policies match (higher wins)
	// +optional
	// +kubebuilder:default=100
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000
	Priority int32 `json:"priority,omitempty"`
}

// OptimizationDirection specifies whether to minimize or maximize an objective
// +kubebuilder:validation:Enum=minimize;maximize
type OptimizationDirection string

const (
	DirectionMinimize OptimizationDirection = "minimize"
	DirectionMaximize OptimizationDirection = "maximize"
)

// PolicyObjective defines a single optimization objective with weight and direction
type PolicyObjective struct {
	// Name of the objective (e.g., "cost", "slo_compliance", "carbon", "fairness")
	Name string `json:"name"`

	// Weight is the relative importance (all weights are normalized at runtime).
	// Value must be between 0 and 1 inclusive.
	Weight float64 `json:"weight"`

	// Direction: minimize (cost, carbon) or maximize (slo_compliance, fairness)
	Direction OptimizationDirection `json:"direction"`
}

// PolicyConstraint defines a CEL expression constraint on candidate plans
type PolicyConstraint struct {
	// Expr is a CEL expression evaluated against the decision context.
	// Must return a boolean.
	Expr string `json:"expr"`

	// Reason is a human-readable explanation shown when this constraint blocks a candidate
	Reason string `json:"reason"`

	// Hard when true means violation rejects the candidate entirely.
	// When false, violation adds a penalty to the candidate's score.
	// +optional
	// +kubebuilder:default=true
	Hard bool `json:"hard,omitempty"`
}

// ScalingBehavior controls how fast and safely scaling actions are applied
type ScalingBehavior struct {
	ScaleUp   *ScalingRule `json:"scaleUp,omitempty"`
	ScaleDown *ScalingRule `json:"scaleDown,omitempty"`
}

// ScalingRule defines a per-direction rate limit for scaling
type ScalingRule struct {
	// MaxPercent is the maximum percentage change per scaling cycle
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=500
	// +kubebuilder:default=100
	MaxPercent int32 `json:"maxPercent,omitempty"`

	// CooldownSeconds is the minimum time between scaling actions
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=60
	CooldownSeconds int32 `json:"cooldownSeconds,omitempty"`
}

// OptimizationPolicyStatus defines the observed state of OptimizationPolicy
type OptimizationPolicyStatus struct {
	// MatchedServices is the count of ServiceObjectives this policy matches
	MatchedServices int32 `json:"matchedServices,omitempty"`

	// CELCompilationStatus indicates whether all constraints compiled successfully
	// +optional
	CELCompilationStatus string `json:"celCompilationStatus,omitempty"`

	// Conditions represent the latest available observations
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=opol
// +kubebuilder:printcolumn:name="DryRun",type=boolean,JSONPath=`.spec.dryRun`
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Matched",type=integer,JSONPath=`.status.matchedServices`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OptimizationPolicy is the Schema for the optimizationpolicies API
type OptimizationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              OptimizationPolicySpec   `json:"spec,omitempty"`
	Status            OptimizationPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OptimizationPolicyList contains a list of OptimizationPolicy
type OptimizationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OptimizationPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OptimizationPolicy{}, &OptimizationPolicyList{})
}
