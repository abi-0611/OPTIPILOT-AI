package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TenantTier defines the supported service tiers
// +kubebuilder:validation:Enum=platinum;gold;silver;bronze
type TenantTier string

const (
	TierPlatinum TenantTier = "platinum"
	TierGold     TenantTier = "gold"
	TierSilver   TenantTier = "silver"
	TierBronze   TenantTier = "bronze"
)

// TenantProfileSpec defines the desired state of TenantProfile
type TenantProfileSpec struct {
	// Tier determines the service level for this tenant
	Tier TenantTier `json:"tier"`

	// Weight is the relative priority weight for fair-share allocation
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	// +kubebuilder:default=10
	Weight int32 `json:"weight,omitempty"`

	// Namespaces lists the Kubernetes namespaces owned by this tenant
	// +kubebuilder:validation:MinItems=1
	Namespaces []string `json:"namespaces"`

	// Budgets defines hard resource and cost limits
	// +optional
	Budgets *TenantBudgets `json:"budgets,omitempty"`

	// FairSharePolicy defines guaranteed and burst allocation parameters
	// +optional
	FairSharePolicy *FairSharePolicy `json:"fairSharePolicy,omitempty"`
}

// TenantBudgets defines the hard resource and cost limits for a tenant
type TenantBudgets struct {
	// MaxMonthlyCostUSD is the hard monthly cost ceiling
	// +optional
	MaxMonthlyCostUSD string `json:"maxMonthlyCostUSD,omitempty"`

	// MaxCores is the maximum CPU cores across all namespaces
	// +optional
	MaxCores int32 `json:"maxCores,omitempty"`

	// MaxMemoryGiB is the maximum memory in GiB across all namespaces
	// +optional
	MaxMemoryGiB int32 `json:"maxMemoryGiB,omitempty"`

	// MaxGPUs is the maximum GPU count across all namespaces
	// +optional
	MaxGPUs int32 `json:"maxGPUs,omitempty"`
}

// FairSharePolicy defines how cluster resources are divided among tenants
type FairSharePolicy struct {
	// GuaranteedCoresPercent is the minimum percentage of cluster CPU guaranteed
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	GuaranteedCoresPercent int32 `json:"guaranteedCoresPercent"`

	// Burstable allows this tenant to use more than guaranteed share when available
	// +kubebuilder:default=true
	Burstable bool `json:"burstable,omitempty"`

	// MaxBurstPercent is the maximum percentage of cluster CPU when bursting
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	MaxBurstPercent int32 `json:"maxBurstPercent,omitempty"`
}

// TenantProfileStatus defines the observed state of TenantProfile
type TenantProfileStatus struct {
	// CurrentCostUSD is the current month-to-date cost
	// +optional
	CurrentCostUSD string `json:"currentCostUSD,omitempty"`

	// CurrentCores is the current CPU cores in use
	// +optional
	CurrentCores int32 `json:"currentCores,omitempty"`

	// CurrentMemoryGiB is the current memory in use
	// +optional
	CurrentMemoryGiB int32 `json:"currentMemoryGiB,omitempty"`

	// FairnessScore is this tenant's contribution to the global Jain's index (0–1)
	// +optional
	FairnessScore string `json:"fairnessScore,omitempty"`

	// AllocationStatus indicates whether tenant is at, above, or below guaranteed share
	// +optional
	// +kubebuilder:validation:Enum=guaranteed;bursting;throttled;under_allocated
	AllocationStatus string `json:"allocationStatus,omitempty"`

	// Conditions represent the latest available observations
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=tp
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=`.spec.tier`
// +kubebuilder:printcolumn:name="Weight",type=integer,JSONPath=`.spec.weight`
// +kubebuilder:printcolumn:name="Cost",type=string,JSONPath=`.status.currentCostUSD`
// +kubebuilder:printcolumn:name="Fairness",type=string,JSONPath=`.status.fairnessScore`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TenantProfile is the Schema for the tenantprofiles API.
// TenantProfile is cluster-scoped because a single tenant can span multiple namespaces.
type TenantProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TenantProfileSpec   `json:"spec,omitempty"`
	Status            TenantProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TenantProfileList contains a list of TenantProfile
type TenantProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TenantProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TenantProfile{}, &TenantProfileList{})
}
