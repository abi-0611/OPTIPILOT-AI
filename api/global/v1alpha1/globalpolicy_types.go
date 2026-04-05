package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

// +kubebuilder:validation:Enum=latency-optimized;cost-optimized;carbon-optimized;balanced
type TrafficStrategy string

const (
	StrategyLatency TrafficStrategy = "latency-optimized"
	StrategyCost    TrafficStrategy = "cost-optimized"
	StrategyCarbon  TrafficStrategy = "carbon-optimized"
	StrategyBalance TrafficStrategy = "balanced"
)

// ---------------------------------------------------------------------------
// Spec sub-types
// ---------------------------------------------------------------------------

// TrafficShiftingSpec configures cross-cluster traffic weight management.
type TrafficShiftingSpec struct {
	// Strategy selects the optimization objective for traffic distribution.
	Strategy TrafficStrategy `json:"strategy"`
	// MaxShiftPerCyclePercent caps how much traffic can move in one optimization cycle.
	MaxShiftPerCyclePercent int32 `json:"maxShiftPerCyclePercent,omitempty"`
	// MinDestinationSLOPercent is the minimum SLO compliance % at the destination before
	// traffic is shifted there.
	MinDestinationSLOPercent float64 `json:"minDestinationSLOPercent,omitempty"`
	// RollbackWindowSeconds is how long the hub watches a destination cluster after a shift
	// before auto-rolling back on SLO degradation.
	RollbackWindowSeconds int32 `json:"rollbackWindowSeconds,omitempty"`
	// ClusterSelector optionally limits which spoke clusters participate.
	ClusterSelector *metav1.LabelSelector `json:"clusterSelector,omitempty"`
}

// ClusterLifecycleSpec configures hibernation and wake-up policies.
type ClusterLifecycleSpec struct {
	// HibernationEnabled gates the hibernation feature.
	HibernationEnabled bool `json:"hibernationEnabled,omitempty"`
	// MinActiveClusters is the minimum number of clusters that must stay active.
	MinActiveClusters int32 `json:"minActiveClusters,omitempty"`
	// IdleThresholdPercent: cluster utilisation below this is considered idle.
	IdleThresholdPercent int32 `json:"idleThresholdPercent,omitempty"`
	// IdleWindowMinutes: how many consecutive minutes of idle before hibernation.
	IdleWindowMinutes int32 `json:"idleWindowMinutes,omitempty"`
	// WakeupLeadMinutes: how far ahead of predicted demand to restore a cluster.
	WakeupLeadMinutes int32 `json:"wakeupLeadMinutes,omitempty"`
	// ExcludedClusters lists clusters that must never be hibernated.
	ExcludedClusters []string `json:"excludedClusters,omitempty"`
}

// CrossClusterConstraint expresses a placement/routing rule across clusters.
type CrossClusterConstraint struct {
	// Name identifies this constraint.
	Name string `json:"name"`
	// TenantName scopes this constraint to a specific tenant.
	TenantName string `json:"tenantName,omitempty"`
	// RequiredRegions restricts the tenant to these regions.
	RequiredRegions []string `json:"requiredRegions,omitempty"`
	// ForbiddenProviders bans the tenant from these cloud providers.
	ForbiddenProviders []CloudProvider `json:"forbiddenProviders,omitempty"`
	// MaxClustersPerTenant caps the number of clusters a tenant may span.
	MaxClustersPerTenant int32 `json:"maxClustersPerTenant,omitempty"`
}

// GlobalPolicySpec defines hub-level optimization behaviour.
type GlobalPolicySpec struct {
	// TrafficShifting configures cross-cluster traffic management.
	TrafficShifting *TrafficShiftingSpec `json:"trafficShifting,omitempty"`
	// ClusterLifecycle configures hibernation and wake-up rules.
	ClusterLifecycle *ClusterLifecycleSpec `json:"clusterLifecycle,omitempty"`
	// CrossClusterConstraints lists placement/routing rules.
	CrossClusterConstraints []CrossClusterConstraint `json:"crossClusterConstraints,omitempty"`
	// OptimizationIntervalSeconds is the global optimization loop period.
	OptimizationIntervalSeconds int32 `json:"optimizationIntervalSeconds,omitempty"`
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// GlobalPolicyStatus is the observed state of the global policy.
type GlobalPolicyStatus struct {
	// LastOptimizationTime is when the hub last ran a global optimization pass.
	LastOptimizationTime *metav1.Time `json:"lastOptimizationTime,omitempty"`
	// ActiveClusters is the number of currently active spoke clusters.
	ActiveClusters int32 `json:"activeClusters,omitempty"`
	// HibernatingClusters is the number of hibernating spoke clusters.
	HibernatingClusters int32 `json:"hibernatingClusters,omitempty"`
	// LastDirectiveSummary is a human-readable summary of the last optimisation pass.
	LastDirectiveSummary string `json:"lastDirectiveSummary,omitempty"`
	// Conditions provide detailed per-aspect status.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ---------------------------------------------------------------------------
// Root types
// ---------------------------------------------------------------------------

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=gp
// +kubebuilder:printcolumn:name="Strategy",type=string,JSONPath=`.spec.trafficShifting.strategy`
// +kubebuilder:printcolumn:name="Active",type=integer,JSONPath=`.status.activeClusters`
// +kubebuilder:printcolumn:name="Hibernating",type=integer,JSONPath=`.status.hibernatingClusters`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GlobalPolicy represents a hub-level optimization policy for cross-cluster management.
type GlobalPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GlobalPolicySpec   `json:"spec,omitempty"`
	Status GlobalPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GlobalPolicyList contains a list of GlobalPolicy.
type GlobalPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GlobalPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GlobalPolicy{}, &GlobalPolicyList{})
}
