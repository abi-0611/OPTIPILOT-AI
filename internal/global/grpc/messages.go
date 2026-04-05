package hubgrpc

import "time"

// ---------------------------------------------------------------------------
// Registration & Heartbeat messages
// ---------------------------------------------------------------------------

// RegisterClusterRequest is sent by a spoke agent to register with the hub.
type RegisterClusterRequest struct {
	ClusterName         string            `json:"clusterName"`
	Provider            string            `json:"provider"`
	Region              string            `json:"region"`
	Endpoint            string            `json:"endpoint"`
	CarbonIntensityGCO2 float64           `json:"carbonIntensityGCO2"`
	Labels              map[string]string `json:"labels,omitempty"`
	Capabilities        *Capabilities     `json:"capabilities,omitempty"`
	CostProfile         *CostProfileMsg   `json:"costProfile,omitempty"`
}

// Capabilities mirrors ClusterCapabilities from the CRD.
type Capabilities struct {
	GPUEnabled        bool  `json:"gpuEnabled"`
	SpotEnabled       bool  `json:"spotEnabled"`
	IstioEnabled      bool  `json:"istioEnabled"`
	GatewayAPIEnabled bool  `json:"gatewayAPIEnabled"`
	MaxNodes          int32 `json:"maxNodes"`
}

// CostProfileMsg mirrors CostProfile from the CRD.
type CostProfileMsg struct {
	CoreCostPerHourUSD      string `json:"coreCostPerHourUSD"`
	MemoryGiBCostPerHourUSD string `json:"memoryGiBCostPerHourUSD"`
	SpotDiscountPercent     int32  `json:"spotDiscountPercent"`
}

// RegisterClusterResponse is returned by the hub after registration.
type RegisterClusterResponse struct {
	Accepted           bool   `json:"accepted"`
	Message            string `json:"message,omitempty"`
	HeartbeatIntervalS int32  `json:"heartbeatIntervalS"`
}

// ---------------------------------------------------------------------------
// Status reporting
// ---------------------------------------------------------------------------

// ClusterStatusReport is sent periodically by spokes (heartbeat).
type ClusterStatusReport struct {
	ClusterName          string    `json:"clusterName"`
	TotalCores           float64   `json:"totalCores"`
	UsedCores            float64   `json:"usedCores"`
	TotalMemoryGiB       float64   `json:"totalMemoryGiB"`
	UsedMemoryGiB        float64   `json:"usedMemoryGiB"`
	NodeCount            int32     `json:"nodeCount"`
	SLOCompliancePercent float64   `json:"sloCompliancePercent"`
	HourlyCostUSD        float64   `json:"hourlyCostUSD"`
	Health               string    `json:"health"`
	Timestamp            time.Time `json:"timestamp"`
}

// StatusAck is the hub's response to a status report.
type StatusAck struct {
	Received bool   `json:"received"`
	Message  string `json:"message,omitempty"`
}

// ---------------------------------------------------------------------------
// Directives (hub → spoke)
// ---------------------------------------------------------------------------

// Directive is a command from the hub to a spoke cluster.
type Directive struct {
	ID              string           `json:"id"`
	Type            DirectiveType    `json:"type"`
	ClusterName     string           `json:"clusterName"`
	TrafficWeights  map[string]int32 `json:"trafficWeights,omitempty"`
	MigrationHints  []MigrationHint  `json:"migrationHints,omitempty"`
	LifecycleAction string           `json:"lifecycleAction,omitempty"`
	Reason          string           `json:"reason,omitempty"`
}

// DirectiveType classifies the kind of directive.
type DirectiveType string

const (
	DirectiveTrafficShift DirectiveType = "traffic_shift"
	DirectiveMigration    DirectiveType = "migration"
	DirectiveHibernate    DirectiveType = "hibernate"
	DirectiveWakeUp       DirectiveType = "wake_up"
	DirectiveNoOp         DirectiveType = "noop"
)

// MigrationHint suggests moving a workload between clusters.
type MigrationHint struct {
	Namespace   string `json:"namespace"`
	Workload    string `json:"workload"`
	FromCluster string `json:"fromCluster"`
	ToCluster   string `json:"toCluster"`
	Reason      string `json:"reason"`
}

// GetDirectiveRequest is sent by a spoke to ask the hub for pending directives.
type GetDirectiveRequest struct {
	ClusterName string `json:"clusterName"`
}

// GetDirectiveResponse wraps the hub's response.
type GetDirectiveResponse struct {
	Directives []Directive `json:"directives"`
}

// ---------------------------------------------------------------------------
// Traffic shift request (spoke → hub)
// ---------------------------------------------------------------------------

// TrafficShiftRequest is sent by a spoke to request traffic redistribution.
type TrafficShiftRequest struct {
	ClusterName string           `json:"clusterName"`
	Service     string           `json:"service"`
	Weights     map[string]int32 `json:"weights"`
	Reason      string           `json:"reason"`
}

// TrafficShiftResponse is the hub's acknowledgement.
type TrafficShiftResponse struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message,omitempty"`
}
