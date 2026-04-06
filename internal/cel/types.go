package cel

// CandidatePlan represents a proposed scaling action evaluated by CEL constraints.
type CandidatePlan struct {
	Replicas        int64    `cel:"replicas"`
	CPURequest      float64  `cel:"cpu_request"`    // cores
	MemoryRequest   float64  `cel:"memory_request"` // GiB
	SpotRatio       float64  `cel:"spot_ratio"`     // 0.0 to 1.0
	SpotCount       int64    `cel:"spot_count"`
	OnDemandCount   int64    `cel:"on_demand_count"`
	InstanceTypes   []string `cel:"instance_types"`
	EstimatedCost   float64  `cel:"estimated_cost"`   // hourly USD
	EstimatedCarbon float64  `cel:"estimated_carbon"` // gCO2/hr
}

// CurrentState represents the current observed state of a workload.
type CurrentState struct {
	Replicas      int64   `cel:"replicas"`
	CPURequest    float64 `cel:"cpu_request"`
	MemoryRequest float64 `cel:"memory_request"`
	CPUUsage      float64 `cel:"cpu_usage"`
	MemoryUsage   float64 `cel:"memory_usage"`
	SpotRatio     float64 `cel:"spot_ratio"`
	HourlyCost    float64 `cel:"hourly_cost"`
}

// SLOStatus represents current SLO compliance state.
type SLOStatus struct {
	Compliant       bool    `cel:"compliant"`
	BurnRate        float64 `cel:"burn_rate"`
	BudgetRemaining float64 `cel:"budget_remaining"` // 0.0 to 1.0
	LatencyP99      float64 `cel:"latency_p99"`      // seconds
	ErrorRate       float64 `cel:"error_rate"`       // ratio
	Availability    float64 `cel:"availability"`     // ratio
	Throughput      float64 `cel:"throughput"`       // rps
}

// TenantStatus represents tenant allocation state.
type TenantStatus struct {
	Name              string  `cel:"name"`
	Tier              string  `cel:"tier"`
	Weight            int64   `cel:"weight"`
	CurrentCores      float64 `cel:"current_cores"`
	GuaranteedCores   float64 `cel:"guaranteed_cores"`
	MaxCores          float64 `cel:"max_cores"`
	BudgetUsedPercent float64 `cel:"budget_used_percent"`
	FairnessScore     float64 `cel:"fairness_score"`
}

// ForecastResult represents predicted future demand.
type ForecastResult struct {
	PredictedRPS     float64 `json:"predictedRPS" cel:"predicted_rps"`
	PredictedLatency float64 `json:"predictedLatency,omitempty" cel:"predicted_latency"`
	ChangePercent    float64 `json:"changePercent" cel:"change_percent"` // vs current
	Confidence       float64 `json:"confidence" cel:"confidence"`
	SpotRiskScore    float64 `json:"spotRiskScore,omitempty" cel:"spot_risk_score"` // 0.0 to 1.0
}

// ClusterState represents overall cluster state.
type ClusterState struct {
	TotalCores     float64 `cel:"total_cores"`
	UsedCores      float64 `cel:"used_cores"`
	TotalMemoryGiB float64 `cel:"total_memory_gib"`
	UsedMemoryGiB  float64 `cel:"used_memory_gib"`
	NodeCount      int64   `cel:"node_count"`
	SpotNodeCount  int64   `cel:"spot_node_count"`
	Region         string  `cel:"region"`
	Provider       string  `cel:"provider"`
}
