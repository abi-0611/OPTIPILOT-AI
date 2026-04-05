package spoke

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	hubgrpc "github.com/optipilot-ai/optipilot/internal/global/grpc"
)

// freePort asks the OS for an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// startHub creates and starts a HubServer with a MemoryHubService on a random port.
// Returns the service, address, and a cancel func to stop the server.
func startHub(t *testing.T) (*hubgrpc.MemoryHubService, string, func()) {
	t.Helper()
	svc := hubgrpc.NewMemoryHubService()
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	srv := hubgrpc.NewHubServer(addr, svc, nil)
	ctx, cancel := context.WithCancel(context.Background())

	ready := make(chan struct{})
	go func() {
		close(ready)
		_ = srv.Start(ctx)
	}()
	<-ready
	time.Sleep(50 * time.Millisecond)

	return svc, addr, cancel
}

// defaultInfo returns a RegistrationInfo for testing.
func defaultInfo() RegistrationInfo {
	return RegistrationInfo{
		ClusterName:         "test-spoke-1",
		Provider:            "aws",
		Region:              "us-east-1",
		Endpoint:            "https://k8s.test.example.com",
		CarbonIntensityGCO2: 400.0,
		Labels:              map[string]string{"env": "test"},
	}
}

// defaultCollector returns a StaticCollector with reasonable test values.
func defaultCollector(name string) *StaticCollector {
	return &StaticCollector{
		ClusterName:          name,
		TotalCores:           64,
		UsedCores:            32,
		TotalMemoryGiB:       256,
		UsedMemoryGiB:        128,
		NodeCount:            8,
		SLOCompliancePercent: 99.5,
		HourlyCostUSD:        12.50,
		Health:               "Healthy",
	}
}

// ---------------------------------------------------------------------------
// Registration tests
// ---------------------------------------------------------------------------

func TestSpokeAgent_RegistersWithHub(t *testing.T) {
	svc, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	handler := &LogDirectiveHandler{}
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), handler,
		WithHeartbeatInterval(100*time.Millisecond),
		WithDirectivePollInterval(100*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	// Wait for the agent to register and send the first heartbeat.
	time.Sleep(300 * time.Millisecond)

	if agent.State() != StateRunning {
		t.Errorf("expected state Running, got %s", agent.State())
	}

	// Hub should have the cluster registered.
	names := svc.GetRegistered()
	if len(names) != 1 || names[0] != "test-spoke-1" {
		t.Errorf("expected [test-spoke-1], got %v", names)
	}

	cancel()
	<-done
}

func TestSpokeAgent_HeartbeatsSentPeriodically(t *testing.T) {
	svc, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), &LogDirectiveHandler{},
		WithHeartbeatInterval(80*time.Millisecond),
		WithDirectivePollInterval(5*time.Second), // don't interfere
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	// The hub returns HeartbeatIntervalS=30, which overrides our 80ms interval.
	// But the initial heartbeat fires immediately after registration, so we should
	// see at least 1 heartbeat within 400ms. The hub override is tested separately.
	time.Sleep(400 * time.Millisecond)

	count := agent.HeartbeatCount()
	if count < 1 {
		t.Errorf("expected at least 1 heartbeat, got %d", count)
	}

	// Hub should have status data.
	st := svc.GetLastStatus("test-spoke-1")
	if st == nil {
		t.Fatal("hub has no status for test-spoke-1")
	}
	if st.TotalCores != 64 {
		t.Errorf("expected TotalCores=64, got %v", st.TotalCores)
	}

	cancel()
	<-done
}

func TestSpokeAgent_HubReturnsHeartbeatInterval(t *testing.T) {
	// The MemoryHubService returns HeartbeatIntervalS=30 on registration.
	_, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), &LogDirectiveHandler{},
		WithHeartbeatInterval(1*time.Second), // initial — should be overridden to 30s by hub
		WithDirectivePollInterval(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	time.Sleep(200 * time.Millisecond)

	if agent.HeartbeatInterval() != 30*time.Second {
		t.Errorf("expected heartbeat interval=30s (from hub), got %s", agent.HeartbeatInterval())
	}

	cancel()
	<-done
}

func TestSpokeAgent_DirectivesPolledAndHandled(t *testing.T) {
	svc, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	handler := &LogDirectiveHandler{}
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), handler,
		WithHeartbeatInterval(5*time.Second),
		WithDirectivePollInterval(80*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	// Wait for registration, then enqueue a directive.
	time.Sleep(200 * time.Millisecond)

	svc.EnqueueDirective("test-spoke-1", hubgrpc.Directive{
		ID:          "d-100",
		Type:        hubgrpc.DirectiveTrafficShift,
		ClusterName: "test-spoke-1",
		Reason:      "cost optimization",
	})

	// Wait for the directive poll to fire.
	time.Sleep(200 * time.Millisecond)

	if agent.DirectiveCount() < 1 {
		t.Errorf("expected at least 1 directive processed, got %d", agent.DirectiveCount())
	}
	if len(handler.Handled) < 1 {
		t.Fatal("handler received no directives")
	}
	if handler.Handled[0].ID != "d-100" {
		t.Errorf("expected directive d-100, got %s", handler.Handled[0].ID)
	}

	cancel()
	<-done
}

func TestSpokeAgent_DirectivesDrained(t *testing.T) {
	svc, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	handler := &LogDirectiveHandler{}
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), handler,
		WithHeartbeatInterval(5*time.Second),
		WithDirectivePollInterval(80*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	time.Sleep(200 * time.Millisecond)

	// Enqueue two directives.
	svc.EnqueueDirective("test-spoke-1", hubgrpc.Directive{ID: "d-1", Type: hubgrpc.DirectiveHibernate})
	svc.EnqueueDirective("test-spoke-1", hubgrpc.Directive{ID: "d-2", Type: hubgrpc.DirectiveWakeUp})

	time.Sleep(200 * time.Millisecond)

	if len(handler.Handled) < 2 {
		t.Fatalf("expected 2 directives handled, got %d", len(handler.Handled))
	}

	// Second poll should be empty (drained).
	beforeCount := agent.DirectiveCount()
	time.Sleep(200 * time.Millisecond)
	if agent.DirectiveCount() != beforeCount {
		t.Errorf("directives should have been drained, but count increased from %d to %d",
			beforeCount, agent.DirectiveCount())
	}

	cancel()
	<-done
}

func TestSpokeAgent_StoppedState(t *testing.T) {
	_, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), &LogDirectiveHandler{},
		WithHeartbeatInterval(5*time.Second),
		WithDirectivePollInterval(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	if agent.State() != StateStopped {
		t.Errorf("expected state Stopped, got %s", agent.State())
	}
}

func TestSpokeAgent_InvalidHubAddr(t *testing.T) {
	info := defaultInfo()
	agent := NewSpokeAgent("127.0.0.1:1", info, defaultCollector(info.ClusterName), &LogDirectiveHandler{},
		WithHeartbeatInterval(100*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := agent.Start(ctx)
	if err == nil {
		t.Fatal("expected error connecting to invalid hub, got nil")
	}
}

func TestSpokeAgent_LastHeartbeatUpdated(t *testing.T) {
	_, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), &LogDirectiveHandler{},
		WithHeartbeatInterval(80*time.Millisecond),
		WithDirectivePollInterval(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	time.Sleep(300 * time.Millisecond)

	lh := agent.LastHeartbeat()
	if lh.IsZero() {
		t.Error("LastHeartbeat should not be zero after heartbeats")
	}
	if time.Since(lh) > 2*time.Second {
		t.Errorf("LastHeartbeat is too old: %v", lh)
	}

	cancel()
	<-done
}

func TestSpokeAgent_DefaultIntervals(t *testing.T) {
	info := defaultInfo()
	agent := NewSpokeAgent("localhost:50051", info, defaultCollector(info.ClusterName), &LogDirectiveHandler{})

	if agent.heartbeatInterval != 30*time.Second {
		t.Errorf("default heartbeat interval = %v, want 30s", agent.heartbeatInterval)
	}
	if agent.directivePollInterval != 10*time.Second {
		t.Errorf("default directive poll interval = %v, want 10s", agent.directivePollInterval)
	}
	if agent.State() != StateDisconnected {
		t.Errorf("initial state = %s, want disconnected", agent.State())
	}
}

func TestStaticCollector_Collect(t *testing.T) {
	c := &StaticCollector{
		ClusterName:          "c1",
		TotalCores:           32,
		UsedCores:            16,
		TotalMemoryGiB:       128,
		UsedMemoryGiB:        64,
		NodeCount:            4,
		SLOCompliancePercent: 98.0,
		HourlyCostUSD:        8.0,
		Health:               "Healthy",
	}

	report, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if report.ClusterName != "c1" {
		t.Errorf("ClusterName = %q, want %q", report.ClusterName, "c1")
	}
	if report.TotalCores != 32 {
		t.Errorf("TotalCores = %v, want 32", report.TotalCores)
	}
	if report.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestLogDirectiveHandler_Handle(t *testing.T) {
	h := &LogDirectiveHandler{}
	d := hubgrpc.Directive{ID: "d-test", Type: hubgrpc.DirectiveNoOp}

	if err := h.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.Handled) != 1 {
		t.Fatalf("expected 1 handled, got %d", len(h.Handled))
	}
	if h.Handled[0].ID != "d-test" {
		t.Errorf("Handled[0].ID = %q, want %q", h.Handled[0].ID, "d-test")
	}
}

func TestSpokeAgent_HubMarksUnhealthy(t *testing.T) {
	svc, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), &LogDirectiveHandler{},
		WithHeartbeatInterval(80*time.Millisecond),
		WithDirectivePollInterval(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	// Wait for registration + heartbeat.
	time.Sleep(200 * time.Millisecond)

	if !svc.IsHealthy("test-spoke-1") {
		t.Error("cluster should be healthy right after heartbeat")
	}

	cancel()
	<-done

	// Hack: manually backdate the last status to simulate heartbeat timeout.
	st := svc.GetLastStatus("test-spoke-1")
	if st != nil {
		st.Timestamp = time.Now().Add(-10 * time.Minute)
	}
	if svc.IsHealthy("test-spoke-1") {
		t.Error("cluster should be unhealthy after heartbeat timeout")
	}
}

func TestSpokeAgent_MultipleDirectiveTypes(t *testing.T) {
	svc, addr, stopHub := startHub(t)
	defer stopHub()

	info := defaultInfo()
	handler := &LogDirectiveHandler{}
	agent := NewSpokeAgent(addr, info, defaultCollector(info.ClusterName), handler,
		WithHeartbeatInterval(5*time.Second),
		WithDirectivePollInterval(80*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()

	time.Sleep(200 * time.Millisecond)

	// Enqueue diverse directive types.
	svc.EnqueueDirective("test-spoke-1", hubgrpc.Directive{
		ID: "d-ts", Type: hubgrpc.DirectiveTrafficShift,
		TrafficWeights: map[string]int32{"us-east-1": 80, "eu-west-1": 20},
	})
	svc.EnqueueDirective("test-spoke-1", hubgrpc.Directive{
		ID: "d-mig", Type: hubgrpc.DirectiveMigration,
		MigrationHints: []hubgrpc.MigrationHint{{
			Namespace: "default", Workload: "api", FromCluster: "us-east-1", ToCluster: "eu-west-1",
		}},
	})
	svc.EnqueueDirective("test-spoke-1", hubgrpc.Directive{
		ID: "d-hib", Type: hubgrpc.DirectiveHibernate, LifecycleAction: "hibernate",
	})

	time.Sleep(200 * time.Millisecond)

	if len(handler.Handled) < 3 {
		t.Fatalf("expected 3 directives, got %d", len(handler.Handled))
	}

	types := map[hubgrpc.DirectiveType]bool{}
	for _, d := range handler.Handled {
		types[d.Type] = true
	}
	for _, dt := range []hubgrpc.DirectiveType{hubgrpc.DirectiveTrafficShift, hubgrpc.DirectiveMigration, hubgrpc.DirectiveHibernate} {
		if !types[dt] {
			t.Errorf("missing directive type %s", dt)
		}
	}

	cancel()
	<-done
}
