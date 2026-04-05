package global

import (
	"context"
	"fmt"
	"testing"
	"time"

	globalv1 "github.com/optipilot-ai/optipilot/api/global/v1alpha1"
	hubgrpc "github.com/optipilot-ai/optipilot/internal/global/grpc"
)

// ---------------------------------------------------------------------------
// Test fakes
// ---------------------------------------------------------------------------

type fakeScaler struct {
	scaledToZero []string
	scaledUp     []string
	scaleErr     error
}

func (f *fakeScaler) ScaleToZero(_ context.Context, name string) error {
	if f.scaleErr != nil {
		return f.scaleErr
	}
	f.scaledToZero = append(f.scaledToZero, name)
	return nil
}

func (f *fakeScaler) ScaleUp(_ context.Context, name string) error {
	if f.scaleErr != nil {
		return f.scaleErr
	}
	f.scaledUp = append(f.scaledUp, name)
	return nil
}

type fakeDrainer struct {
	drained  []string
	drainErr error
}

func (f *fakeDrainer) Drain(_ context.Context, name string) error {
	if f.drainErr != nil {
		return f.drainErr
	}
	f.drained = append(f.drained, name)
	return nil
}

type fakeForecaster struct {
	forecasts map[string]bool
	err       error
}

func (f *fakeForecaster) ForecastDemand(_ context.Context, region string, _ time.Duration) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.forecasts[region], nil
}

type fakeTenantLocator struct {
	soleLocations map[string]bool
	err           error
}

func (f *fakeTenantLocator) IsSoleLocationForAnyTenant(_ context.Context, name string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.soleLocations[name], nil
}

func newTestManager(opts ...LifecycleOption) (*LifecycleManager, *fakeScaler, *fakeDrainer) {
	scaler := &fakeScaler{}
	drainer := &fakeDrainer{}
	forecaster := &fakeForecaster{}
	tenants := &fakeTenantLocator{}
	m := NewLifecycleManager(scaler, drainer, forecaster, tenants, opts...)
	return m, scaler, drainer
}

// ---------------------------------------------------------------------------
// Tests: Hibernate execution
// ---------------------------------------------------------------------------

func TestExecuteHibernate_Success(t *testing.T) {
	m, scaler, drainer := newTestManager()

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "cluster-a",
	}

	if err := m.ExecuteDirective(context.Background(), d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(drainer.drained) != 1 || drainer.drained[0] != "cluster-a" {
		t.Errorf("expected drain of cluster-a, got %v", drainer.drained)
	}
	if len(scaler.scaledToZero) != 1 || scaler.scaledToZero[0] != "cluster-a" {
		t.Errorf("expected scale-to-zero of cluster-a, got %v", scaler.scaledToZero)
	}
	if m.ClusterState("cluster-a") != StateHibernating {
		t.Errorf("expected state Hibernating, got %s", m.ClusterState("cluster-a"))
	}
}

func TestExecuteHibernate_ManagementClusterRefused(t *testing.T) {
	m, _, _ := newTestManager(WithManagementCluster("hub-cluster"))

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "hub-cluster",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err == nil {
		t.Fatal("expected error for management cluster")
	}
}

func TestExecuteHibernate_SoleTenantRefused(t *testing.T) {
	scaler := &fakeScaler{}
	drainer := &fakeDrainer{}
	tenants := &fakeTenantLocator{soleLocations: map[string]bool{"cluster-a": true}}
	m := NewLifecycleManager(scaler, drainer, nil, tenants)

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "cluster-a",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err == nil {
		t.Fatal("expected error for sole tenant location")
	}
	if m.ClusterState("cluster-a") != StateActive {
		t.Errorf("expected state reverted to Active, got %s", m.ClusterState("cluster-a"))
	}
}

func TestExecuteHibernate_DrainError_Reverts(t *testing.T) {
	scaler := &fakeScaler{}
	drainer := &fakeDrainer{drainErr: fmt.Errorf("drain timeout")}
	m := NewLifecycleManager(scaler, drainer, nil, nil)

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "cluster-a",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err == nil {
		t.Fatal("expected drain error")
	}
	if m.ClusterState("cluster-a") != StateActive {
		t.Errorf("state should revert to Active after drain failure")
	}
}

func TestExecuteHibernate_ScaleError_Reverts(t *testing.T) {
	scaler := &fakeScaler{scaleErr: fmt.Errorf("API unavailable")}
	drainer := &fakeDrainer{}
	m := NewLifecycleManager(scaler, drainer, nil, nil)

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "cluster-a",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err == nil {
		t.Fatal("expected scale error")
	}
	if m.ClusterState("cluster-a") != StateActive {
		t.Errorf("state should revert to Active after scale failure")
	}
}

func TestExecuteHibernate_AlreadyHibernating_NoOp(t *testing.T) {
	m, scaler, _ := newTestManager()
	m.setState("cluster-a", StateHibernating)

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "cluster-a",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scaler.scaledToZero) != 0 {
		t.Error("should not scale again if already hibernating")
	}
}

// ---------------------------------------------------------------------------
// Tests: Wake-up execution
// ---------------------------------------------------------------------------

func TestExecuteWakeUp_Success(t *testing.T) {
	m, scaler, _ := newTestManager()
	m.setState("cluster-a", StateHibernating)

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveWakeUp,
		ClusterName: "cluster-a",
	}

	if err := m.ExecuteDirective(context.Background(), d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(scaler.scaledUp) != 1 || scaler.scaledUp[0] != "cluster-a" {
		t.Errorf("expected scale-up of cluster-a, got %v", scaler.scaledUp)
	}
	if m.ClusterState("cluster-a") != StateActive {
		t.Errorf("expected state Active after wake, got %s", m.ClusterState("cluster-a"))
	}
}

func TestExecuteWakeUp_AlreadyActive_NoOp(t *testing.T) {
	m, scaler, _ := newTestManager()
	// State defaults to Active.

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveWakeUp,
		ClusterName: "cluster-a",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scaler.scaledUp) != 0 {
		t.Error("should not scale if already active")
	}
}

func TestExecuteWakeUp_ScaleError_Reverts(t *testing.T) {
	scaler := &fakeScaler{scaleErr: fmt.Errorf("quota exceeded")}
	m := NewLifecycleManager(scaler, nil, nil, nil)
	m.setState("cluster-a", StateHibernating)

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveWakeUp,
		ClusterName: "cluster-a",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err == nil {
		t.Fatal("expected scale error")
	}
	if m.ClusterState("cluster-a") != StateHibernating {
		t.Errorf("state should revert to Hibernating, got %s", m.ClusterState("cluster-a"))
	}
}

// ---------------------------------------------------------------------------
// Tests: Unsupported directive
// ---------------------------------------------------------------------------

func TestExecuteDirective_UnsupportedType(t *testing.T) {
	m, _, _ := newTestManager()

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveTrafficShift,
		ClusterName: "cluster-a",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err == nil {
		t.Fatal("expected error for unsupported directive type")
	}
}

// ---------------------------------------------------------------------------
// Tests: Idle tracking
// ---------------------------------------------------------------------------

func TestUpdateIdleStatus_BelowThreshold_BecomesIdle(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m, _, _ := newTestManager(WithLifecycleNowFn(func() time.Time { return now }))

	policy := &globalv1.ClusterLifecycleSpec{
		IdleThresholdPercent: 10,
		IdleWindowMinutes:    30,
	}

	// First call: not idle long enough.
	if m.UpdateIdleStatus("cluster-a", 5.0, policy) {
		t.Error("should not be ready immediately")
	}

	// After 29 minutes: still not ready.
	now = now.Add(29 * time.Minute)
	if m.UpdateIdleStatus("cluster-a", 5.0, policy) {
		t.Error("should not be ready before 30 minutes")
	}

	// After 31 minutes total: ready.
	now = now.Add(2 * time.Minute)
	if !m.UpdateIdleStatus("cluster-a", 5.0, policy) {
		t.Error("should be ready after 31 minutes idle")
	}
}

func TestUpdateIdleStatus_AboveThreshold_ResetsTimer(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m, _, _ := newTestManager(WithLifecycleNowFn(func() time.Time { return now }))

	policy := &globalv1.ClusterLifecycleSpec{
		IdleThresholdPercent: 10,
		IdleWindowMinutes:    30,
	}

	// Start idle.
	m.UpdateIdleStatus("cluster-a", 5.0, policy)

	// Jump 15 minutes and go above threshold.
	now = now.Add(15 * time.Minute)
	m.UpdateIdleStatus("cluster-a", 50.0, policy)

	// Back to idle, timer should reset.
	now = now.Add(1 * time.Minute)
	m.UpdateIdleStatus("cluster-a", 5.0, policy)

	// After 29 more minutes: not ready (timer reset after activity spike).
	now = now.Add(29 * time.Minute)
	if m.UpdateIdleStatus("cluster-a", 5.0, policy) {
		t.Error("should not be ready, timer was reset")
	}

	// 1 more minute: ready.
	now = now.Add(1 * time.Minute)
	if !m.UpdateIdleStatus("cluster-a", 5.0, policy) {
		t.Error("should be ready after full 30 min idle window since reset")
	}
}

func TestUpdateIdleStatus_DefaultThresholds(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m, _, _ := newTestManager(
		WithLifecycleNowFn(func() time.Time { return now }),
		WithIdleWindow(30*time.Minute),
	)

	// Zero values = defaults: threshold 10%, window 30m.
	policy := &globalv1.ClusterLifecycleSpec{}

	m.UpdateIdleStatus("c", 5.0, policy)
	now = now.Add(31 * time.Minute)
	if !m.UpdateIdleStatus("c", 5.0, policy) {
		t.Error("should be ready with default 30m window and 10% threshold")
	}
}

// ---------------------------------------------------------------------------
// Tests: Predictive wake-up
// ---------------------------------------------------------------------------

func TestCheckPredictiveWakeUp_ForecastNeed(t *testing.T) {
	forecaster := &fakeForecaster{
		forecasts: map[string]bool{"us-east-1": true},
	}
	m := NewLifecycleManager(&fakeScaler{}, nil, forecaster, nil,
		WithWakeupLead(15*time.Minute))
	m.setState("cluster-a", StateHibernating)

	clusters := []*ClusterSnapshot{
		{Name: "cluster-a", Region: "us-east-1"},
		{Name: "cluster-b", Region: "eu-west-1"},
	}

	directives := m.CheckPredictiveWakeUp(context.Background(), clusters)
	if len(directives) != 1 {
		t.Fatalf("expected 1 wake-up directive, got %d", len(directives))
	}
	if directives[0].ClusterName != "cluster-a" {
		t.Errorf("expected cluster-a, got %s", directives[0].ClusterName)
	}
	if directives[0].Type != hubgrpc.DirectiveWakeUp {
		t.Errorf("expected wake_up type, got %s", directives[0].Type)
	}
}

func TestCheckPredictiveWakeUp_NoNeed(t *testing.T) {
	forecaster := &fakeForecaster{
		forecasts: map[string]bool{"us-east-1": false},
	}
	m := NewLifecycleManager(&fakeScaler{}, nil, forecaster, nil)
	m.setState("cluster-a", StateHibernating)

	clusters := []*ClusterSnapshot{
		{Name: "cluster-a", Region: "us-east-1"},
	}

	directives := m.CheckPredictiveWakeUp(context.Background(), clusters)
	if len(directives) != 0 {
		t.Errorf("expected no directives, got %d", len(directives))
	}
}

func TestCheckPredictiveWakeUp_SkipsActiveClusters(t *testing.T) {
	forecaster := &fakeForecaster{
		forecasts: map[string]bool{"us-east-1": true},
	}
	m := NewLifecycleManager(&fakeScaler{}, nil, forecaster, nil)
	// cluster-a is active — should not be woken.

	clusters := []*ClusterSnapshot{
		{Name: "cluster-a", Region: "us-east-1"},
	}

	directives := m.CheckPredictiveWakeUp(context.Background(), clusters)
	if len(directives) != 0 {
		t.Errorf("should not wake active clusters, got %d directives", len(directives))
	}
}

func TestCheckPredictiveWakeUp_NilForecaster(t *testing.T) {
	m := NewLifecycleManager(&fakeScaler{}, nil, nil, nil)
	m.setState("cluster-a", StateHibernating)

	directives := m.CheckPredictiveWakeUp(context.Background(), []*ClusterSnapshot{
		{Name: "cluster-a", Region: "us-east-1"},
	})
	if directives != nil {
		t.Error("expected nil with nil forecaster")
	}
}

func TestCheckPredictiveWakeUp_ForecasterError_Skips(t *testing.T) {
	forecaster := &fakeForecaster{err: fmt.Errorf("network error")}
	m := NewLifecycleManager(&fakeScaler{}, nil, forecaster, nil)
	m.setState("cluster-a", StateHibernating)

	directives := m.CheckPredictiveWakeUp(context.Background(), []*ClusterSnapshot{
		{Name: "cluster-a", Region: "us-east-1"},
	})
	if len(directives) != 0 {
		t.Errorf("should skip on error, got %d directives", len(directives))
	}
}

// ---------------------------------------------------------------------------
// Tests: State helpers
// ---------------------------------------------------------------------------

func TestHibernatingClusters(t *testing.T) {
	m, _, _ := newTestManager()
	m.setState("a", StateHibernating)
	m.setState("b", StateActive)
	m.setState("c", StateHibernating)

	got := m.HibernatingClusters()
	if len(got) != 2 {
		t.Errorf("expected 2 hibernating, got %d", len(got))
	}
}

func TestActiveClusters(t *testing.T) {
	m, _, _ := newTestManager()
	m.setState("a", StateActive)
	m.setState("b", StateHibernating)
	m.setState("c", StateActive)

	got := m.ActiveClusters()
	if len(got) != 2 {
		t.Errorf("expected 2 active, got %d", len(got))
	}
}

func TestDefaultClusterState_IsActive(t *testing.T) {
	m, _, _ := newTestManager()
	if m.ClusterState("unknown") != StateActive {
		t.Errorf("default state should be Active")
	}
}

func TestWakeUpClearsIdleTracker(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m, _, _ := newTestManager(WithLifecycleNowFn(func() time.Time { return now }))

	policy := &globalv1.ClusterLifecycleSpec{
		IdleThresholdPercent: 10,
		IdleWindowMinutes:    30,
	}

	// Make cluster idle for 31 minutes.
	m.UpdateIdleStatus("cluster-a", 5.0, policy)
	now = now.Add(31 * time.Minute)
	if !m.UpdateIdleStatus("cluster-a", 5.0, policy) {
		t.Fatal("should be idle candidate")
	}

	// Hibernate then wake up.
	m.setState("cluster-a", StateHibernating)
	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveWakeUp,
		ClusterName: "cluster-a",
	}
	if err := m.ExecuteDirective(context.Background(), d); err != nil {
		t.Fatalf("wake error: %v", err)
	}

	// After wake-up, idle tracker should be cleared — not immediately idle.
	if m.UpdateIdleStatus("cluster-a", 5.0, policy) {
		t.Error("idle tracker should have been cleared after wake-up")
	}
}

func TestLifecycleOptions(t *testing.T) {
	m := NewLifecycleManager(&fakeScaler{}, nil, nil, nil,
		WithManagementCluster("mgmt"),
		WithIdleWindow(45*time.Minute),
		WithWakeupLead(20*time.Minute),
	)
	if m.mgmtCluster != "mgmt" {
		t.Errorf("expected mgmt, got %s", m.mgmtCluster)
	}
	if m.idleWindowDur != 45*time.Minute {
		t.Errorf("expected 45m, got %v", m.idleWindowDur)
	}
	if m.wakeupLeadTime != 20*time.Minute {
		t.Errorf("expected 20m, got %v", m.wakeupLeadTime)
	}
}

func TestTenantCheckError_RevertsState(t *testing.T) {
	tenants := &fakeTenantLocator{err: fmt.Errorf("db connection lost")}
	m := NewLifecycleManager(&fakeScaler{}, nil, nil, tenants)

	d := hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "cluster-a",
	}

	err := m.ExecuteDirective(context.Background(), d)
	if err == nil {
		t.Fatal("expected error from tenant check")
	}
	if m.ClusterState("cluster-a") != StateActive {
		t.Errorf("state should revert to Active, got %s", m.ClusterState("cluster-a"))
	}
}
