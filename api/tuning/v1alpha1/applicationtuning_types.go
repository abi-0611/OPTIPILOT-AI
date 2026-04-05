package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

// ParameterType classifies the data type of a tunable parameter.
// +kubebuilder:validation:Enum=integer;float;string
type ParameterType string

const (
	ParamTypeInteger ParameterType = "integer"
	ParamTypeFloat   ParameterType = "float"
	ParamTypeString  ParameterType = "string"
)

// ParameterSource specifies where the parameter value is stored.
// +kubebuilder:validation:Enum=configmap;env
type ParameterSource string

const (
	SourceConfigMap ParameterSource = "configmap"
	SourceEnv       ParameterSource = "env"
)

// TuningPhase describes the current lifecycle phase of an ApplicationTuning resource.
type TuningPhase string

const (
	TuningIdle       TuningPhase = "Idle"
	TuningExploring  TuningPhase = "Exploring"
	TuningObserving  TuningPhase = "Observing"
	TuningConverged  TuningPhase = "Converged"
	TuningRolledBack TuningPhase = "RolledBack"
	TuningPaused     TuningPhase = "Paused"
	TuningError      TuningPhase = "Error"
)

// ---------------------------------------------------------------------------
// Supporting types
// ---------------------------------------------------------------------------

// ConfigMapRef identifies a key in a specific ConfigMap.
type ConfigMapRef struct {
	// Name of the ConfigMap.
	Name string `json:"name"`

	// Namespace of the ConfigMap. Defaults to the ApplicationTuning namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Key inside the ConfigMap data map.
	Key string `json:"key"`
}

// TunableParameter defines one parameter that the optimizer can adjust.
type TunableParameter struct {
	// Name is a unique identifier for this parameter within the spec.
	Name string `json:"name"`

	// Type is the data type of the parameter value.
	Type ParameterType `json:"type"`

	// Source indicates where the parameter value lives (ConfigMap or env var).
	Source ParameterSource `json:"source"`

	// ConfigMapRef identifies the ConfigMap key. Required when Source=configmap.
	// +optional
	ConfigMapRef *ConfigMapRef `json:"configMapRef,omitempty"`

	// EnvVarName is the environment variable name. Required when Source=env.
	// +optional
	EnvVarName string `json:"envVarName,omitempty"`

	// Min is the lower bound of the parameter range (numeric types).
	// +optional
	Min string `json:"min,omitempty"`

	// Max is the upper bound of the parameter range (numeric types).
	// +optional
	Max string `json:"max,omitempty"`

	// Step is the grid increment for numeric exploration.
	// +optional
	Step string `json:"step,omitempty"`

	// AllowedValues lists permitted values for string-type parameters.
	// +optional
	AllowedValues []string `json:"allowedValues,omitempty"`

	// Default is the initial value before any optimization.
	// +optional
	Default string `json:"default,omitempty"`
}

// OptimizationTarget describes the metric the optimizer aims to improve.
type OptimizationTarget struct {
	// MetricName is the SLO metric to optimize.
	MetricName string `json:"metricName"`

	// PromQLExpr is an optional raw PromQL expression to query.
	// +optional
	PromQLExpr string `json:"promQLExpr,omitempty"`

	// Objective is "minimize" or "maximize".
	// +kubebuilder:default="minimize"
	// +kubebuilder:validation:Enum=minimize;maximize
	Objective string `json:"objective"`
}

// TuningSafetyPolicy controls how aggressively the optimizer may change parameters.
type TuningSafetyPolicy struct {
	// MaxChangePercent caps the magnitude of each parameter change (default 50).
	// +kubebuilder:default=50
	MaxChangePercent int32 `json:"maxChangePercent,omitempty"`

	// CooldownMinutes is the minimum wait between successive changes (default 5).
	// +kubebuilder:default=5
	CooldownMinutes int32 `json:"cooldownMinutes,omitempty"`

	// RollbackOnSLOViolation triggers automatic rollback when SLO degrades (default true).
	// +kubebuilder:default=true
	RollbackOnSLOViolation bool `json:"rollbackOnSLOViolation,omitempty"`

	// SLOThresholdPercent is the minimum acceptable SLO compliance (default 95.0).
	SLOThresholdPercent float64 `json:"sloThresholdPercent,omitempty"`
}

// TuningTargetRef identifies the workload being tuned.
type TuningTargetRef struct {
	// APIVersion of the target resource (e.g. "apps/v1").
	APIVersion string `json:"apiVersion"`

	// Kind of the target resource (e.g. "Deployment").
	Kind string `json:"kind"`

	// Name of the target resource.
	Name string `json:"name"`

	// Namespace of the target resource. Defaults to the CR namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ParameterObservation records one SLO measurement for a specific parameter value.
type ParameterObservation struct {
	// ParameterName matches TunableParameter.Name.
	ParameterName string `json:"parameterName"`

	// Value is the parameter value at the time of observation.
	Value string `json:"value"`

	// SLOValue is the SLO compliance measurement (0.0–100.0).
	SLOValue float64 `json:"sloValue"`

	// ObservedAt is when this observation was recorded.
	ObservedAt metav1.Time `json:"observedAt"`
}

// ---------------------------------------------------------------------------
// Spec / Status / Root types
// ---------------------------------------------------------------------------

// ApplicationTuningSpec defines the desired tuning configuration.
type ApplicationTuningSpec struct {
	// TargetRef identifies the workload to tune.
	TargetRef TuningTargetRef `json:"targetRef"`

	// Parameters lists the tunable parameters (at least one required).
	// +kubebuilder:validation:MinItems=1
	Parameters []TunableParameter `json:"parameters"`

	// OptimizationTarget describes what the optimizer is trying to improve.
	OptimizationTarget OptimizationTarget `json:"optimizationTarget"`

	// SafetyPolicy controls change magnitude and cooldown.
	// +optional
	SafetyPolicy *TuningSafetyPolicy `json:"safetyPolicy,omitempty"`

	// OptimizationIntervalMinutes is how often to run an optimization cycle (default 60).
	// +kubebuilder:default=60
	OptimizationIntervalMinutes int32 `json:"optimizationIntervalMinutes,omitempty"`

	// MaxObservations caps the observation history per parameter (default 100).
	// +kubebuilder:default=100
	MaxObservations int32 `json:"maxObservations,omitempty"`

	// Paused stops optimization when true.
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// ApplicationTuningStatus defines the observed tuning state.
type ApplicationTuningStatus struct {
	// Phase is the current lifecycle phase.
	Phase TuningPhase `json:"phase,omitempty"`

	// CurrentValues maps parameter name → current applied value.
	// +optional
	CurrentValues map[string]string `json:"currentValues,omitempty"`

	// BestValues maps parameter name → best-known value.
	// +optional
	BestValues map[string]string `json:"bestValues,omitempty"`

	// BestSLOValue is the SLO measurement at the optimal parameter set.
	BestSLOValue float64 `json:"bestSLOValue,omitempty"`

	// ActiveParameter is the parameter currently being tuned.
	// +optional
	ActiveParameter string `json:"activeParameter,omitempty"`

	// Observations stores parameter → SLO correlation data.
	// +optional
	Observations []ParameterObservation `json:"observations,omitempty"`

	// LastOptimizationTime records when the last cycle ran.
	// +optional
	LastOptimizationTime *metav1.Time `json:"lastOptimizationTime,omitempty"`

	// NextOptimizationTime indicates the scheduled next cycle.
	// +optional
	NextOptimizationTime *metav1.Time `json:"nextOptimizationTime,omitempty"`

	// CooldownUntil is when the current cooldown expires.
	// +optional
	CooldownUntil *metav1.Time `json:"cooldownUntil,omitempty"`

	// Message is a human-readable status message.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=optipilot
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Best SLO",type=number,JSONPath=`.status.bestSLOValue`
// +kubebuilder:printcolumn:name="Active Param",type=string,JSONPath=`.status.activeParameter`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ApplicationTuning is the Schema for the applicationtunings API.
type ApplicationTuning struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApplicationTuningSpec   `json:"spec,omitempty"`
	Status ApplicationTuningStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ApplicationTuningList contains a list of ApplicationTuning.
type ApplicationTuningList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApplicationTuning `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ApplicationTuning{}, &ApplicationTuningList{})
}
