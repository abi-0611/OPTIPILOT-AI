package actuator

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GlobalPauseConfigMap is the namespace/name of the global emergency-stop ConfigMap.
// When its data key "pause" equals "true", all actuation is halted cluster-wide.
const GlobalPauseConfigMap = "optipilot-system/optipilot-pause"

// circuitBreakerMaxStrikes is the number of consecutive SLO-degrading actuations
// before the circuit breaker opens (pauses actuation for a service).
const circuitBreakerMaxStrikes = 3

// circuitBreakerPauseDuration is how long the circuit breaker keeps a service paused.
const circuitBreakerPauseDuration = 15 * time.Minute

// ActuationOutcome describes whether a recent actuation improved or degraded SLO.
type ActuationOutcome int

const (
	// OutcomeImproved means the actuation improved the SLO.
	OutcomeImproved ActuationOutcome = iota
	// OutcomeDegraded means the actuation caused SLO degradation.
	OutcomeDegraded
)

// circuitEntry tracks state per service for the circuit breaker.
type circuitEntry struct {
	consecutiveStrikes int
	pausedUntil        time.Time
}

// cooldownEntry tracks the timestamp of the last applied actuation per service.
type cooldownEntry struct {
	lastActuated time.Time
}

// SafetyGuard wraps actuations with:
//   - Emergency stop: checks AnnotationPause annotation on Namespace and a global ConfigMap
//   - Rate limiter:   enforces CooldownSeconds between actuations per service
//   - Circuit breaker: pauses a service for circuitBreakerPauseDuration after
//     circuitBreakerMaxStrikes consecutive SLO-degrading actuations
type SafetyGuard struct {
	Client client.Client

	mu        sync.Mutex
	circuits  map[string]*circuitEntry  // keyed by "namespace/name"
	cooldowns map[string]*cooldownEntry // keyed by "namespace/name"

	// nowFn allows tests to inject a fake clock.
	nowFn func() time.Time
}

// NewSafetyGuard creates a SafetyGuard.
func NewSafetyGuard(c client.Client) *SafetyGuard {
	return &SafetyGuard{
		Client:    c,
		circuits:  make(map[string]*circuitEntry),
		cooldowns: make(map[string]*cooldownEntry),
		nowFn:     time.Now,
	}
}

// SetClock replaces the internal clock function. Use in tests only.
func (s *SafetyGuard) SetClock(fn func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nowFn = fn
}

// serviceKey returns the map key for a ServiceRef.
func serviceKey(ref ServiceRef) string {
	return ref.Namespace + "/" + ref.Name
}

// ──────────────────────────────────────────────────────────────────────────────
// Emergency stop
// ──────────────────────────────────────────────────────────────────────────────

// ErrEmergencyStop is returned when actuation is paused by an emergency-stop signal.
var ErrEmergencyStop = fmt.Errorf("actuation paused: emergency stop active")

// ErrCooldownActive is returned when the cooldown window has not expired.
var ErrCooldownActive = fmt.Errorf("actuation paused: cooldown period not elapsed")

// ErrCircuitOpen is returned when the circuit breaker is open for a service.
var ErrCircuitOpen = fmt.Errorf("actuation paused: circuit breaker open (too many SLO degradations)")

// CheckEmergencyStop returns ErrEmergencyStop if:
//   - The Namespace has annotation optipilot.ai/pause = "true", OR
//   - The global pause ConfigMap has data["pause"] = "true"
func (s *SafetyGuard) CheckEmergencyStop(ctx context.Context, ref ServiceRef) error {
	// 1. Check namespace annotation.
	var ns corev1.Namespace
	if err := s.Client.Get(ctx, types.NamespacedName{Name: ref.Namespace}, &ns); err == nil {
		if ns.Annotations[AnnotationPause] == "true" {
			return ErrEmergencyStop
		}
	}

	// 2. Check global pause ConfigMap.
	parts := splitNamespacedName(GlobalPauseConfigMap)
	var cm corev1.ConfigMap
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: parts[0], Name: parts[1]}, &cm); err == nil {
		if cm.Data["pause"] == "true" {
			return ErrEmergencyStop
		}
	}

	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Rate limiter
// ──────────────────────────────────────────────────────────────────────────────

// CheckCooldown returns ErrCooldownActive if the service was actuated more recently
// than opts.CooldownSeconds ago. CooldownSeconds=0 disables the check.
func (s *SafetyGuard) CheckCooldown(ref ServiceRef, opts ActuationOptions) error {
	if opts.CooldownSeconds <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := serviceKey(ref)
	entry, ok := s.cooldowns[key]
	if !ok {
		return nil
	}

	minGap := time.Duration(opts.CooldownSeconds) * time.Second
	if s.nowFn().Sub(entry.lastActuated) < minGap {
		return ErrCooldownActive
	}
	return nil
}

// RecordActuation stamps the last-actuation time for the service.
// Must be called after a successful (non-dry-run) actuation.
func (s *SafetyGuard) RecordActuation(ref ServiceRef) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := serviceKey(ref)
	s.cooldowns[key] = &cooldownEntry{lastActuated: s.nowFn()}
}

// ──────────────────────────────────────────────────────────────────────────────
// Circuit breaker
// ──────────────────────────────────────────────────────────────────────────────

// CheckCircuit returns ErrCircuitOpen if the circuit breaker is open for the service.
func (s *SafetyGuard) CheckCircuit(ref ServiceRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := serviceKey(ref)
	entry, ok := s.circuits[key]
	if !ok {
		return nil
	}

	if entry.consecutiveStrikes >= circuitBreakerMaxStrikes {
		if s.nowFn().Before(entry.pausedUntil) {
			return ErrCircuitOpen
		}
		// Pause duration expired — reset the breaker.
		delete(s.circuits, key)
	}
	return nil
}

// RecordOutcome updates the circuit breaker state for the service.
// Call this after verifying post-actuation SLO status.
func (s *SafetyGuard) RecordOutcome(ref ServiceRef, outcome ActuationOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := serviceKey(ref)

	if outcome == OutcomeImproved {
		// Any success resets the strike counter.
		delete(s.circuits, key)
		return
	}

	// OutcomeDegraded: increment strikes.
	entry, ok := s.circuits[key]
	if !ok {
		entry = &circuitEntry{}
		s.circuits[key] = entry
	}
	entry.consecutiveStrikes++

	if entry.consecutiveStrikes >= circuitBreakerMaxStrikes {
		entry.pausedUntil = s.nowFn().Add(circuitBreakerPauseDuration)
	}
}

// StrikeCount returns the current consecutive-degradation count for a service.
// Primarily for testing and observability.
func (s *SafetyGuard) StrikeCount(ref ServiceRef) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.circuits[serviceKey(ref)]; ok {
		return entry.consecutiveStrikes
	}
	return 0
}

// IsCircuitOpen returns true if the circuit breaker is currently open (paused) for the service.
func (s *SafetyGuard) IsCircuitOpen(ref ServiceRef) bool {
	return s.CheckCircuit(ref) == ErrCircuitOpen
}

// ──────────────────────────────────────────────────────────────────────────────
// Composite check
// ──────────────────────────────────────────────────────────────────────────────

// Allow runs all three safety checks in order.
// Returns nil if actuation is permitted, or the first blocking error.
func (s *SafetyGuard) Allow(ctx context.Context, ref ServiceRef, opts ActuationOptions) error {
	if err := s.CheckEmergencyStop(ctx, ref); err != nil {
		return err
	}
	if err := s.CheckCooldown(ref, opts); err != nil {
		return err
	}
	if err := s.CheckCircuit(ref); err != nil {
		return err
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Private helpers
// ──────────────────────────────────────────────────────────────────────────────

// splitNamespacedName splits "namespace/name" into [namespace, name].
// Falls back to ["default", input] if no slash.
func splitNamespacedName(s string) [2]string {
	for i, c := range s {
		if c == '/' {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{"default", s}
}
