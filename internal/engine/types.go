package engine

import (
	"time"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
)

// ActionType classifies the kind of scaling action.
type ActionType string

const (
	ActionScaleUp   ActionType = "scale_up"
	ActionScaleDown ActionType = "scale_down"
	ActionTune      ActionType = "tune"
	ActionNoAction  ActionType = "no_action"
)

// SolverInput aggregates all context the solver needs for one optimization cycle
// on a single ServiceObjective.
type SolverInput struct {
	// Service identification
	Namespace string
	Service   string // name of the target workload

	// Current workload state
	Current cel.CurrentState

	// SLO compliance snapshot
	SLO cel.SLOStatus

	// Matching policies (sorted by priority desc)
	Policies []MatchedPolicy

	// Tenant status (nil if no tenant)
	Tenant *cel.TenantStatus

	// Forecast (nil if not available)
	Forecast *cel.ForecastResult

	// Arbitrary metrics map[name]value — exposed as "metrics" in CEL
	Metrics map[string]float64

	// Cluster-level state
	Cluster cel.ClusterState

	// Instance types available for spot/on-demand mix
	InstanceTypes []string

	// Region for cost/carbon lookups
	Region string

	// Right-sized recommendation (nil if not available)
	RightSizedCPU    *float64
	RightSizedMemory *float64

	// Trigger describes why this cycle was initiated
	Trigger string
}

// MatchedPolicy is a policy that matched a ServiceObjective, along with its CEL cache key.
type MatchedPolicy struct {
	Policy policyv1alpha1.OptimizationPolicy
	Key    string // PolicyKey (UID/Generation)
}

// ScalingAction is the output of the solver — what to do next.
type ScalingAction struct {
	Type          ActionType        `json:"type"`
	TargetReplica int32             `json:"targetReplica"`
	CPURequest    float64           `json:"cpuRequest"`    // cores
	MemoryRequest float64           `json:"memoryRequest"` // GiB
	SpotRatio     float64           `json:"spotRatio"`
	DryRun        bool              `json:"dryRun"`
	Reason        string            `json:"reason"`
	Confidence    float64           `json:"confidence"`   // 0.0 to 1.0
	TuningParams  map[string]string `json:"tuningParams"` // app-level key=value tuning overrides
}

// CandidateScore holds per-dimension scores for a single candidate.
type CandidateScore struct {
	SLO      float64 `json:"slo"`      // 0–1, higher = better SLO compliance
	Cost     float64 `json:"cost"`     // 0–1, higher = cheaper
	Carbon   float64 `json:"carbon"`   // 0–1, higher = greener
	Fairness float64 `json:"fairness"` // 0–1, higher = fairer
	Weighted float64 `json:"weighted"` // policy-weighted aggregate
}

// ConstraintResult records the outcome of evaluating a single CEL constraint.
type ConstraintResult struct {
	Expr       string `json:"expr"`
	Reason     string `json:"reason"`
	Hard       bool   `json:"hard"`
	Passed     bool   `json:"passed"`
	PolicyName string `json:"policyName"`
}

// ScoredCandidate is a candidate plan enriched with scores and constraint outcomes.
type ScoredCandidate struct {
	Plan        cel.CandidatePlan  `json:"plan"`
	Score       CandidateScore     `json:"score"`
	Constraints []ConstraintResult `json:"constraints"`
	Viable      bool               `json:"viable"` // true if all hard constraints passed
}

// DecisionRecord captures the complete causal chain for one optimization decision.
// Stored in the Decision Journal for explainability.
type DecisionRecord struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Namespace string    `json:"namespace"`
	Service   string    `json:"service"`
	Trigger   string    `json:"trigger"`

	// Input snapshot
	CurrentState  cel.CurrentState    `json:"currentState"`
	SLOStatus     cel.SLOStatus       `json:"sloStatus"`
	TenantStatus  *cel.TenantStatus   `json:"tenantStatus,omitempty"`
	ForecastState *cel.ForecastResult `json:"forecastState,omitempty"`
	ClusterState  cel.ClusterState    `json:"clusterState"`
	Metrics       map[string]float64  `json:"metrics,omitempty"`

	// All candidates evaluated
	Candidates []ScoredCandidate `json:"candidates"`

	// Pareto front (subset of Candidates)
	ParetoFront []ScoredCandidate `json:"paretoFront"`

	// Selected action
	SelectedAction ScalingAction `json:"selectedAction"`

	// Policy info
	PolicyNames      []string           `json:"policyNames"`
	ObjectiveWeights map[string]float64 `json:"objectiveWeights"`

	// Outcome
	ActionType ActionType `json:"actionType"`
	DryRun     bool       `json:"dryRun"`
	Confidence float64    `json:"confidence"` // 0.0 to 1.0
}
