package tenant

import (
	"fmt"
	"sync"
	"time"
)

const (
	// NoisyGrowthThreshold is the minimum growth rate (50%) over the window
	// to classify a tenant as a noisy neighbor.
	NoisyGrowthThreshold = 0.50

	// NoisyDetectionWindow is the time window for measuring growth rate.
	NoisyDetectionWindow = 5 * time.Minute
)

// NoisyNeighborAlert describes a detected noisy-neighbor event.
type NoisyNeighborAlert struct {
	// Aggressor is the tenant whose usage grew >50% in the detection window.
	Aggressor string
	// AggressorGrowth is the fractional growth (e.g. 0.65 = 65%).
	AggressorGrowth float64
	// Victims are tenants pushed below their guaranteed share.
	Victims []string
	// Timestamp when the alert was generated.
	Timestamp time.Time
}

// usageSnapshot records a tenant's CPU usage at a point in time.
type usageSnapshot struct {
	cores float64
	at    time.Time
}

// NoisyNeighborDetector monitors rate of change of tenant resource consumption
// and detects noisy neighbors. Thread-safe.
type NoisyNeighborDetector struct {
	clock     Clock
	threshold float64
	window    time.Duration

	mu       sync.Mutex
	history  map[string][]usageSnapshot // tenant → time-ordered snapshots
	alerts   []NoisyNeighborAlert       // recent alerts (ring buffer)
	maxHist  int                        // max snapshots per tenant
	maxAlert int                        // max alerts to retain

	// Solver signals.
	noisy   map[string]time.Time // aggressor → last alert time
	victims map[string]time.Time // victim → last alert time
}

// NoisyNeighborOption configures the detector.
type NoisyNeighborOption func(*NoisyNeighborDetector)

// WithNoisyThreshold overrides the default 50% growth threshold.
func WithNoisyThreshold(t float64) NoisyNeighborOption {
	return func(d *NoisyNeighborDetector) { d.threshold = t }
}

// WithNoisyWindow overrides the default 5-minute detection window.
func WithNoisyWindow(w time.Duration) NoisyNeighborOption {
	return func(d *NoisyNeighborDetector) { d.window = w }
}

// WithNoisyClock injects a custom clock for testing.
func WithNoisyClock(c Clock) NoisyNeighborOption {
	return func(d *NoisyNeighborDetector) { d.clock = c }
}

// NewNoisyNeighborDetector creates a new detector.
func NewNoisyNeighborDetector(opts ...NoisyNeighborOption) *NoisyNeighborDetector {
	d := &NoisyNeighborDetector{
		clock:     realClock{},
		threshold: NoisyGrowthThreshold,
		window:    NoisyDetectionWindow,
		history:   make(map[string][]usageSnapshot),
		noisy:     make(map[string]time.Time),
		victims:   make(map[string]time.Time),
		maxHist:   120, // ~10 min at 5s intervals
		maxAlert:  100,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// RecordUsage records a usage snapshot for a tenant. Call on each refresh cycle.
func (d *NoisyNeighborDetector) RecordUsage(name string, cores float64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	snap := usageSnapshot{cores: cores, at: d.clock.Now()}
	h := d.history[name]
	h = append(h, snap)

	// Trim old entries beyond retention.
	if len(h) > d.maxHist {
		h = h[len(h)-d.maxHist:]
	}
	d.history[name] = h
}

// Detect checks all tenants for noisy-neighbor conditions and returns any alerts.
// guaranteedCores maps tenant name → their guaranteed CPU cores.
// currentCores maps tenant name → their current CPU usage.
func (d *NoisyNeighborDetector) Detect(guaranteedCores map[string]float64, currentCores map[string]float64) []NoisyNeighborAlert {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.clock.Now()
	cutoff := now.Add(-d.window)

	// Find aggressors: tenants whose usage grew >threshold in the window.
	type aggressor struct {
		name   string
		growth float64
	}
	var aggressors []aggressor

	for name, snapshots := range d.history {
		if len(snapshots) < 2 {
			continue
		}

		// Find the earliest snapshot within the window.
		var baseline float64
		found := false
		for _, s := range snapshots {
			if !s.at.Before(cutoff) {
				baseline = s.cores
				found = true
				break
			}
		}
		if !found {
			continue
		}

		// Latest snapshot.
		latest := snapshots[len(snapshots)-1].cores

		if baseline <= 0 {
			// Can't compute growth from zero baseline.
			continue
		}

		growth := (latest - baseline) / baseline
		if growth > d.threshold {
			aggressors = append(aggressors, aggressor{name: name, growth: growth})
		}
	}

	if len(aggressors) == 0 {
		return nil
	}

	// Find victims: tenants below their guaranteed share.
	var victimNames []string
	for name, guaranteed := range guaranteedCores {
		if guaranteed <= 0 {
			continue
		}
		current, ok := currentCores[name]
		if !ok {
			continue
		}
		if current < guaranteed {
			victimNames = append(victimNames, name)
		}
	}

	if len(victimNames) == 0 {
		return nil
	}

	// Generate alerts: each aggressor × all victims (excluding self).
	var alerts []NoisyNeighborAlert
	for _, agg := range aggressors {
		var relevantVictims []string
		for _, v := range victimNames {
			if v != agg.name {
				relevantVictims = append(relevantVictims, v)
			}
		}
		if len(relevantVictims) == 0 {
			continue
		}

		alert := NoisyNeighborAlert{
			Aggressor:       agg.name,
			AggressorGrowth: agg.growth,
			Victims:         relevantVictims,
			Timestamp:       now,
		}
		alerts = append(alerts, alert)

		// Update solver signals.
		d.noisy[agg.name] = now
		for _, v := range relevantVictims {
			d.victims[v] = now
		}
	}

	// Retain alerts.
	d.alerts = append(d.alerts, alerts...)
	if len(d.alerts) > d.maxAlert {
		d.alerts = d.alerts[len(d.alerts)-d.maxAlert:]
	}

	return alerts
}

// IsNoisy returns true if the tenant is currently flagged as a noisy neighbor.
// Signals expire after 2× the detection window.
func (d *NoisyNeighborDetector) IsNoisy(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.noisy[name]
	if !ok {
		return false
	}
	if d.clock.Now().Sub(t) > 2*d.window {
		delete(d.noisy, name)
		return false
	}
	return true
}

// IsVictim returns true if the tenant is flagged as a victim of a noisy neighbor.
// Signals expire after 2× the detection window.
func (d *NoisyNeighborDetector) IsVictim(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.victims[name]
	if !ok {
		return false
	}
	if d.clock.Now().Sub(t) > 2*d.window {
		delete(d.victims, name)
		return false
	}
	return true
}

// RecentAlerts returns the most recent alerts (up to maxAlert).
func (d *NoisyNeighborDetector) RecentAlerts() []NoisyNeighborAlert {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]NoisyNeighborAlert, len(d.alerts))
	copy(out, d.alerts)
	return out
}

// FormatAlert returns a human-readable string for an alert.
func FormatAlert(a NoisyNeighborAlert) string {
	return fmt.Sprintf("NoisyNeighbor: tenant %q grew %.0f%% in detection window, impacting victims %v",
		a.Aggressor, a.AggressorGrowth*100, a.Victims)
}
