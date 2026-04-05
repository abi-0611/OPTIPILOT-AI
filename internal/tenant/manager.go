package tenant

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/metrics"
)

// TenantState holds the current tracked state for a single tenant.
type TenantState struct {
	Name       string
	Tier       string
	Weight     int32
	Namespaces []string

	// Current usage from Prometheus.
	CurrentCores     float64
	CurrentMemoryGiB float64
	CurrentCostUSD   float64

	// Hard budget limits from TenantBudgets.
	MaxCores          int32
	MaxMemoryGiB      int32
	MaxMonthlyCostUSD float64

	// Fair-share policy from spec.
	GuaranteedCoresPercent int32
	Burstable              bool
	MaxBurstPercent        int32

	// Computed values (set by fair-share algorithm / refresh).
	FairnessScore    float64
	AllocationStatus string // guaranteed|bursting|throttled|under_allocated

	// Tracking.
	LastRefreshed time.Time
}

// Clock abstracts time for testing.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ManagerOption configures the Manager.
type ManagerOption func(*Manager)

// WithClock injects a custom clock for testing.
func WithClock(c Clock) ManagerOption {
	return func(m *Manager) { m.clock = c }
}

// WithRefreshInterval overrides the default 30s polling interval.
func WithRefreshInterval(d time.Duration) ManagerOption {
	return func(m *Manager) { m.interval = d }
}

// Manager tracks per-tenant resource usage by querying Prometheus.
// It is designed as a plain struct with a background goroutine (manager.Runnable),
// not a Kubebuilder reconciler.
type Manager struct {
	prom     metrics.PrometheusClient
	clock    Clock
	interval time.Duration

	mu       sync.RWMutex
	tenants  map[string]*TenantState
	profiles map[string]tenantv1alpha1.TenantProfile
}

// NewManager creates a new tenant Manager.
func NewManager(prom metrics.PrometheusClient, opts ...ManagerOption) *Manager {
	m := &Manager{
		prom:     prom,
		clock:    realClock{},
		interval: 30 * time.Second,
		tenants:  make(map[string]*TenantState),
		profiles: make(map[string]tenantv1alpha1.TenantProfile),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// UpdateProfiles replaces the tracked tenant set from TenantProfile CRs.
// It adds new tenants, updates existing ones, and removes stale entries.
func (m *Manager) UpdateProfiles(profiles []tenantv1alpha1.TenantProfile) {
	m.mu.Lock()
	defer m.mu.Unlock()

	incoming := make(map[string]tenantv1alpha1.TenantProfile, len(profiles))
	for _, p := range profiles {
		incoming[p.Name] = p
	}

	// Remove tenants no longer present.
	for name := range m.tenants {
		if _, ok := incoming[name]; !ok {
			delete(m.tenants, name)
		}
	}

	// Add or update each profile.
	for name, p := range incoming {
		if _, exists := m.tenants[name]; !exists {
			m.tenants[name] = &TenantState{}
		}
		s := m.tenants[name]
		s.Name = name
		s.Tier = string(p.Spec.Tier)
		s.Weight = p.Spec.Weight
		s.Namespaces = append([]string(nil), p.Spec.Namespaces...)

		if p.Spec.Budgets != nil {
			s.MaxCores = p.Spec.Budgets.MaxCores
			s.MaxMemoryGiB = p.Spec.Budgets.MaxMemoryGiB
			if p.Spec.Budgets.MaxMonthlyCostUSD != "" {
				if v, err := strconv.ParseFloat(p.Spec.Budgets.MaxMonthlyCostUSD, 64); err == nil {
					s.MaxMonthlyCostUSD = v
				}
			}
		} else {
			s.MaxCores = 0
			s.MaxMemoryGiB = 0
			s.MaxMonthlyCostUSD = 0
		}

		if p.Spec.FairSharePolicy != nil {
			s.GuaranteedCoresPercent = p.Spec.FairSharePolicy.GuaranteedCoresPercent
			s.Burstable = p.Spec.FairSharePolicy.Burstable
			s.MaxBurstPercent = p.Spec.FairSharePolicy.MaxBurstPercent
		} else {
			s.GuaranteedCoresPercent = 0
			s.Burstable = false
			s.MaxBurstPercent = 0
		}
	}

	m.profiles = incoming
}

// GetState returns a copy of the current state for a tenant, or nil if not tracked.
func (m *Manager) GetState(name string) *TenantState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.tenants[name]
	if !ok {
		return nil
	}
	return copyState(s)
}

// GetAllStates returns a snapshot of all tenant states.
func (m *Manager) GetAllStates() map[string]*TenantState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]*TenantState, len(m.tenants))
	for name, s := range m.tenants {
		out[name] = copyState(s)
	}
	return out
}

// TenantCount returns the number of tracked tenants.
func (m *Manager) TenantCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tenants)
}

// Refresh queries Prometheus and updates all tenant usage states.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.RLock()
	type entry struct {
		name       string
		namespaces []string
	}
	entries := make([]entry, 0, len(m.tenants))
	for name, s := range m.tenants {
		entries = append(entries, entry{name: name, namespaces: s.Namespaces})
	}
	m.mu.RUnlock()

	var errs []string
	for _, e := range entries {
		if len(e.namespaces) == 0 {
			continue
		}

		nsRegex := strings.Join(e.namespaces, "|")
		cpuQuery := fmt.Sprintf(`sum(namespace_cpu_usage_seconds_total{namespace=~"%s"})`, nsRegex)
		memQuery := fmt.Sprintf(`sum(namespace_memory_working_set_bytes{namespace=~"%s"})`, nsRegex)
		costQuery := fmt.Sprintf(`sum(namespace_cost_per_hour{namespace=~"%s"})`, nsRegex)

		cpuCores, err := m.prom.Query(ctx, cpuQuery)
		if err != nil {
			errs = append(errs, fmt.Sprintf("cpu query for %s: %v", e.name, err))
			cpuCores = 0
		}

		memBytes, err := m.prom.Query(ctx, memQuery)
		if err != nil {
			errs = append(errs, fmt.Sprintf("memory query for %s: %v", e.name, err))
			memBytes = 0
		}

		costUSD, err := m.prom.Query(ctx, costQuery)
		if err != nil {
			errs = append(errs, fmt.Sprintf("cost query for %s: %v", e.name, err))
			costUSD = 0
		}

		memGiB := memBytes / (1024 * 1024 * 1024)

		m.mu.Lock()
		if s, ok := m.tenants[e.name]; ok {
			s.CurrentCores = cpuCores
			s.CurrentMemoryGiB = memGiB
			s.CurrentCostUSD = costUSD
			s.LastRefreshed = m.clock.Now()
		}
		m.mu.Unlock()
	}

	if len(errs) > 0 {
		return fmt.Errorf("refresh errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Start implements manager.Runnable. It runs the refresh loop until ctx is cancelled.
func (m *Manager) Start(ctx context.Context) error {
	logger := ctrl.Log.WithName("tenant-manager")
	logger.Info("starting tenant manager", "interval", m.interval.String())

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	// Initial refresh immediately.
	if err := m.Refresh(ctx); err != nil {
		logger.Error(err, "initial refresh failed")
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("tenant manager stopped")
			return nil
		case <-ticker.C:
			if err := m.Refresh(ctx); err != nil {
				logger.Error(err, "refresh cycle failed")
			}
		}
	}
}

// copyState returns a deep copy of a TenantState.
func copyState(s *TenantState) *TenantState {
	c := *s
	c.Namespaces = make([]string, len(s.Namespaces))
	copy(c.Namespaces, s.Namespaces)
	return &c
}
