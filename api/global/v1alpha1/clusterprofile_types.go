package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

// +kubebuilder:validation:Enum=aws;gcp;azure;on-prem;other
type CloudProvider string

const (
	ProviderAWS    CloudProvider = "aws"
	ProviderGCP    CloudProvider = "gcp"
	ProviderAzure  CloudProvider = "azure"
	ProviderOnPrem CloudProvider = "on-prem"
	ProviderOther  CloudProvider = "other"
)

// +kubebuilder:validation:Enum=healthy;degraded;unreachable;hibernating;unknown
type ClusterHealthStatus string

const (
	ClusterHealthy     ClusterHealthStatus = "healthy"
	ClusterDegraded    ClusterHealthStatus = "degraded"
	ClusterUnreachable ClusterHealthStatus = "unreachable"
	ClusterHibernating ClusterHealthStatus = "hibernating"
	ClusterUnknown     ClusterHealthStatus = "unknown"
)

// ---------------------------------------------------------------------------
// Spec sub-types
// ---------------------------------------------------------------------------

// ClusterCapabilities describes optional capabilities of a spoke cluster.
type ClusterCapabilities struct {
	GPUEnabled        bool  `json:"gpuEnabled,omitempty"`
	SpotEnabled       bool  `json:"spotEnabled,omitempty"`
	IstioEnabled      bool  `json:"istioEnabled,omitempty"`
	GatewayAPIEnabled bool  `json:"gatewayAPIEnabled,omitempty"`
	MaxNodes          int32 `json:"maxNodes,omitempty"`
}

// CostProfile describes the pricing of compute resources in the cluster.
type CostProfile struct {
	CoreCostPerHourUSD      string `json:"coreCostPerHourUSD,omitempty"`
	MemoryGiBCostPerHourUSD string `json:"memoryGiBCostPerHourUSD,omitempty"`
	GPUCostPerHourUSD       string `json:"gpuCostPerHourUSD,omitempty"`
	SpotDiscountPercent     int32  `json:"spotDiscountPercent,omitempty"`
}

// ClusterProfileSpec defines the desired state of a spoke cluster.
type ClusterProfileSpec struct {
	// Provider is the cloud/on-prem provider.
	Provider CloudProvider `json:"provider"`
	// Region is the geographic region of this cluster.
	Region string `json:"region"`
	// Endpoint is the gRPC dial address of the spoke agent.
	Endpoint string `json:"endpoint"`
	// Capabilities lists optional cluster features.
	Capabilities *ClusterCapabilities `json:"capabilities,omitempty"`
	// CostProfile describes compute pricing.
	CostProfile *CostProfile `json:"costProfile,omitempty"`
	// CarbonIntensityGCO2PerKWh is the regional grid carbon intensity.
	CarbonIntensityGCO2PerKWh float64 `json:"carbonIntensityGCO2PerKWh,omitempty"`
	// Labels are free-form labels for cluster selection.
	Labels map[string]string `json:"labels,omitempty"`
}

// ---------------------------------------------------------------------------
// Status sub-types
// ---------------------------------------------------------------------------

// ClusterCapacityStatus holds real-time resource usage.
type ClusterCapacityStatus struct {
	TotalCores     float64 `json:"totalCores"`
	UsedCores      float64 `json:"usedCores"`
	TotalMemoryGiB float64 `json:"totalMemoryGiB"`
	UsedMemoryGiB  float64 `json:"usedMemoryGiB"`
	NodeCount      int32   `json:"nodeCount"`
}

// ClusterProfileStatus defines the observed state of the spoke cluster.
type ClusterProfileStatus struct {
	// Health is the last-known health.
	Health ClusterHealthStatus `json:"health,omitempty"`
	// Capacity holds runtime resource metrics.
	Capacity *ClusterCapacityStatus `json:"capacity,omitempty"`
	// SLOCompliancePercent is the % of SLOs currently met.
	SLOCompliancePercent float64 `json:"sloCompliancePercent,omitempty"`
	// HourlyCostUSD is the current estimated hourly cost.
	HourlyCostUSD float64 `json:"hourlyCostUSD,omitempty"`
	// LastHeartbeat is the last time the spoke reported in.
	LastHeartbeat *metav1.Time `json:"lastHeartbeat,omitempty"`
	// Conditions provide detailed per-aspect status.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ---------------------------------------------------------------------------
// Root types
// ---------------------------------------------------------------------------

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cp
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Health",type=string,JSONPath=`.status.health`
// +kubebuilder:printcolumn:name="SLO%",type=number,JSONPath=`.status.sloCompliancePercent`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterProfile represents a spoke Kubernetes cluster registered with the hub.
type ClusterProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterProfileSpec   `json:"spec,omitempty"`
	Status ClusterProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterProfileList contains a list of ClusterProfile.
type ClusterProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterProfile{}, &ClusterProfileList{})
}
