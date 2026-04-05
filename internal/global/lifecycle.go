package global

import (
	"context"
	"fmt"
	"sync"
	"time"

	globalv1 "github.com/optipilot-ai/optipilot/api/global/v1alpha1"
	hubgrpc "github.com/optipilot-ai/optipilot/internal/global/grpc"
)

// ---------------------------------------------------------------------------
// Cluster Lifecycle Manager
// ---------------------------------------------------------------------------

// LifecycleState tracks where a cluster is in the hibernate/wake cycle.
type LifecycleState string

const (
	StateActive      LifecycleState = "active"
	StateDraining    LifecycleState = "draining"
	StateHibernating LifecycleState = "hibernating"
	StateWaking      LifecycleState = "waking"
)

// NodePoolScaler scales a cluster's node pools up or down.
type NodePoolScaler interface {
	// ScaleToZero drains workloads and scales all node pools to 0.
	ScaleToZero(ctx context.Context, clusterName string) error
	// ScaleUp restores node pools to their prior min-replica counts.
	ScaleUp(ctx context.Context, clusterName string) error
}

// WorkloadDrainer safely migrates workloads off a cluster before hibernation.
type WorkloadDrainer interface {
	// Drain cordons and evicts workloads. Returns when complete or context cancelled.
	Drain(ctx context.Context, clusterName string) error
}

// DemandForecaster predicts whether a region will need capacity soon.
type DemandForecaster interface {
	// ForecastDemand returns true if the region will need additional capacity
	// within the given lead time.
	ForecastDemand(ctx context.Context, region string, leadTime time.Duration) (bool, error)
}

// TenantLocator checks whether a cluster is the sole location for any tenant.
type TenantLocator interface {
	// IsSoleLocationForAnyTenant returns true if the cluster is the only one
	// hosting at least one tenant.
	IsSoleLocationForAnyTenant(ctx context.Context, clusterName string) (bool, error)
}

// ---------------------------------------------------------------------------
// Idle tracker — monitors consecutive idle duration per cluster
// ---------------------------------------------------------------------------

// idleRecord tracks how long a cluster has been idle.
type idleRecord struct {
	idleSince time.Time
	idle      bool
}

// ---------------------------------------------------------------------------
// Lifecycle Manager
// ---------------------------------------------------------------------------

// LifecycleManager executes hibernate and wake-up directives from the solver.
// It enforces safety guards: idle window, tenant sole-location, management cluster
// exclusion, and demand-forecast-based predictive wake-up.
type LifecycleManager struct {
	scaler     NodePoolScaler
	drainer    WorkloadDrainer
	forecaster DemandForecaster
	tenants    TenantLocator

	mu             sync.Mutex
	states         map[string]LifecycleState // cluster name → state
	idleTracker    map[string]*idleRecord
	mgmtCluster    string        // never hibernate this one
	idleWindowDur  time.Duration // consecutive idle time before hibernate
	wakeupLeadTime time.Duration // how far ahead to wake clusters
	nowFn          func() time.Time
}

// LifecycleOption configures a LifecycleManager.
type LifecycleOption func(*LifecycleManager)

// WithManagementCluster sets the name of the management cluster (never hibernated).
func WithManagementCluster(name string) LifecycleOption {
	return func(m *LifecycleManager) { m.mgmtCluster = name }
}

// WithIdleWindow overrides the default 30-minute idle window.
func WithIdleWindow(d time.Duration) LifecycleOption {
	return func(m *LifecycleManager) { m.idleWindowDur = d }
}

// WithWakeupLead overrides the default 15-minute wakeup lead time.
func WithWakeupLead(d time.Duration) LifecycleOption {
	return func(m *LifecycleManager) { m.wakeupLeadTime = d }
}

// WithLifecycleNowFn injects a clock for testing.
func WithLifecycleNowFn(fn func() time.Time) LifecycleOption {
	return func(m *LifecycleManager) { m.nowFn = fn }
}

// NewLifecycleManager creates a LifecycleManager with the given dependencies.
func NewLifecycleManager(
	scaler NodePoolScaler,
	drainer WorkloadDrainer,
	forecaster DemandForecaster,
	tenants TenantLocator,
	opts ...LifecycleOption,
) *LifecycleManager {
	m := &LifecycleManager{
		scaler:         scaler,
		drainer:        drainer,
		forecaster:     forecaster,
		tenants:        tenants,
		states:         make(map[string]LifecycleState),
		idleTracker:    make(map[string]*idleRecord),
		idleWindowDur:  30 * time.Minute,
		wakeupLeadTime: 15 * time.Minute,
		nowFn:          time.Now,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// ClusterState returns the lifecycle state of a cluster.
func (m *LifecycleManager) ClusterState(name string) LifecycleState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.states[name]; ok {
		return s
	}
	return StateActive
}

// ---------------------------------------------------------------------------
// Execute directives
// ---------------------------------------------------------------------------

// ExecuteDirective handles a single hibernate or wake_up directive.
func (m *LifecycleManager) ExecuteDirective(ctx context.Context, d hubgrpc.Directive) error {
	switch d.Type {
	case hubgrpc.DirectiveHibernate:
		return m.executeHibernate(ctx, d)
	case hubgrpc.DirectiveWakeUp:
		return m.executeWakeUp(ctx, d)
	default:
		return fmt.Errorf("lifecycle manager: unsupported directive type %q", d.Type)
	}
}

func (m *LifecycleManager) executeHibernate(ctx context.Context, d hubgrpc.Directive) error {
	name := d.ClusterName

	// Guard: never hibernate management cluster.
	if name == m.mgmtCluster {
		return fmt.Errorf("refusing to hibernate management cluster %q", name)
	}

	// Guard: check current state.
	m.mu.Lock()
	current := m.states[name]
	if current == StateHibernating || current == StateDraining {
		m.mu.Unlock()
		return nil // already in progress or complete
	}
	m.states[name] = StateDraining
	m.mu.Unlock()

	// Guard: check sole tenant location.
	if m.tenants != nil {
		sole, err := m.tenants.IsSoleLocationForAnyTenant(ctx, name)
		if err != nil {
			m.setState(name, StateActive) // revert
			return fmt.Errorf("tenant check for %s: %w", name, err)
		}
		if sole {
			m.setState(name, StateActive) // revert
			return fmt.Errorf("cluster %q is sole location for a tenant: refusing to hibernate", name)
		}
	}

	// Drain workloads.
	if m.drainer != nil {
		if err := m.drainer.Drain(ctx, name); err != nil {
			m.setState(name, StateActive) // revert
			return fmt.Errorf("drain %s: %w", name, err)
		}
	}

	// Scale to zero.
	if err := m.scaler.ScaleToZero(ctx, name); err != nil {
		m.setState(name, StateActive) // revert
		return fmt.Errorf("scale-to-zero %s: %w", name, err)
	}

	m.setState(name, StateHibernating)
	return nil
}

func (m *LifecycleManager) executeWakeUp(ctx context.Context, d hubgrpc.Directive) error {
	name := d.ClusterName

	m.mu.Lock()
	current := m.states[name]
	if current == StateActive || current == StateWaking || current == "" {
		m.mu.Unlock()
		return nil // already active or waking
	}
	m.states[name] = StateWaking
	m.mu.Unlock()

	if err := m.scaler.ScaleUp(ctx, name); err != nil {
		m.setState(name, StateHibernating) // revert
		return fmt.Errorf("scale-up %s: %w", name, err)
	}

	m.setState(name, StateActive)
	// Clear idle tracker so it doesn't immediately hibernate again.
	m.mu.Lock()
	delete(m.idleTracker, name)
	m.mu.Unlock()

	return nil
}

func (m *LifecycleManager) setState(name string, state LifecycleState) {
	m.mu.Lock()
	m.states[name] = state
	m.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Idle tracking — used to enforce the idle window
// ---------------------------------------------------------------------------

// UpdateIdleStatus should be called every heartbeat with the cluster's current
// utilization and policy. It returns true if the cluster has been idle long enough
// to be a hibernation candidate.
func (m *LifecycleManager) UpdateIdleStatus(name string, utilizationPct float64, policy *globalv1.ClusterLifecycleSpec) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	idleThreshold := float64(policy.IdleThresholdPercent)
	if idleThreshold <= 0 {
		idleThreshold = 10
	}

	idleWindow := m.idleWindowDur
	if policy.IdleWindowMinutes > 0 {
		idleWindow = time.Duration(policy.IdleWindowMinutes) * time.Minute
	}

	rec, ok := m.idleTracker[name]
	if !ok {
		rec = &idleRecord{}
		m.idleTracker[name] = rec
	}

	now := m.nowFn()

	if utilizationPct < idleThreshold {
		if !rec.idle {
			// Transition to idle.
			rec.idle = true
			rec.idleSince = now
		}
		// Check if idle long enough.
		return now.Sub(rec.idleSince) >= idleWindow
	}

	// Not idle — reset tracker.
	rec.idle = false
	rec.idleSince = time.Time{}
	return false
}

// ---------------------------------------------------------------------------
// Predictive wake-up
// ---------------------------------------------------------------------------

// CheckPredictiveWakeUp evaluates whether any hibernating clusters should be
// woken based on demand forecasts. Returns wake-up directives for clusters
// that should be restored.
func (m *LifecycleManager) CheckPredictiveWakeUp(ctx context.Context, clusters []*ClusterSnapshot) []hubgrpc.Directive {
	if m.forecaster == nil {
		return nil
	}

	m.mu.Lock()
	// Build set of hibernating clusters with their regions.
	type candidate struct {
		name   string
		region string
	}
	var candidates []candidate
	for _, c := range clusters {
		if m.states[c.Name] == StateHibernating {
			candidates = append(candidates, candidate{name: c.Name, region: c.Region})
		}
	}
	m.mu.Unlock()

	var directives []hubgrpc.Directive
	for _, cand := range candidates {
		needed, err := m.forecaster.ForecastDemand(ctx, cand.region, m.wakeupLeadTime)
		if err != nil {
			continue // skip on error, don't wake speculatively
		}
		if needed {
			directives = append(directives, hubgrpc.Directive{
				Type:            hubgrpc.DirectiveWakeUp,
				ClusterName:     cand.name,
				LifecycleAction: "wake_up",
				Reason:          fmt.Sprintf("demand forecast predicts need in region %s within %v", cand.region, m.wakeupLeadTime),
			})
		}
	}
	return directives
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// HibernatingClusters returns the names of all clusters currently hibernating.
func (m *LifecycleManager) HibernatingClusters() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for name, state := range m.states {
		if state == StateHibernating {
			out = append(out, name)
		}
	}
	return out
}

// ActiveClusters returns the names of all clusters currently active.
func (m *LifecycleManager) ActiveClusters() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for name, state := range m.states {
		if state == StateActive {
			out = append(out, name)
		}
	}
	return out
}
