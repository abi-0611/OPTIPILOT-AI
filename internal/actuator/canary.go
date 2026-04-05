package actuator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/optipilot-ai/optipilot/internal/engine"
)

// canaryThreshold is the fractional change above which canary mode splits the
// actuation into two steps.
const canaryThreshold = 0.5

// autoRollbackDelay is how long after actuation the background goroutine waits
// before checking SLO health.
const autoRollbackDelay = 5 * time.Minute

// SLOChecker is a narrow interface for canary/rollback to check post-actuation SLO health.
// Implement this with a real SLO evaluator in production and a stub in tests.
type SLOChecker interface {
	// IsHealthy returns true if the service's SLO is currently compliant.
	IsHealthy(ctx context.Context, namespace, name string) (bool, error)
}

// CanaryController handles:
//  1. Step-wise application of large scaling changes (>50% in a single step → two 50% steps)
//  2. Background auto-rollback: 5 minutes after actuation, check SLO; revert if degraded
//
// It is NOT an Actuator itself — it wraps a Registry to orchestrate multi-step actuations.
type CanaryController struct {
	Registry *Registry
	Checker  SLOChecker

	// stepDelay is the pause between canary steps. Defaults to 2 minutes.
	// Tests can override via SetStepDelay.
	stepDelay time.Duration

	// sleepFn is injectable for testing (replaces time.Sleep).
	sleepFn func(time.Duration)

	mu       sync.Mutex
	watchers map[string]context.CancelFunc // keyed by "namespace/name"
}

// NewCanaryController creates a CanaryController.
func NewCanaryController(registry *Registry, checker SLOChecker) *CanaryController {
	return &CanaryController{
		Registry:  registry,
		Checker:   checker,
		stepDelay: 2 * time.Minute,
		sleepFn:   time.Sleep,
		watchers:  make(map[string]context.CancelFunc),
	}
}

// SetStepDelay overrides the inter-step pause. Use in tests.
func (c *CanaryController) SetStepDelay(d time.Duration) { c.stepDelay = d }

// SetSleepFn replaces time.Sleep. Use in tests.
func (c *CanaryController) SetSleepFn(fn func(time.Duration)) { c.sleepFn = fn }

// ──────────────────────────────────────────────────────────────────────────────
// Apply (canary-aware)
// ──────────────────────────────────────────────────────────────────────────────

// Apply applies the scaling action. If opts.Canary is true AND the fractional
// change exceeds canaryThreshold, it applies the action in two half-steps with
// an SLO check between them.
//
// After a successful (non-dry-run) apply, it starts a background goroutine to
// automatically roll back if SLO degrades within autoRollbackDelay.
func (c *CanaryController) Apply(
	ctx context.Context,
	ref ServiceRef,
	action engine.ScalingAction,
	opts ActuationOptions,
	currentReplicas int32,
) (ActuationResult, error) {

	if !opts.Canary || !isLargeChange(currentReplicas, action.TargetReplica) {
		// Standard single-shot apply.
		result, err := c.Registry.Apply(ctx, ref, action, opts)
		if err != nil {
			return result, err
		}
		if result.Applied && !opts.DryRun {
			c.startAutoRollbackWatcher(ref)
		}
		return result, nil
	}

	// ── Canary two-step ──────────────────────────────────────────────────────
	// Step 1: apply half the change.
	step1 := halfStep(currentReplicas, action.TargetReplica)
	step1Action := action
	step1Action.TargetReplica = step1

	result1, err := c.Registry.Apply(ctx, ref, step1Action, opts)
	if err != nil {
		return result1, fmt.Errorf("canary step 1: %w", err)
	}
	if !result1.Applied {
		return result1, nil
	}

	// Wait the inter-step delay.
	c.sleepFn(c.stepDelay)

	// SLO check between steps.
	if c.Checker != nil {
		healthy, err := c.Checker.IsHealthy(ctx, ref.Namespace, ref.Name)
		if err != nil || !healthy {
			// Step 1 degraded SLO — roll back step 1 and abort.
			_ = c.Registry.Rollback(ctx, ref)
			reason := "SLO degraded after canary step 1"
			if err != nil {
				reason = fmt.Sprintf("SLO check failed: %v", err)
			}
			return ActuationResult{
				Applied:       false,
				PreviousState: result1.PreviousState,
				Changes:       result1.Changes,
				Error:         fmt.Errorf("%s — rolled back", reason),
			}, nil
		}
	}

	// Step 2: apply the remainder to reach final target.
	step2Action := action
	step2Action.TargetReplica = action.TargetReplica

	result2, err := c.Registry.Apply(ctx, ref, step2Action, opts)
	if err != nil {
		return result2, fmt.Errorf("canary step 2: %w", err)
	}

	merged := ActuationResult{
		Applied:       result2.Applied,
		PreviousState: result1.PreviousState, // keep original state for rollback
		Changes:       append(result1.Changes, result2.Changes...),
	}

	if merged.Applied && !opts.DryRun {
		c.startAutoRollbackWatcher(ref)
	}
	return merged, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Rollback
// ──────────────────────────────────────────────────────────────────────────────

// Rollback cancels any pending auto-rollback watcher and immediately rolls back
// all actuators for the service.
func (c *CanaryController) Rollback(ctx context.Context, ref ServiceRef) []error {
	c.cancelWatcher(ref)
	return c.Registry.Rollback(ctx, ref)
}

// ──────────────────────────────────────────────────────────────────────────────
// Auto-rollback watcher
// ──────────────────────────────────────────────────────────────────────────────

// startAutoRollbackWatcher starts a background goroutine that checks SLO health
// after autoRollbackDelay and triggers Rollback if the service is unhealthy.
func (c *CanaryController) startAutoRollbackWatcher(ref ServiceRef) {
	if c.Checker == nil {
		return
	}

	// Cancel any previous watcher for this service.
	c.cancelWatcher(ref)

	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.watchers[serviceKey(ref)] = cancel
	c.mu.Unlock()

	go func() {
		defer cancel()
		c.sleepFn(autoRollbackDelay)

		select {
		case <-ctx.Done():
			return // was cancelled (e.g. a new action was applied)
		default:
		}

		healthy, err := c.Checker.IsHealthy(ctx, ref.Namespace, ref.Name)
		if err != nil || !healthy {
			_ = c.Registry.Rollback(ctx, ref)
		}
	}()
}

// cancelWatcher stops any active auto-rollback goroutine for the service.
func (c *CanaryController) cancelWatcher(ref ServiceRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := serviceKey(ref)
	if cancel, ok := c.watchers[key]; ok {
		cancel()
		delete(c.watchers, key)
	}
}

// WatcherActive returns true if a background watcher is running for the service.
// For testing and observability only.
func (c *CanaryController) WatcherActive(ref ServiceRef) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.watchers[serviceKey(ref)]
	return ok
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// isLargeChange returns true if the delta between current and target exceeds
// canaryThreshold as a fraction of current (avoiding division by zero).
func isLargeChange(current, target int32) bool {
	if current == 0 {
		return false
	}
	delta := target - current
	if delta < 0 {
		delta = -delta
	}
	return float64(delta)/float64(current) > canaryThreshold
}

// halfStep returns the replica count halfway between current and target,
// rounded toward target (ceiling for scale-up, floor for scale-down).
func halfStep(current, target int32) int32 {
	if target > current {
		return current + (target-current+1)/2
	}
	return current - (current-target+1)/2
}
