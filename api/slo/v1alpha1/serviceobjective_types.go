package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceObjectiveSpec defines the desired SLO state for a service
type ServiceObjectiveSpec struct {
	// TargetRef points to the workload this SLO applies to
	TargetRef CrossVersionObjectReference `json:"targetRef"`

	// Objectives defines the SLO targets
	// +kubebuilder:validation:MinItems=1
	Objectives []Objective `json:"objectives"`

	// ErrorBudget defines the total error budget and burn rate alerts
	// +optional
	ErrorBudget *ErrorBudget `json:"errorBudget,omitempty"`

	// CostConstraint defines maximum acceptable cost
	// +optional
	CostConstraint *CostConstraint `json:"costConstraint,omitempty"`

	// TenantRef references the TenantProfile this service belongs to
	// +optional
	TenantRef *TenantReference `json:"tenantRef,omitempty"`

	// EvaluationInterval is how often to evaluate this SLO (default: 30s)
	// +optional
	// +kubebuilder:default="30s"
	EvaluationInterval string `json:"evaluationInterval,omitempty"`

	// CustomMetrics allows injecting arbitrary Prometheus metrics into evaluation
	// +optional
	CustomMetrics []CustomMetric `json:"customMetrics,omitempty"`
}

// CrossVersionObjectReference contains enough information to let you identify another resource
type CrossVersionObjectReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

// MetricType defines supported SLO metric types
// +kubebuilder:validation:Enum=latency_p50;latency_p90;latency_p95;latency_p99;error_rate;availability;throughput;custom
type MetricType string

const (
	MetricLatencyP50   MetricType = "latency_p50"
	MetricLatencyP90   MetricType = "latency_p90"
	MetricLatencyP95   MetricType = "latency_p95"
	MetricLatencyP99   MetricType = "latency_p99"
	MetricErrorRate    MetricType = "error_rate"
	MetricAvailability MetricType = "availability"
	MetricThroughput   MetricType = "throughput"
	MetricCustom       MetricType = "custom"
)

// Objective defines a single SLO target
type Objective struct {
	// Metric is the type of SLO metric
	Metric MetricType `json:"metric"`

	// Target is the SLO target value (e.g., "200ms", "0.1%", "99.95%", "1000rps")
	Target string `json:"target"`

	// Window is the evaluation window for this objective
	// +kubebuilder:default="5m"
	Window string `json:"window,omitempty"`

	// CustomQuery is a PromQL query (required when metric is "custom")
	// +optional
	CustomQuery string `json:"customQuery,omitempty"`
}

// ErrorBudget defines the total error budget and burn rate alerting config
type ErrorBudget struct {
	// Total error budget as a percentage (e.g., "0.05%")
	Total string `json:"total"`

	// BurnRateAlerts defines multi-window burn rate alert thresholds
	// +optional
	BurnRateAlerts []BurnRateAlert `json:"burnRateAlerts,omitempty"`
}

// BurnRateAlert defines a multi-window burn rate alert
type BurnRateAlert struct {
	// +kubebuilder:validation:Enum=info;warning;critical
	Severity    string  `json:"severity"`
	ShortWindow string  `json:"shortWindow"`
	LongWindow  string  `json:"longWindow"`
	Factor      float64 `json:"factor"`
}

// CostConstraint defines a maximum acceptable cost ceiling
type CostConstraint struct {
	// MaxHourlyCostUSD is the maximum acceptable hourly cost
	MaxHourlyCostUSD string `json:"maxHourlyCostUSD"`
}

// TenantReference is a lightweight reference to a TenantProfile
type TenantReference struct {
	Name string `json:"name"`
}

// CustomMetric defines an arbitrary Prometheus metric injected into evaluation
type CustomMetric struct {
	Name   string  `json:"name"`
	Query  string  `json:"query"`
	Target float64 `json:"target"`
	Weight float64 `json:"weight"`
}

// ServiceObjectiveStatus defines the observed state of ServiceObjective
type ServiceObjectiveStatus struct {
	// CurrentBurn maps objective metric names to current burn rates
	// +optional
	CurrentBurn map[string]string `json:"currentBurn,omitempty"`

	// BudgetRemaining is the remaining error budget as a percentage
	// +optional
	BudgetRemaining string `json:"budgetRemaining,omitempty"`

	// LastEvaluation is the timestamp of the last evaluation
	// +optional
	LastEvaluation *metav1.Time `json:"lastEvaluation,omitempty"`

	// Conditions represent the latest available observations
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="Budget",type=string,JSONPath=`.status.budgetRemaining`
// +kubebuilder:printcolumn:name="Compliant",type=string,JSONPath=`.status.conditions[?(@.type=="SLOCompliant")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ServiceObjective is the Schema for the serviceobjectives API
type ServiceObjective struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceObjectiveSpec   `json:"spec,omitempty"`
	Status            ServiceObjectiveStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceObjectiveList contains a list of ServiceObjective
type ServiceObjectiveList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceObjective `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceObjective{}, &ServiceObjectiveList{})
}
