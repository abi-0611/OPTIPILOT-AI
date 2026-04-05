package actuator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/optipilot-ai/optipilot/internal/actuator"
	"github.com/optipilot-ai/optipilot/internal/engine"
)

// stubActuator is a test double that records calls.
type stubActuator struct {
	canApply       bool
	applyResult    actuator.ActuationResult
	applyErr       error
	rollbackErr    error
	applyCalled    int
	rollbackCalled int
}

func (s *stubActuator) Apply(_ context.Context, _ actuator.ServiceRef, _ engine.ScalingAction, _ actuator.ActuationOptions) (actuator.ActuationResult, error) {
	s.applyCalled++
	return s.applyResult, s.applyErr
}

func (s *stubActuator) Rollback(_ context.Context, _ actuator.ServiceRef) error {
	s.rollbackCalled++
	return s.rollbackErr
}

func (s *stubActuator) CanApply(_ engine.ScalingAction) bool {
	return s.canApply
}

func ref() actuator.ServiceRef {
	return actuator.ServiceRef{
		Namespace:  "production",
		Name:       "api-server",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
	}
}

func scaleUpAction() engine.ScalingAction {
	return engine.ScalingAction{
		Type:          engine.ActionScaleUp,
		TargetReplica: 8,
		CPURequest:    0.5,
		MemoryRequest: 1.0,
		SpotRatio:     0.0,
		Confidence:    0.9,
	}
}

func TestRegistry_DispatchesToFirstCapableActuator(t *testing.T) {
	a1 := &stubActuator{canApply: false}
	a2 := &stubActuator{canApply: true, applyResult: actuator.ActuationResult{Applied: true}}
	a3 := &stubActuator{canApply: true}

	reg := actuator.NewRegistry()
	reg.Register(a1)
	reg.Register(a2)
	reg.Register(a3)

	result, err := reg.Apply(context.Background(), ref(), scaleUpAction(), actuator.ActuationOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}
	if a1.applyCalled != 0 {
		t.Error("a1 should not be called (CanApply=false)")
	}
	if a2.applyCalled != 1 {
		t.Error("a2 should be called exactly once")
	}
	if a3.applyCalled != 0 {
		t.Error("a3 should not be called (a2 handled it)")
	}
}

func TestRegistry_NoCapableActuator_ReturnsNoOp(t *testing.T) {
	a1 := &stubActuator{canApply: false}
	a2 := &stubActuator{canApply: false}

	reg := actuator.NewRegistry()
	reg.Register(a1)
	reg.Register(a2)

	result, err := reg.Apply(context.Background(), ref(), scaleUpAction(), actuator.ActuationOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Applied {
		t.Error("expected Applied=false when no actuator matches")
	}
}

func TestRegistry_EmptyRegistry_NoOp(t *testing.T) {
	reg := actuator.NewRegistry()
	result, err := reg.Apply(context.Background(), ref(), scaleUpAction(), actuator.ActuationOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Applied {
		t.Error("expected Applied=false for empty registry")
	}
}

func TestRegistry_Rollback_CallsAllActuators(t *testing.T) {
	a1 := &stubActuator{canApply: true, rollbackErr: nil}
	a2 := &stubActuator{canApply: false, rollbackErr: errors.New("rollback failed")}
	a3 := &stubActuator{canApply: true, rollbackErr: nil}

	reg := actuator.NewRegistry()
	reg.Register(a1)
	reg.Register(a2)
	reg.Register(a3)

	errs := reg.Rollback(context.Background(), ref())

	// All 3 should be called regardless of CanApply.
	if a1.rollbackCalled != 1 {
		t.Errorf("a1 rollback: %d calls, want 1", a1.rollbackCalled)
	}
	if a2.rollbackCalled != 1 {
		t.Errorf("a2 rollback: %d calls, want 1", a2.rollbackCalled)
	}
	if a3.rollbackCalled != 1 {
		t.Errorf("a3 rollback: %d calls, want 1", a3.rollbackCalled)
	}
	// One error from a2.
	if len(errs) != 1 {
		t.Errorf("expected 1 rollback error, got %d", len(errs))
	}
}

func TestRegistry_PropagatesActuatorError(t *testing.T) {
	boom := errors.New("kubernetes unavailable")
	a := &stubActuator{canApply: true, applyErr: boom}

	reg := actuator.NewRegistry()
	reg.Register(a)

	_, err := reg.Apply(context.Background(), ref(), scaleUpAction(), actuator.ActuationOptions{})
	if !errors.Is(err, boom) {
		t.Errorf("expected kubernetes unavailable error, got: %v", err)
	}
}

func TestActuationOptions_DryRunField(t *testing.T) {
	opts := actuator.ActuationOptions{DryRun: true, Canary: false, MaxChange: 0.5, CooldownSeconds: 60}
	if !opts.DryRun {
		t.Error("DryRun should be true")
	}
	if opts.MaxChange != 0.5 {
		t.Errorf("MaxChange = %.2f, want 0.5", opts.MaxChange)
	}
}

func TestServiceRef_Fields(t *testing.T) {
	r := actuator.ServiceRef{
		Namespace:  "ns",
		Name:       "svc",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
	}
	if r.Namespace != "ns" || r.Name != "svc" || r.Kind != "Deployment" {
		t.Errorf("ServiceRef fields incorrect: %+v", r)
	}
}

func TestChangeRecord_Fields(t *testing.T) {
	cr := actuator.ChangeRecord{
		Resource: "Deployment/api",
		Field:    "spec.replicas",
		OldValue: "4",
		NewValue: "8",
	}
	if cr.Field != "spec.replicas" {
		t.Errorf("ChangeRecord.Field = %q", cr.Field)
	}
	if cr.OldValue != "4" || cr.NewValue != "8" {
		t.Errorf("ChangeRecord values wrong: %+v", cr)
	}
}

func TestConstants(t *testing.T) {
	if actuator.AnnotationPreviousState != "optipilot.ai/previous-state" {
		t.Errorf("AnnotationPreviousState = %q", actuator.AnnotationPreviousState)
	}
	if actuator.AnnotationPause != "optipilot.ai/pause" {
		t.Errorf("AnnotationPause = %q", actuator.AnnotationPause)
	}
	if actuator.AnnotationRestartOnTuning != "optipilot.ai/restart-on-tuning" {
		t.Errorf("AnnotationRestartOnTuning = %q", actuator.AnnotationRestartOnTuning)
	}
}
