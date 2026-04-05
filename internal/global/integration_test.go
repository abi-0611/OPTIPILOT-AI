package global_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	globalv1 "github.com/optipilot-ai/optipilot/api/global/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/global"
	hubgrpc "github.com/optipilot-ai/optipilot/internal/global/grpc"
	"github.com/optipilot-ai/optipilot/internal/global/spoke"
)

// ---------------------------------------------------------------------------
// Test helpers / fakes
// ---------------------------------------------------------------------------

// fixedCollector returns the same status report every time.
type fixedCollector struct {
	report *hubgrpc.ClusterStatusReport
}

func (f *fixedCollector) Collect(_ context.Context) (*hubgrpc.ClusterStatusReport, error) {
	cp := *f.report
	cp.Timestamp = time.Now()
	return &cp, nil
}

// recordingHandler records directives it receives.
type recordingHandler struct {
	received []hubgrpc.Directive
}

func (r *recordingHandler) Handle(_ context.Context, d hubgrpc.Directive) error {
	r.received = append(r.received, d)
	return nil
}

// noopScaler is a lifecycle scaler that succeeds but records calls.
type noopScaler struct {
	scaledToZero []string
	scaledUp     []string
}

func (n *noopScaler) ScaleToZero(_ context.Context, name string) error {
	n.scaledToZero = append(n.scaledToZero, name)
	return nil
}
func (n *noopScaler) ScaleUp(_ context.Context, name string) error {
	n.scaledUp = append(n.scaledUp, name)
	return nil
}

// noopDrainer succeeds immediately.
type noopDrainer struct {
	drained []string
}

func (n *noopDrainer) Drain(_ context.Context, name string) error {
	n.drained = append(n.drained, name)
	return nil
}

// noopForecaster returns false for all regions.
type noopForecaster struct{}

func (n *noopForecaster) ForecastDemand(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return false, nil
}

// noopTenantLocator says no cluster is sole location.
type noopTenantLocator struct{}

func (n *noopTenantLocator) IsSoleLocationForAnyTenant(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// startHub starts a MemoryHubService + gRPC server on a free port.
// Returns the service, the stop function, and the address.
func startHub(t *testing.T) (*hubgrpc.MemoryHubService, func(), string) {
	t.Helper()
	svc := hubgrpc.NewMemoryHubService()
	server := hubgrpc.NewHubServer("127.0.0.1:0", svc, nil)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- server.Start(ctx) }()

	// Give the server a moment to bind.
	time.Sleep(50 * time.Millisecond)

	stop := func() {
		cancel()
		server.Stop()
	}
	return svc, stop, server.Addr()
}

// ---------------------------------------------------------------------------
// Integration Test 1: Two spokes register, hub sees global view
// ---------------------------------------------------------------------------

func TestIntegration_TwoClustersRegisterAndHeartbeat(t *testing.T) {
	svc, stop, addr := startHub(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Spoke A.
	collectorA := &fixedCollector{report: &hubgrpc.ClusterStatusReport{
		ClusterName:          "cluster-a",
		TotalCores:           100,
		UsedCores:            60,
		TotalMemoryGiB:       256,
		UsedMemoryGiB:        128,
		NodeCount:            5,
		SLOCompliancePercent: 99.5,
		HourlyCostUSD:        12.50,
		Health:               "healthy",
	}}
	handlerA := &recordingHandler{}
	agentA := spoke.NewSpokeAgent(addr,
		spoke.RegistrationInfo{ClusterName: "cluster-a", Provider: "aws", Region: "us-east-1"},
		collectorA, handlerA,
		spoke.WithHeartbeatInterval(100*time.Millisecond),
		spoke.WithDirectivePollInterval(100*time.Millisecond),
	)

	// Spoke B.
	collectorB := &fixedCollector{report: &hubgrpc.ClusterStatusReport{
		ClusterName:          "cluster-b",
		TotalCores:           80,
		UsedCores:            20,
		TotalMemoryGiB:       128,
		UsedMemoryGiB:        32,
		NodeCount:            3,
		SLOCompliancePercent: 98.0,
		HourlyCostUSD:        8.00,
		Health:               "healthy",
	}}
	handlerB := &recordingHandler{}
	agentB := spoke.NewSpokeAgent(addr,
		spoke.RegistrationInfo{ClusterName: "cluster-b", Provider: "gcp", Region: "eu-west-1"},
		collectorB, handlerB,
		spoke.WithHeartbeatInterval(100*time.Millisecond),
		spoke.WithDirectivePollInterval(100*time.Millisecond),
	)

	// Start both agents.
	go agentA.Start(ctx)
	go agentB.Start(ctx)

	// Wait for registration.
	deadline := time.After(3 * time.Second)
	for {
		registered := svc.GetRegistered()
		if len(registered) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for registration, got: %v", svc.GetRegistered())
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Verify both registered.
	registered := svc.GetRegistered()
	hasA, hasB := false, false
	for _, name := range registered {
		if name == "cluster-a" {
			hasA = true
		}
		if name == "cluster-b" {
			hasB = true
		}
	}
	if !hasA || !hasB {
		t.Errorf("expected both clusters registered, got %v", registered)
	}

	// Wait for heartbeats to arrive.
	deadline = time.After(2 * time.Second)
	for {
		stA := svc.GetLastStatus("cluster-a")
		stB := svc.GetLastStatus("cluster-b")
		if stA != nil && stB != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for heartbeats")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Verify hub has global view.
	stA := svc.GetLastStatus("cluster-a")
	if stA.TotalCores != 100 || stA.UsedCores != 60 {
		t.Errorf("cluster-a status wrong: %+v", stA)
	}
	stB := svc.GetLastStatus("cluster-b")
	if stB.TotalCores != 80 || stB.UsedCores != 20 {
		t.Errorf("cluster-b status wrong: %+v", stB)
	}
}

// ---------------------------------------------------------------------------
// Integration Test 2: Global solver produces traffic shift directive
// ---------------------------------------------------------------------------

func TestIntegration_SolverProducesTrafficDirective(t *testing.T) {
	solver := global.NewGlobalSolver()

	snapshots := []*global.ClusterSnapshot{
		{
			Name:                 "cluster-a",
			Provider:             "aws",
			Region:               "us-east-1",
			Health:               "healthy",
			TotalCores:           100,
			UsedCores:            60,
			SLOCompliancePercent: 99.5,
			HourlyCostUSD:        12.50,
			CarbonIntensityGCO2:  400,
		},
		{
			Name:                 "cluster-b",
			Provider:             "gcp",
			Region:               "eu-west-1",
			Health:               "healthy",
			TotalCores:           80,
			UsedCores:            20,
			SLOCompliancePercent: 98.0,
			HourlyCostUSD:        8.00,
			CarbonIntensityGCO2:  200,
		},
	}

	policy := &globalv1.GlobalPolicySpec{
		TrafficShifting: &globalv1.TrafficShiftingSpec{
			Strategy:                 globalv1.StrategyCost,
			MaxShiftPerCyclePercent:  25,
			MinDestinationSLOPercent: 95,
		},
	}

	result, err := solver.Solve(&global.SolverInput{
		Clusters: snapshots,
		Policy:   policy,
	})
	if err != nil {
		t.Fatalf("solver error: %v", err)
	}
	if len(result.Directives) == 0 {
		t.Fatal("expected at least one directive")
	}

	found := false
	for _, d := range result.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			found = true
			// Weights should sum to 100.
			total := int32(0)
			for _, w := range d.TrafficWeights {
				total += w
			}
			if total != 100 {
				t.Errorf("traffic weights should sum to 100, got %d: %v", total, d.TrafficWeights)
			}
			// Cost-optimized: cluster-b is cheaper, should get more weight.
			wB := d.TrafficWeights["cluster-b"]
			wA := d.TrafficWeights["cluster-a"]
			if wB <= wA {
				t.Errorf("cost-optimized should prefer cheaper cluster-b (%d) over cluster-a (%d)", wB, wA)
			}
		}
	}
	if !found {
		t.Error("no traffic_shift directive found")
	}
}

// ---------------------------------------------------------------------------
// Integration Test 3: Directive delivered to spoke via hub
// ---------------------------------------------------------------------------

func TestIntegration_DirectiveDeliveredToSpoke(t *testing.T) {
	svc, stop, addr := startHub(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handler := &recordingHandler{}
	collector := &fixedCollector{report: &hubgrpc.ClusterStatusReport{
		ClusterName:          "cluster-a",
		TotalCores:           100,
		UsedCores:            50,
		SLOCompliancePercent: 99.0,
		Health:               "healthy",
	}}

	agent := spoke.NewSpokeAgent(addr,
		spoke.RegistrationInfo{ClusterName: "cluster-a", Provider: "aws", Region: "us-east-1"},
		collector, handler,
		spoke.WithHeartbeatInterval(100*time.Millisecond),
		spoke.WithDirectivePollInterval(100*time.Millisecond),
	)

	go agent.Start(ctx)

	// Wait for registration.
	deadline := time.After(2 * time.Second)
	for {
		if len(svc.GetRegistered()) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for registration")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Enqueue a traffic shift directive.
	svc.EnqueueDirective("cluster-a", hubgrpc.Directive{
		ID:             "test-directive-1",
		Type:           hubgrpc.DirectiveTrafficShift,
		ClusterName:    "cluster-a",
		TrafficWeights: map[string]int32{"cluster-a": 60, "cluster-b": 40},
		Reason:         "integration test",
	})

	// Wait for spoke to receive the directive.
	deadline = time.After(3 * time.Second)
	for {
		if len(handler.received) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for directive delivery")
		case <-time.After(50 * time.Millisecond):
		}
	}

	if handler.received[0].ID != "test-directive-1" {
		t.Errorf("expected directive ID test-directive-1, got %s", handler.received[0].ID)
	}
	if handler.received[0].Type != hubgrpc.DirectiveTrafficShift {
		t.Errorf("expected traffic_shift, got %s", handler.received[0].Type)
	}
}

// ---------------------------------------------------------------------------
// Integration Test 4: Solver hibernation + lifecycle execution
// ---------------------------------------------------------------------------

func TestIntegration_HibernationEndToEnd(t *testing.T) {
	// Step 1: Solver detects idle cluster and produces hibernate directive.
	solver := global.NewGlobalSolver()

	snapshots := []*global.ClusterSnapshot{
		{
			Name:                 "cluster-a",
			Provider:             "aws",
			Region:               "us-east-1",
			Health:               "healthy",
			TotalCores:           100,
			UsedCores:            80,
			SLOCompliancePercent: 99.0,
			HourlyCostUSD:        12.0,
			CarbonIntensityGCO2:  400,
		},
		{
			Name:                 "cluster-b",
			Provider:             "gcp",
			Region:               "eu-west-1",
			Health:               "healthy",
			TotalCores:           100,
			UsedCores:            2, // Nearly idle.
			SLOCompliancePercent: 99.0,
			HourlyCostUSD:        8.0,
			CarbonIntensityGCO2:  200,
		},
	}

	policy := &globalv1.GlobalPolicySpec{
		TrafficShifting: &globalv1.TrafficShiftingSpec{
			Strategy:                globalv1.StrategyBalance,
			MaxShiftPerCyclePercent: 25,
		},
		ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
			HibernationEnabled:   true,
			MinActiveClusters:    1,
			IdleThresholdPercent: 10,
		},
	}

	result, err := solver.Solve(&global.SolverInput{
		Clusters: snapshots,
		Policy:   policy,
	})
	if err != nil {
		t.Fatalf("solver error: %v", err)
	}

	// Find hibernate directive for cluster-b.
	var hibernateDirective *hubgrpc.Directive
	for i, d := range result.Directives {
		if d.Type == hubgrpc.DirectiveHibernate && d.ClusterName == "cluster-b" {
			hibernateDirective = &result.Directives[i]
			break
		}
	}
	if hibernateDirective == nil {
		t.Fatalf("expected hibernate directive for cluster-b, got directives: %v", result.Directives)
	}

	// Step 2: Lifecycle manager executes the directive.
	scaler := &noopScaler{}
	drainer := &noopDrainer{}
	lcm := global.NewLifecycleManager(scaler, drainer, &noopForecaster{}, &noopTenantLocator{},
		global.WithManagementCluster("hub-cluster"))

	if err := lcm.ExecuteDirective(context.Background(), *hibernateDirective); err != nil {
		t.Fatalf("execute hibernate: %v", err)
	}

	// Verify lifecycle state.
	if lcm.ClusterState("cluster-b") != global.StateHibernating {
		t.Errorf("expected Hibernating, got %s", lcm.ClusterState("cluster-b"))
	}
	if len(drainer.drained) != 1 || drainer.drained[0] != "cluster-b" {
		t.Errorf("expected drain of cluster-b, got %v", drainer.drained)
	}
	if len(scaler.scaledToZero) != 1 || scaler.scaledToZero[0] != "cluster-b" {
		t.Errorf("expected scale-to-zero of cluster-b, got %v", scaler.scaledToZero)
	}

	// Step 3: Verify hibernated cluster is listed.
	hibernating := lcm.HibernatingClusters()
	if len(hibernating) != 1 || hibernating[0] != "cluster-b" {
		t.Errorf("expected cluster-b in hibernating list, got %v", hibernating)
	}
}

// ---------------------------------------------------------------------------
// Integration Test 5: Wake-up after hibernation
// ---------------------------------------------------------------------------

func TestIntegration_WakeUpAfterHibernation(t *testing.T) {
	scaler := &noopScaler{}
	lcm := global.NewLifecycleManager(scaler, &noopDrainer{}, &noopForecaster{}, &noopTenantLocator{})

	// First hibernate.
	if err := lcm.ExecuteDirective(context.Background(), hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "cluster-b",
	}); err != nil {
		t.Fatalf("hibernate: %v", err)
	}
	if lcm.ClusterState("cluster-b") != global.StateHibernating {
		t.Fatal("expected Hibernating state")
	}

	// Now wake up.
	if err := lcm.ExecuteDirective(context.Background(), hubgrpc.Directive{
		Type:        hubgrpc.DirectiveWakeUp,
		ClusterName: "cluster-b",
	}); err != nil {
		t.Fatalf("wake-up: %v", err)
	}

	if lcm.ClusterState("cluster-b") != global.StateActive {
		t.Errorf("expected Active after wake-up, got %s", lcm.ClusterState("cluster-b"))
	}
	if len(scaler.scaledUp) != 1 || scaler.scaledUp[0] != "cluster-b" {
		t.Errorf("expected scale-up of cluster-b, got %v", scaler.scaledUp)
	}
}

// ---------------------------------------------------------------------------
// Integration Test 6: Management cluster never hibernated
// ---------------------------------------------------------------------------

func TestIntegration_ManagementClusterProtected(t *testing.T) {
	lcm := global.NewLifecycleManager(&noopScaler{}, &noopDrainer{}, &noopForecaster{}, &noopTenantLocator{},
		global.WithManagementCluster("hub-cluster"))

	err := lcm.ExecuteDirective(context.Background(), hubgrpc.Directive{
		Type:        hubgrpc.DirectiveHibernate,
		ClusterName: "hub-cluster",
	})
	if err == nil {
		t.Fatal("expected error refusing to hibernate management cluster")
	}
	if lcm.ClusterState("hub-cluster") != global.StateActive {
		t.Errorf("management cluster should stay Active")
	}
}

// ---------------------------------------------------------------------------
// Integration Test 7: Idle tracking feeds into solver
// ---------------------------------------------------------------------------

func TestIntegration_IdleTrackingToSolverPipeline(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	lcm := global.NewLifecycleManager(&noopScaler{}, &noopDrainer{}, &noopForecaster{}, &noopTenantLocator{},
		global.WithLifecycleNowFn(func() time.Time { return now }),
		global.WithIdleWindow(30*time.Minute))

	policy := &globalv1.ClusterLifecycleSpec{
		IdleThresholdPercent: 10,
		IdleWindowMinutes:    30,
	}

	// Cluster at 5% utilization.
	if lcm.UpdateIdleStatus("cluster-c", 5.0, policy) {
		t.Error("should not be idle candidate immediately")
	}

	// Advance 31 minutes.
	now = now.Add(31 * time.Minute)
	if !lcm.UpdateIdleStatus("cluster-c", 5.0, policy) {
		t.Error("cluster should be idle candidate after 31 minutes")
	}

	// Simulate the solver producing a hibernate directive, then execute it.
	solver := global.NewGlobalSolver()
	result, _ := solver.Solve(&global.SolverInput{
		Clusters: []*global.ClusterSnapshot{
			{Name: "cluster-c", Health: "healthy", TotalCores: 100, UsedCores: 2, SLOCompliancePercent: 99.0, HourlyCostUSD: 5.0, CarbonIntensityGCO2: 200},
			{Name: "cluster-d", Health: "healthy", TotalCores: 100, UsedCores: 80, SLOCompliancePercent: 99.0, HourlyCostUSD: 10.0, CarbonIntensityGCO2: 400},
		},
		Policy: &globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{Strategy: globalv1.StrategyBalance, MaxShiftPerCyclePercent: 25},
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled:   true,
				MinActiveClusters:    1,
				IdleThresholdPercent: 10,
			},
		},
	})

	// Find and execute hibernate.
	for _, d := range result.Directives {
		if d.Type == hubgrpc.DirectiveHibernate && d.ClusterName == "cluster-c" {
			if err := lcm.ExecuteDirective(context.Background(), d); err != nil {
				t.Fatalf("execute: %v", err)
			}
		}
	}

	if lcm.ClusterState("cluster-c") != global.StateHibernating {
		t.Errorf("expected cluster-c to be Hibernating, got %s", lcm.ClusterState("cluster-c"))
	}
}

// ---------------------------------------------------------------------------
// Integration Test 8: Full pipeline — register, heartbeat, solve, enqueue
// ---------------------------------------------------------------------------

func TestIntegration_FullPipeline(t *testing.T) {
	svc, stop, addr := startHub(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start two spoke agents.
	handlerA := &recordingHandler{}
	agentA := spoke.NewSpokeAgent(addr,
		spoke.RegistrationInfo{ClusterName: "prod-us", Provider: "aws", Region: "us-east-1"},
		&fixedCollector{report: &hubgrpc.ClusterStatusReport{
			ClusterName: "prod-us", TotalCores: 100, UsedCores: 80,
			SLOCompliancePercent: 99.5, HourlyCostUSD: 15.0, Health: "healthy",
		}}, handlerA,
		spoke.WithHeartbeatInterval(100*time.Millisecond),
		spoke.WithDirectivePollInterval(100*time.Millisecond),
	)

	handlerB := &recordingHandler{}
	agentB := spoke.NewSpokeAgent(addr,
		spoke.RegistrationInfo{ClusterName: "prod-eu", Provider: "gcp", Region: "eu-west-1"},
		&fixedCollector{report: &hubgrpc.ClusterStatusReport{
			ClusterName: "prod-eu", TotalCores: 80, UsedCores: 30,
			SLOCompliancePercent: 98.0, HourlyCostUSD: 8.0, Health: "healthy",
		}}, handlerB,
		spoke.WithHeartbeatInterval(100*time.Millisecond),
		spoke.WithDirectivePollInterval(100*time.Millisecond),
	)

	go agentA.Start(ctx)
	go agentB.Start(ctx)

	// Wait for both to register and send heartbeats.
	waitFor(t, 3*time.Second, func() bool {
		return svc.GetLastStatus("prod-us") != nil && svc.GetLastStatus("prod-eu") != nil
	})

	// Build solver input from hub state.
	stUS := svc.GetLastStatus("prod-us")
	stEU := svc.GetLastStatus("prod-eu")

	snapshots := []*global.ClusterSnapshot{
		{
			Name: "prod-us", TotalCores: stUS.TotalCores, UsedCores: stUS.UsedCores,
			SLOCompliancePercent: stUS.SLOCompliancePercent, HourlyCostUSD: stUS.HourlyCostUSD,
			Health: stUS.Health, CarbonIntensityGCO2: 450,
		},
		{
			Name: "prod-eu", TotalCores: stEU.TotalCores, UsedCores: stEU.UsedCores,
			SLOCompliancePercent: stEU.SLOCompliancePercent, HourlyCostUSD: stEU.HourlyCostUSD,
			Health: stEU.Health, CarbonIntensityGCO2: 200,
		},
	}

	solver := global.NewGlobalSolver()
	result, err := solver.Solve(&global.SolverInput{
		Clusters: snapshots,
		Policy: &globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{
				Strategy:                globalv1.StrategyBalance,
				MaxShiftPerCyclePercent: 25,
			},
		},
	})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}

	// Enqueue directives to spokes.
	for _, d := range result.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			for clusterName := range d.TrafficWeights {
				svc.EnqueueDirective(clusterName, d)
			}
		}
	}

	// Wait for at least one spoke to receive a directive.
	waitFor(t, 3*time.Second, func() bool {
		return len(handlerA.received) > 0 || len(handlerB.received) > 0
	})

	totalDirectives := len(handlerA.received) + len(handlerB.received)
	if totalDirectives == 0 {
		t.Fatal("no directives delivered to any spoke")
	}
}

// ---------------------------------------------------------------------------
// Integration Test 9: Predictive wake-up via demand forecast
// ---------------------------------------------------------------------------

func TestIntegration_PredictiveWakeUp(t *testing.T) {
	forecaster := &fakeRegionForecaster{regions: map[string]bool{"us-east-1": true}}
	scaler := &noopScaler{}
	lcm := global.NewLifecycleManager(scaler, &noopDrainer{}, forecaster, &noopTenantLocator{},
		global.WithWakeupLead(15*time.Minute))

	// Hibernate cluster first.
	lcm.ExecuteDirective(context.Background(), hubgrpc.Directive{
		Type: hubgrpc.DirectiveHibernate, ClusterName: "cluster-a",
	})

	clusters := []*global.ClusterSnapshot{
		{Name: "cluster-a", Region: "us-east-1"},
		{Name: "cluster-b", Region: "eu-west-1"},
	}

	directives := lcm.CheckPredictiveWakeUp(context.Background(), clusters)
	if len(directives) != 1 {
		t.Fatalf("expected 1 predictive wake-up, got %d", len(directives))
	}
	if directives[0].ClusterName != "cluster-a" {
		t.Errorf("expected cluster-a, got %s", directives[0].ClusterName)
	}

	// Execute the wake-up.
	if err := lcm.ExecuteDirective(context.Background(), directives[0]); err != nil {
		t.Fatalf("wake-up: %v", err)
	}
	if lcm.ClusterState("cluster-a") != global.StateActive {
		t.Errorf("expected Active after predictive wake-up, got %s", lcm.ClusterState("cluster-a"))
	}
}

type fakeRegionForecaster struct {
	regions map[string]bool
}

func (f *fakeRegionForecaster) ForecastDemand(_ context.Context, region string, _ time.Duration) (bool, error) {
	return f.regions[region], nil
}

// ---------------------------------------------------------------------------
// Integration Test 10: Multiple solver rounds with changing state
// ---------------------------------------------------------------------------

func TestIntegration_MultipleSolverRounds(t *testing.T) {
	solver := global.NewGlobalSolver()

	// Round 1: Both clusters healthy.
	result1, _ := solver.Solve(&global.SolverInput{
		Clusters: []*global.ClusterSnapshot{
			{Name: "a", Health: "healthy", TotalCores: 100, UsedCores: 50, SLOCompliancePercent: 99.0, HourlyCostUSD: 10, CarbonIntensityGCO2: 300},
			{Name: "b", Health: "healthy", TotalCores: 100, UsedCores: 50, SLOCompliancePercent: 99.0, HourlyCostUSD: 10, CarbonIntensityGCO2: 300},
		},
		Policy: &globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{
				Strategy: globalv1.StrategyBalance, MaxShiftPerCyclePercent: 25,
			},
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled: true, MinActiveClusters: 1, IdleThresholdPercent: 10,
			},
		},
	})

	// Should have traffic directive, no hibernation (both 50% util).
	hasTraffic, hasHibernate := false, false
	for _, d := range result1.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			hasTraffic = true
		}
		if d.Type == hubgrpc.DirectiveHibernate {
			hasHibernate = true
		}
	}
	if !hasTraffic {
		t.Error("round 1: expected traffic directive")
	}
	if hasHibernate {
		t.Error("round 1: should not hibernate at 50% util")
	}

	// Round 2: Cluster-b drops to near zero.
	result2, _ := solver.Solve(&global.SolverInput{
		Clusters: []*global.ClusterSnapshot{
			{Name: "a", Health: "healthy", TotalCores: 100, UsedCores: 80, SLOCompliancePercent: 99.0, HourlyCostUSD: 10, CarbonIntensityGCO2: 300},
			{Name: "b", Health: "healthy", TotalCores: 100, UsedCores: 2, SLOCompliancePercent: 99.0, HourlyCostUSD: 10, CarbonIntensityGCO2: 300},
		},
		Policy: &globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{
				Strategy: globalv1.StrategyBalance, MaxShiftPerCyclePercent: 25,
			},
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled: true, MinActiveClusters: 1, IdleThresholdPercent: 10,
			},
		},
	})

	hasHibernate = false
	for _, d := range result2.Directives {
		if d.Type == hubgrpc.DirectiveHibernate && d.ClusterName == "b" {
			hasHibernate = true
		}
	}
	if !hasHibernate {
		t.Error("round 2: expected hibernate directive for cluster-b at 2% utilization")
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("waitFor: timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Ensure unused import error doesn't occur.
var _ = fmt.Sprintf
