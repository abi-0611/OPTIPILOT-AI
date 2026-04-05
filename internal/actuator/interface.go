package actuator

import (
	"context"
	"time"

	"github.com/optipilot-ai/optipilot/internal/engine"
)

// AnnotationPreviousState is stored on the target workload for rollback.
const AnnotationPreviousState = "optipilot.ai/previous-state"

// AnnotationPause halts all actuation when set to "true".
const AnnotationPause = "optipilot.ai/pause"

// AnnotationRestartOnTuning triggers a rolling restart when app tuning changes are applied.
const AnnotationRestartOnTuning = "optipilot.ai/restart-on-tuning"

// AnnotationVPARecommendation stores a VPA-style resource recommendation.
const AnnotationVPARecommendation = "optipilot.ai/vpa-recommendation"

// ServiceRef identifies the target workload for an actuation.
type ServiceRef struct {
	Namespace string
	Name      string
	// APIVersion and Kind of the target workload (e.g. "apps/v1", "Deployment")
	APIVersion string
	Kind       string
}

// ActuationOptions controls how an actuation is applied.
type ActuationOptions struct {
	// DryRun when true skips actual API calls but still returns what would be done.
	DryRun bool
	// Canary when true applies large changes in increments with SLO checks between.
	Canary bool
	// MaxChange is the maximum fractional change to apply in one step (0.5 = 50%).
	// 0 means no limit.
	MaxChange float64
	// CooldownSeconds from the matched policy ScalingBehavior.
	CooldownSeconds int32
}

// ChangeRecord is one atomic change made by an actuator.
type ChangeRecord struct {
	Resource  string // e.g. "Deployment/my-app"
	Field     string // e.g. "spec.replicas"
	OldValue  string
	NewValue  string
	Timestamp time.Time
}

// ActuationResult is the outcome of one Apply call.
type ActuationResult struct {
	// Applied is true if the action was actually applied (false in dry-run or no-op).
	Applied bool
	// PreviousState is a JSON snapshot of the previous resource state, used for rollback.
	PreviousState string
	// Changes lists every field that was modified.
	Changes []ChangeRecord
	// Error is non-nil if the actuation failed (partial failure may still set Applied=true).
	Error error
}

// Actuator is the common interface all actuator implementations must satisfy.
type Actuator interface {
	// Apply executes the scaling action against the target workload.
	Apply(ctx context.Context, ref ServiceRef, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, error)
	// Rollback reverts the most recent action for the given service using the
	// previous-state annotation written during Apply.
	Rollback(ctx context.Context, ref ServiceRef) error
	// CanApply returns true if this actuator knows how to handle this action type.
	CanApply(action engine.ScalingAction) bool
}

// Registry holds all registered actuators and dispatches Apply to the right one.
type Registry struct {
	actuators []Actuator
}

// NewRegistry creates an empty actuator registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds an actuator to the registry.
func (r *Registry) Register(a Actuator) {
	r.actuators = append(r.actuators, a)
}

// Apply finds the first actuator that can handle the action and applies it.
// If no actuator can handle the action type, returns a no-op result.
func (r *Registry) Apply(ctx context.Context, ref ServiceRef, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, error) {
	for _, a := range r.actuators {
		if a.CanApply(action) {
			return a.Apply(ctx, ref, action, opts)
		}
	}
	return ActuationResult{Applied: false}, nil
}

// Rollback calls Rollback on every registered actuator for the given service.
// Errors are collected but do not stop subsequent rollbacks.
func (r *Registry) Rollback(ctx context.Context, ref ServiceRef) []error {
	var errs []error
	for _, a := range r.actuators {
		if err := a.Rollback(ctx, ref); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
