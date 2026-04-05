package actuator_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/optipilot-ai/optipilot/internal/actuator"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func buildSafetyGuard(t *testing.T, objects ...client.Object) *actuator.SafetyGuard {
	t.Helper()
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
	return actuator.NewSafetyGuard(cl)
}

func makeNamespace(name string, annotations map[string]string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
		},
	}
}

func makeGlobalPauseCM(paused bool) *corev1.ConfigMap {
	val := "false"
	if paused {
		val = "true"
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "optipilot-system",
			Name:      "optipilot-pause",
		},
		Data: map[string]string{"pause": val},
	}
}

// ── Emergency stop — namespace annotation ─────────────────────────────────────

func TestSafetyGuard_EmergencyStop_NamespaceAnnotation(t *testing.T) {
	ns := makeNamespace("production", map[string]string{actuator.AnnotationPause: "true"})
	sg := buildSafetyGuard(t, ns)

	err := sg.CheckEmergencyStop(context.Background(), serviceRef("production", "api"))
	if err == nil {
		t.Error("expected ErrEmergencyStop when namespace is paused; got nil")
	}
}

func TestSafetyGuard_EmergencyStop_NamespaceNotPaused(t *testing.T) {
	ns := makeNamespace("production", nil)
	sg := buildSafetyGuard(t, ns)

	err := sg.CheckEmergencyStop(context.Background(), serviceRef("production", "api"))
	if err != nil {
		t.Errorf("unexpected error when namespace not paused: %v", err)
	}
}

// ── Emergency stop — global ConfigMap ────────────────────────────────────────

func TestSafetyGuard_EmergencyStop_GlobalConfigMap(t *testing.T) {
	ns := makeNamespace("production", nil) // no annotation
	cm := makeGlobalPauseCM(true)
	sg := buildSafetyGuard(t, ns, cm)

	err := sg.CheckEmergencyStop(context.Background(), serviceRef("production", "api"))
	if err == nil {
		t.Error("expected ErrEmergencyStop from global configmap; got nil")
	}
}

func TestSafetyGuard_EmergencyStop_GlobalConfigMap_NotPaused(t *testing.T) {
	ns := makeNamespace("production", nil)
	cm := makeGlobalPauseCM(false)
	sg := buildSafetyGuard(t, ns, cm)

	err := sg.CheckEmergencyStop(context.Background(), serviceRef("production", "api"))
	if err != nil {
		t.Errorf("unexpected error when global CM not paused: %v", err)
	}
}

func TestSafetyGuard_EmergencyStop_NoObjects_AllowsActuation(t *testing.T) {
	sg := buildSafetyGuard(t)

	// No namespace or ConfigMap → fail-open (allow actuation).
	err := sg.CheckEmergencyStop(context.Background(), serviceRef("unknown-ns", "svc"))
	if err != nil {
		t.Errorf("expected nil (no objects means not paused): %v", err)
	}
}

// ── Rate limiter / cooldown ───────────────────────────────────────────────────

func TestSafetyGuard_Cooldown_FirstActuation_Allowed(t *testing.T) {
	sg := buildSafetyGuard(t)

	err := sg.CheckCooldown(serviceRef("ns", "svc"), actuator.ActuationOptions{CooldownSeconds: 60})
	if err != nil {
		t.Errorf("first actuation should be allowed; got %v", err)
	}
}

func TestSafetyGuard_Cooldown_WithinWindow_Blocked(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sg := buildSafetyGuard(t)
	sg.SetClock(func() time.Time { return now })

	ref := serviceRef("ns", "svc")
	sg.RecordActuation(ref)

	// Advance clock 30s — within 60s cooldown.
	sg.SetClock(func() time.Time { return now.Add(30 * time.Second) })

	err := sg.CheckCooldown(ref, actuator.ActuationOptions{CooldownSeconds: 60})
	if err == nil {
		t.Error("expected ErrCooldownActive within cooldown window")
	}
}

func TestSafetyGuard_Cooldown_AfterWindow_Allowed(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sg := buildSafetyGuard(t)
	sg.SetClock(func() time.Time { return now })

	ref := serviceRef("ns", "svc")
	sg.RecordActuation(ref)

	// Advance clock beyond cooldown.
	sg.SetClock(func() time.Time { return now.Add(90 * time.Second) })

	err := sg.CheckCooldown(ref, actuator.ActuationOptions{CooldownSeconds: 60})
	if err != nil {
		t.Errorf("expected nil after cooldown expired; got %v", err)
	}
}

func TestSafetyGuard_Cooldown_ZeroSeconds_Disabled(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sg := buildSafetyGuard(t)
	sg.SetClock(func() time.Time { return now })

	ref := serviceRef("ns", "svc")
	sg.RecordActuation(ref)

	// Even immediately after actuation, CooldownSeconds=0 should not block.
	err := sg.CheckCooldown(ref, actuator.ActuationOptions{CooldownSeconds: 0})
	if err != nil {
		t.Errorf("CooldownSeconds=0 should disable rate limiter; got %v", err)
	}
}

// ── Circuit breaker ───────────────────────────────────────────────────────────

func TestSafetyGuard_CircuitBreaker_NoStrikes_Open(t *testing.T) {
	sg := buildSafetyGuard(t)

	err := sg.CheckCircuit(serviceRef("ns", "svc"))
	if err != nil {
		t.Errorf("circuit should be closed with no strikes: %v", err)
	}
}

func TestSafetyGuard_CircuitBreaker_TwoStrikes_StillClosed(t *testing.T) {
	sg := buildSafetyGuard(t)
	ref := serviceRef("ns", "svc")

	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)

	if sg.StrikeCount(ref) != 2 {
		t.Errorf("expected 2 strikes, got %d", sg.StrikeCount(ref))
	}
	if sg.IsCircuitOpen(ref) {
		t.Error("circuit opened after only 2 strikes; needs 3")
	}
}

func TestSafetyGuard_CircuitBreaker_ThreeStrikes_Opens(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sg := buildSafetyGuard(t)
	sg.SetClock(func() time.Time { return now })
	ref := serviceRef("ns", "svc")

	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded) // 3rd strike → breaker opens

	if !sg.IsCircuitOpen(ref) {
		t.Error("circuit should be open after 3 consecutive degraded outcomes")
	}
}

func TestSafetyGuard_CircuitBreaker_SuccessResetsStrikes(t *testing.T) {
	sg := buildSafetyGuard(t)
	ref := serviceRef("ns", "svc")

	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeImproved) // reset

	if sg.StrikeCount(ref) != 0 {
		t.Errorf("success should reset strikes to 0, got %d", sg.StrikeCount(ref))
	}
	if sg.IsCircuitOpen(ref) {
		t.Error("circuit should be closed after a success")
	}
}

func TestSafetyGuard_CircuitBreaker_PauseExpiry_Resets(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sg := buildSafetyGuard(t)
	sg.SetClock(func() time.Time { return now })
	ref := serviceRef("ns", "svc")

	// Open the breaker.
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)

	if !sg.IsCircuitOpen(ref) {
		t.Fatal("circuit should be open")
	}

	// Advance past the 15-min pause.
	sg.SetClock(func() time.Time { return now.Add(16 * time.Minute) })

	if sg.IsCircuitOpen(ref) {
		t.Error("circuit should auto-reset after pause duration")
	}
}

func TestSafetyGuard_CircuitBreaker_IndependentPerService(t *testing.T) {
	sg := buildSafetyGuard(t)
	refA := serviceRef("ns", "service-a")
	refB := serviceRef("ns", "service-b")

	sg.RecordOutcome(refA, actuator.OutcomeDegraded)
	sg.RecordOutcome(refA, actuator.OutcomeDegraded)
	sg.RecordOutcome(refA, actuator.OutcomeDegraded)

	if !sg.IsCircuitOpen(refA) {
		t.Error("service-a circuit should be open")
	}
	if sg.IsCircuitOpen(refB) {
		t.Error("service-b circuit should be independent (closed)")
	}
}

// ── Allow (composite check) ───────────────────────────────────────────────────

func TestSafetyGuard_Allow_BlockedByEmergencyStop(t *testing.T) {
	ns := makeNamespace("prod", map[string]string{actuator.AnnotationPause: "true"})
	sg := buildSafetyGuard(t, ns)

	err := sg.Allow(context.Background(), serviceRef("prod", "svc"), actuator.ActuationOptions{})
	if err == nil {
		t.Error("Allow should be blocked by emergency stop")
	}
}

func TestSafetyGuard_Allow_BlockedByCooldown(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sg := buildSafetyGuard(t)
	sg.SetClock(func() time.Time { return now })

	ref := serviceRef("ns", "svc")
	sg.RecordActuation(ref)

	err := sg.Allow(context.Background(), ref, actuator.ActuationOptions{CooldownSeconds: 300})
	if err == nil {
		t.Error("Allow should be blocked by cooldown")
	}
}

func TestSafetyGuard_Allow_BlockedByCircuit(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sg := buildSafetyGuard(t)
	sg.SetClock(func() time.Time { return now })
	ref := serviceRef("ns", "svc")

	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)
	sg.RecordOutcome(ref, actuator.OutcomeDegraded)

	err := sg.Allow(context.Background(), ref, actuator.ActuationOptions{})
	if err == nil {
		t.Error("Allow should be blocked by open circuit")
	}
}

func TestSafetyGuard_Allow_Permitted(t *testing.T) {
	ns := makeNamespace("ns", nil) // not paused
	sg := buildSafetyGuard(t, ns)

	err := sg.Allow(context.Background(), serviceRef("ns", "svc"), actuator.ActuationOptions{CooldownSeconds: 0})
	if err != nil {
		t.Errorf("Allow should permit actuation: %v", err)
	}
}
