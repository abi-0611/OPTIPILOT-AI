// Package forecaster provides a Go HTTP client for the OptiPilot ML service.
//
// AccuracyTracker records predicted-vs-actual values per service and computes
// MAE and MAPE over a sliding window.  If MAPE exceeds 30 % for 1 hour,
// the tracker marks that service in fallback mode so the solver skips
// pre-warming candidates.
package forecaster

import (
	"math"
	"sync"
	"time"
)

// ── thresholds ────────────────────────────────────────────────────────────────

const (
	// MAPEThreshold is the MAPE (%) above which fallback is considered.
	MAPEThreshold = 30.0

	// FallbackSustainDuration is how long MAPE must stay above threshold
	// before pre-warming is disabled.
	FallbackSustainDuration = 1 * time.Hour

	// accuracyWindowMax is the max number of records kept per service.
	accuracyWindowMax = 200
)

// ── types ─────────────────────────────────────────────────────────────────────

type accuracyRecord struct {
	ts        time.Time
	predicted float64
	actual    float64
}

type serviceAccuracy struct {
	records       []accuracyRecord
	mae           float64
	mape          float64
	fallback      bool
	highMAPESince time.Time // zero = not exceeded
}

// AccuracyTracker tracks forecast accuracy per service.
type AccuracyTracker struct {
	mu       sync.Mutex
	services map[string]*serviceAccuracy
	nowFn    func() time.Time

	threshold float64
	sustain   time.Duration
	windowMax int
}

// AccuracyOption configures an AccuracyTracker.
type AccuracyOption func(*AccuracyTracker)

// WithMAPEThreshold overrides the default 30% MAPE threshold.
func WithMAPEThreshold(pct float64) AccuracyOption {
	return func(t *AccuracyTracker) { t.threshold = pct }
}

// WithSustainDuration overrides the default 1-hour sustain duration.
func WithSustainDuration(d time.Duration) AccuracyOption {
	return func(t *AccuracyTracker) { t.sustain = d }
}

// WithWindowMax overrides the default record window cap.
func WithWindowMax(n int) AccuracyOption {
	return func(t *AccuracyTracker) { t.windowMax = n }
}

// NewAccuracyTracker creates a tracker with the given options.
func NewAccuracyTracker(opts ...AccuracyOption) *AccuracyTracker {
	t := &AccuracyTracker{
		services:  make(map[string]*serviceAccuracy),
		nowFn:     time.Now,
		threshold: MAPEThreshold,
		sustain:   FallbackSustainDuration,
		windowMax: accuracyWindowMax,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// SetNowFn injects a clock function (for tests).
func (t *AccuracyTracker) SetNowFn(fn func() time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nowFn = fn
}

// ── public API ────────────────────────────────────────────────────────────────

// Record stores a predicted-vs-actual pair for a service and recomputes metrics.
func (t *AccuracyTracker) Record(service string, predicted, actual float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.nowFn()
	sa := t.getOrCreate(service)

	sa.records = append(sa.records, accuracyRecord{ts: now, predicted: predicted, actual: actual})

	// Evict oldest if over cap.
	if len(sa.records) > t.windowMax {
		sa.records = sa.records[len(sa.records)-t.windowMax:]
	}

	t.recompute(sa, now)
}

// MAE returns the latest MAE for the service (0 if no data).
func (t *AccuracyTracker) MAE(service string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sa, ok := t.services[service]; ok {
		return sa.mae
	}
	return 0
}

// MAPE returns the latest MAPE (%) for the service (0 if no data).
func (t *AccuracyTracker) MAPE(service string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sa, ok := t.services[service]; ok {
		return sa.mape
	}
	return 0
}

// IsFallbackActive returns true if pre-warming should be disabled for the service.
func (t *AccuracyTracker) IsFallbackActive(service string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sa, ok := t.services[service]; ok {
		return sa.fallback
	}
	return false
}

// Services returns all tracked service names.
func (t *AccuracyTracker) Services() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	names := make([]string, 0, len(t.services))
	for name := range t.services {
		names = append(names, name)
	}
	return names
}

// Reset clears all records for a service and deactivates fallback.
func (t *AccuracyTracker) Reset(service string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.services, service)
}

// ── internals ─────────────────────────────────────────────────────────────────

func (t *AccuracyTracker) getOrCreate(service string) *serviceAccuracy {
	sa, ok := t.services[service]
	if !ok {
		sa = &serviceAccuracy{}
		t.services[service] = sa
	}
	return sa
}

func (t *AccuracyTracker) recompute(sa *serviceAccuracy, now time.Time) {
	n := len(sa.records)
	if n == 0 {
		sa.mae = 0
		sa.mape = 0
		sa.fallback = false
		sa.highMAPESince = time.Time{}
		return
	}

	var totalAE, totalAPE float64
	apeCount := 0

	for _, r := range sa.records {
		ae := math.Abs(r.predicted - r.actual)
		totalAE += ae
		if r.actual != 0 {
			totalAPE += ae / math.Abs(r.actual) * 100.0
			apeCount++
		}
	}

	sa.mae = totalAE / float64(n)
	if apeCount > 0 {
		sa.mape = totalAPE / float64(apeCount)
	} else {
		sa.mape = 0
	}

	// Fallback logic.
	if sa.mape > t.threshold {
		if sa.highMAPESince.IsZero() {
			sa.highMAPESince = now
		}
		if now.Sub(sa.highMAPESince) >= t.sustain {
			sa.fallback = true
		}
	} else {
		sa.highMAPESince = time.Time{}
		sa.fallback = false
	}
}
