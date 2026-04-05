package hubgrpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
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

// startTestServer spins up a HubServer + MemoryHubService on a random port
// and returns a connected SpokeClient plus cleanup func.
func startTestServer(t *testing.T) (*MemoryHubService, *SpokeClient, func()) {
	t.Helper()

	svc := NewMemoryHubService()
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	srv := NewHubServer(addr, svc, nil) // insecure for tests
	ctx, cancel := context.WithCancel(context.Background())

	ready := make(chan struct{})
	go func() {
		// Tiny delay to let Serve bind; signal right away.
		close(ready)
		_ = srv.Start(ctx)
	}()
	<-ready
	// Give server a moment to start listening.
	time.Sleep(50 * time.Millisecond)

	client, err := NewSpokeClient(addr, nil)
	if err != nil {
		cancel()
		t.Fatalf("NewSpokeClient: %v", err)
	}

	cleanup := func() {
		client.Close()
		cancel()
		time.Sleep(20 * time.Millisecond) // allow graceful stop
	}
	return svc, client, cleanup
}

// ---------------------------------------------------------------------------
// Round-trip gRPC tests
// ---------------------------------------------------------------------------

func TestRegisterCluster_Success(t *testing.T) {
	svc, client, cleanup := startTestServer(t)
	defer cleanup()

	resp, err := client.RegisterCluster(context.Background(), &RegisterClusterRequest{
		ClusterName: "us-east-1",
		Provider:    "aws",
		Region:      "us-east-1",
		Endpoint:    "https://k8s.us-east-1.example.com",
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	if !resp.Accepted {
		t.Errorf("expected Accepted=true, got false")
	}
	if resp.HeartbeatIntervalS != 30 {
		t.Errorf("expected HeartbeatIntervalS=30, got %d", resp.HeartbeatIntervalS)
	}

	// Verify the service stored it.
	names := svc.GetRegistered()
	if len(names) != 1 || names[0] != "us-east-1" {
		t.Errorf("expected [us-east-1], got %v", names)
	}
}

func TestRegisterCluster_EmptyName(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()

	_, err := client.RegisterCluster(context.Background(), &RegisterClusterRequest{
		ClusterName: "",
		Provider:    "aws",
	})
	if err == nil {
		t.Fatal("expected error for empty cluster name, got nil")
	}
}

func TestReportStatus_Heartbeat(t *testing.T) {
	svc, client, cleanup := startTestServer(t)
	defer cleanup()

	// Register first.
	_, err := client.RegisterCluster(context.Background(), &RegisterClusterRequest{
		ClusterName: "eu-west-1",
		Provider:    "gcp",
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}

	now := time.Now()
	ack, err := client.ReportStatus(context.Background(), &ClusterStatusReport{
		ClusterName:          "eu-west-1",
		TotalCores:           64,
		UsedCores:            32,
		TotalMemoryGiB:       256,
		UsedMemoryGiB:        128,
		SLOCompliancePercent: 99.5,
		Health:               "Healthy",
		Timestamp:            now,
	})
	if err != nil {
		t.Fatalf("ReportStatus: %v", err)
	}
	if !ack.Received {
		t.Error("expected Received=true")
	}

	st := svc.GetLastStatus("eu-west-1")
	if st == nil {
		t.Fatal("GetLastStatus returned nil")
	}
	if st.TotalCores != 64 {
		t.Errorf("expected TotalCores=64, got %v", st.TotalCores)
	}
}

func TestReportStatus_UnregisteredCluster(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()

	_, err := client.ReportStatus(context.Background(), &ClusterStatusReport{
		ClusterName: "unknown-cluster",
		Health:      "Healthy",
	})
	if err == nil {
		t.Fatal("expected error for unregistered cluster, got nil")
	}
}

func TestGetDirective_WithPending(t *testing.T) {
	svc, client, cleanup := startTestServer(t)
	defer cleanup()

	// Register.
	_, _ = client.RegisterCluster(context.Background(), &RegisterClusterRequest{
		ClusterName: "ap-south-1",
		Provider:    "aws",
	})

	// Enqueue a directive.
	svc.EnqueueDirective("ap-south-1", Directive{
		ID:          "d-001",
		Type:        DirectiveTrafficShift,
		ClusterName: "ap-south-1",
		TrafficWeights: map[string]int32{
			"us-east-1":  70,
			"ap-south-1": 30,
		},
		Reason: "cost optimisation",
	})

	resp, err := client.GetDirective(context.Background(), "ap-south-1")
	if err != nil {
		t.Fatalf("GetDirective: %v", err)
	}
	if len(resp.Directives) != 1 {
		t.Fatalf("expected 1 directive, got %d", len(resp.Directives))
	}
	d := resp.Directives[0]
	if d.ID != "d-001" || d.Type != DirectiveTrafficShift {
		t.Errorf("unexpected directive: %+v", d)
	}
}

func TestGetDirective_Drains(t *testing.T) {
	svc, client, cleanup := startTestServer(t)
	defer cleanup()

	_, _ = client.RegisterCluster(context.Background(), &RegisterClusterRequest{
		ClusterName: "eu-central-1",
	})
	svc.EnqueueDirective("eu-central-1", Directive{ID: "d-002", Type: DirectiveNoOp})

	// First fetch returns the directive.
	resp1, err := client.GetDirective(context.Background(), "eu-central-1")
	if err != nil {
		t.Fatalf("first GetDirective: %v", err)
	}
	if len(resp1.Directives) != 1 {
		t.Fatalf("expected 1, got %d", len(resp1.Directives))
	}

	// Second fetch is empty (drained).
	resp2, err := client.GetDirective(context.Background(), "eu-central-1")
	if err != nil {
		t.Fatalf("second GetDirective: %v", err)
	}
	if len(resp2.Directives) != 0 {
		t.Errorf("expected 0 after drain, got %d", len(resp2.Directives))
	}
}

func TestRequestTrafficShift_Success(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()

	resp, err := client.RequestTrafficShift(context.Background(), &TrafficShiftRequest{
		ClusterName: "us-east-1",
		Service:     "api-gateway",
		Weights:     map[string]int32{"us-east-1": 80, "eu-west-1": 20},
		Reason:      "latency spike",
	})
	if err != nil {
		t.Fatalf("RequestTrafficShift: %v", err)
	}
	if !resp.Accepted {
		t.Error("expected Accepted=true")
	}
}

func TestRequestTrafficShift_MissingFields(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()

	_, err := client.RequestTrafficShift(context.Background(), &TrafficShiftRequest{
		ClusterName: "",
		Service:     "",
	})
	if err == nil {
		t.Fatal("expected error for missing fields, got nil")
	}
}

// ---------------------------------------------------------------------------
// MemoryHubService unit tests (no gRPC)
// ---------------------------------------------------------------------------

func TestMemoryHubService_IsHealthy(t *testing.T) {
	svc := NewMemoryHubService()
	ctx := context.Background()

	// Not registered → not healthy.
	if svc.IsHealthy("ghost") {
		t.Error("unregistered cluster should not be healthy")
	}

	_, _ = svc.RegisterCluster(ctx, &RegisterClusterRequest{ClusterName: "c1"})
	// No status yet → not healthy.
	if svc.IsHealthy("c1") {
		t.Error("cluster with no status should not be healthy")
	}

	// Report recent status.
	_, _ = svc.ReportStatus(ctx, &ClusterStatusReport{
		ClusterName: "c1",
		Timestamp:   time.Now(),
		Health:      "Healthy",
	})
	if !svc.IsHealthy("c1") {
		t.Error("cluster with fresh heartbeat should be healthy")
	}

	// Expire the heartbeat by backdating.
	svc.mu.Lock()
	svc.lastStatus["c1"].Timestamp = time.Now().Add(-10 * time.Minute)
	svc.mu.Unlock()
	if svc.IsHealthy("c1") {
		t.Error("cluster with expired heartbeat should not be healthy")
	}
}

func TestMemoryHubService_GetRegistered(t *testing.T) {
	svc := NewMemoryHubService()
	ctx := context.Background()

	if got := svc.GetRegistered(); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}

	_, _ = svc.RegisterCluster(ctx, &RegisterClusterRequest{ClusterName: "a"})
	_, _ = svc.RegisterCluster(ctx, &RegisterClusterRequest{ClusterName: "b"})

	names := svc.GetRegistered()
	if len(names) != 2 {
		t.Fatalf("expected 2, got %d", len(names))
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["a"] || !found["b"] {
		t.Errorf("expected [a, b], got %v", names)
	}
}

func TestServerLifecycle_StartStop(t *testing.T) {
	svc := NewMemoryHubService()
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	srv := NewHubServer(addr, svc, nil)

	if srv.Addr() != addr {
		t.Errorf("Addr() = %q, want %q", srv.Addr(), addr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down within 3 s")
	}
}

// ---------------------------------------------------------------------------
// mTLS helpers — error path tests (no real certs needed)
// ---------------------------------------------------------------------------

func TestServerCredentials_InvalidPaths(t *testing.T) {
	_, err := ServerCredentials(MTLSConfig{
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
		CAFile:   "/nonexistent/ca.pem",
	})
	if err == nil {
		t.Fatal("expected error for invalid cert paths")
	}
}

func TestClientCredentials_InvalidPaths(t *testing.T) {
	_, err := ClientCredentials(MTLSConfig{
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
		CAFile:   "/nonexistent/ca.pem",
	})
	if err == nil {
		t.Fatal("expected error for invalid cert paths")
	}
}

func TestJSONCodec_RoundTrip(t *testing.T) {
	c := JSONCodec{}
	if c.Name() != "json" {
		t.Errorf("Name() = %q, want %q", c.Name(), "json")
	}

	orig := &RegisterClusterRequest{
		ClusterName: "test",
		Provider:    "aws",
		Labels:      map[string]string{"env": "prod"},
	}
	data, err := c.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got := new(RegisterClusterRequest)
	if err := c.Unmarshal(data, got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ClusterName != orig.ClusterName || got.Provider != orig.Provider {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}

func TestDirectiveType_Constants(t *testing.T) {
	types := []DirectiveType{
		DirectiveTrafficShift,
		DirectiveMigration,
		DirectiveHibernate,
		DirectiveWakeUp,
		DirectiveNoOp,
	}
	expected := []string{"traffic_shift", "migration", "hibernate", "wake_up", "noop"}
	for i, dt := range types {
		if string(dt) != expected[i] {
			t.Errorf("DirectiveType[%d] = %q, want %q", i, dt, expected[i])
		}
	}
}
