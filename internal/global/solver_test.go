package global

import (
	"testing"
	"time"

	globalv1 "github.com/optipilot-ai/optipilot/api/global/v1alpha1"
	hubgrpc "github.com/optipilot-ai/optipilot/internal/global/grpc"
)

// fixedNow returns a fixed clock for deterministic tests.
func fixedNow() time.Time { return time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC) }

// newSolver returns a solver with a fixed clock.
func newSolver() *GlobalSolver {
	s := NewGlobalSolver()
	s.NowFn = fixedNow
	return s
}

// helpers to build test clusters.
func cluster(name, region, health string, totalCores, usedCores, slo, cost, carbon float64) *ClusterSnapshot {
	return &ClusterSnapshot{
		Name:                 name,
		Region:               region,
		Health:               health,
		TotalCores:           totalCores,
		UsedCores:            usedCores,
		TotalMemoryGiB:       totalCores * 4, // arbitrary
		UsedMemoryGiB:        usedCores * 4,
		SLOCompliancePercent: slo,
		HourlyCostUSD:        cost,
		CarbonIntensityGCO2:  carbon,
	}
}

func balancedPolicy() *globalv1.GlobalPolicySpec {
	return &globalv1.GlobalPolicySpec{
		TrafficShifting: &globalv1.TrafficShiftingSpec{
			Strategy:                 globalv1.StrategyBalance,
			MaxShiftPerCyclePercent:  25,
			MinDestinationSLOPercent: 90.0,
		},
	}
}

// ---------------------------------------------------------------------------
// Nil / empty input
// ---------------------------------------------------------------------------

func TestSolve_NilInput(t *testing.T) {
	s := newSolver()
	res, err := s.Solve(nil)
	if err != nil {
		t.Fatalf("Solve(nil): %v", err)
	}
	if res.Summary != "no clusters" {
		t.Errorf("summary = %q, want %q", res.Summary, "no clusters")
	}
}

func TestSolve_EmptyClusters(t *testing.T) {
	s := newSolver()
	res, err := s.Solve(&SolverInput{Policy: balancedPolicy()})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Summary != "no clusters" {
		t.Errorf("summary = %q, want %q", res.Summary, "no clusters")
	}
}

func TestSolve_NoPolicy(t *testing.T) {
	s := newSolver()
	res, err := s.Solve(&SolverInput{
		Clusters: []*ClusterSnapshot{cluster("c1", "us-east-1", "healthy", 64, 32, 99, 10, 400)},
	})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Summary != "no policy" {
		t.Errorf("summary = %q, want %q", res.Summary, "no policy")
	}
}

// ---------------------------------------------------------------------------
// Traffic weight tests
// ---------------------------------------------------------------------------

func TestSolve_TrafficWeights_Balanced(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("us-east", "us-east-1", "healthy", 100, 50, 99, 10, 400),
			cluster("eu-west", "eu-west-1", "healthy", 100, 50, 99, 10, 400),
		},
		Policy: balancedPolicy(),
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// Equal clusters should get equal-ish weights.
	found := false
	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			found = true
			total := int32(0)
			for _, w := range d.TrafficWeights {
				total += w
			}
			if total != 100 {
				t.Errorf("weights sum = %d, want 100", total)
			}
			// With identical clusters, each should get 50.
			for name, w := range d.TrafficWeights {
				if w != 50 {
					t.Errorf("cluster %s weight = %d, want 50", name, w)
				}
			}
		}
	}
	if !found {
		t.Error("expected a traffic_shift directive")
	}
}

func TestSolve_TrafficWeights_CostOptimized(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("cheap", "us-east-1", "healthy", 100, 50, 99, 5, 400),
			cluster("expensive", "eu-west-1", "healthy", 100, 50, 99, 20, 400),
		},
		Policy: &globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{
				Strategy:                globalv1.StrategyCost,
				MaxShiftPerCyclePercent: 25,
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			if d.TrafficWeights["cheap"] <= d.TrafficWeights["expensive"] {
				t.Errorf("cheap cluster (%d) should have more weight than expensive (%d)",
					d.TrafficWeights["cheap"], d.TrafficWeights["expensive"])
			}
			return
		}
	}
	t.Error("expected a traffic_shift directive")
}

func TestSolve_TrafficWeights_CarbonOptimized(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("green", "eu-north-1", "healthy", 100, 50, 99, 10, 50), // low carbon
			cluster("dirty", "us-east-1", "healthy", 100, 50, 99, 10, 800), // high carbon
		},
		Policy: &globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{
				Strategy:                globalv1.StrategyCarbon,
				MaxShiftPerCyclePercent: 25,
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			if d.TrafficWeights["green"] <= d.TrafficWeights["dirty"] {
				t.Errorf("green cluster (%d) should have more weight than dirty (%d)",
					d.TrafficWeights["green"], d.TrafficWeights["dirty"])
			}
			return
		}
	}
	t.Error("expected a traffic_shift directive")
}

func TestSolve_TrafficWeights_SLOFiltersUnhealthy(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("good", "us-east-1", "healthy", 100, 50, 99, 10, 400),
			cluster("bad", "eu-west-1", "healthy", 100, 50, 80, 10, 400), // below 90% SLO
			cluster("down", "ap-south-1", "unreachable", 100, 50, 99, 10, 400),
		},
		Policy: balancedPolicy(),
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// "bad" (SLO 80% < minSLO 90%) and "down" (unreachable) should be excluded.
	// Only 1 eligible cluster → no traffic directive (need ≥2).
	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			t.Error("should not produce traffic directive with <2 eligible clusters")
		}
	}
}

func TestSolve_TrafficWeights_MaxShiftClamped(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("best", "us-east-1", "healthy", 100, 10, 100, 1, 10),   // vastly better
			cluster("worst", "eu-west-1", "healthy", 100, 90, 92, 50, 800), // way worse
		},
		Policy: &globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{
				Strategy:                globalv1.StrategyBalance,
				MaxShiftPerCyclePercent: 10, // very tight clamp
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			best := d.TrafficWeights["best"]
			worst := d.TrafficWeights["worst"]
			// With 10% max shift from 50/50, max is 60/40.
			if best > 60 {
				t.Errorf("best weight %d exceeds max shift (60)", best)
			}
			if worst < 40 {
				t.Errorf("worst weight %d below min (40)", worst)
			}
			return
		}
	}
	t.Error("expected a traffic_shift directive")
}

func TestSolve_TrafficWeights_WeightsSum100(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("a", "r1", "healthy", 100, 30, 95, 10, 200),
			cluster("b", "r2", "healthy", 100, 50, 99, 15, 400),
			cluster("c", "r3", "healthy", 100, 70, 92, 8, 100),
		},
		Policy: balancedPolicy(),
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			total := int32(0)
			for _, w := range d.TrafficWeights {
				total += w
			}
			if total != 100 {
				t.Errorf("weights sum = %d, want 100", total)
			}
			return
		}
	}
	t.Error("expected a traffic_shift directive")
}

func TestSolve_SingleCluster_NoTraffic(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("alone", "us-east-1", "healthy", 100, 50, 99, 10, 400),
		},
		Policy: balancedPolicy(),
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			t.Error("should not produce traffic directive with single cluster")
		}
	}
}

// ---------------------------------------------------------------------------
// Lifecycle / hibernation tests
// ---------------------------------------------------------------------------

func TestSolve_Hibernation_IdleCluster(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("busy", "us-east-1", "healthy", 100, 60, 99, 10, 400),
			cluster("idle", "eu-west-1", "healthy", 100, 5, 99, 10, 400), // 5% util
		},
		Policy: &globalv1.GlobalPolicySpec{
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled:   true,
				MinActiveClusters:    1,
				IdleThresholdPercent: 10,
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	found := false
	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveHibernate && d.ClusterName == "idle" {
			found = true
		}
	}
	if !found {
		t.Error("expected hibernate directive for idle cluster")
	}
}

func TestSolve_Hibernation_MinActiveProtection(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("idle1", "us-east-1", "healthy", 100, 5, 99, 10, 400),
			cluster("idle2", "eu-west-1", "healthy", 100, 5, 99, 10, 400),
		},
		Policy: &globalv1.GlobalPolicySpec{
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled:   true,
				MinActiveClusters:    2,
				IdleThresholdPercent: 10,
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveHibernate {
			t.Error("should not hibernate when at min active clusters")
		}
	}
}

func TestSolve_Hibernation_ExcludedCluster(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("busy", "us-east-1", "healthy", 100, 60, 99, 10, 400),
			cluster("mgmt", "eu-west-1", "healthy", 100, 3, 99, 10, 400), // idle but excluded
			cluster("idle", "ap-south-1", "healthy", 100, 3, 99, 10, 400),
		},
		Policy: &globalv1.GlobalPolicySpec{
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled:   true,
				MinActiveClusters:    1,
				IdleThresholdPercent: 10,
				ExcludedClusters:     []string{"mgmt"},
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveHibernate && d.ClusterName == "mgmt" {
			t.Error("excluded cluster should not be hibernated")
		}
	}
}

func TestSolve_WakeUp_AllHeavy(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("busy1", "us-east-1", "healthy", 100, 85, 99, 10, 400),
			cluster("busy2", "eu-west-1", "healthy", 100, 90, 99, 10, 400),
			cluster("sleeping", "ap-south-1", "hibernating", 100, 0, 0, 0, 200),
		},
		Policy: &globalv1.GlobalPolicySpec{
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled: true,
				MinActiveClusters:  1,
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	found := false
	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveWakeUp && d.ClusterName == "sleeping" {
			found = true
		}
	}
	if !found {
		t.Error("expected wake_up directive when all active clusters are heavily loaded")
	}
}

func TestSolve_NoWakeUp_WhenCapacityAvailable(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("busy", "us-east-1", "healthy", 100, 85, 99, 10, 400),
			cluster("light", "eu-west-1", "healthy", 100, 30, 99, 10, 400), // < 80%
			cluster("sleeping", "ap-south-1", "hibernating", 100, 0, 0, 0, 200),
		},
		Policy: &globalv1.GlobalPolicySpec{
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled: true,
				MinActiveClusters:  1,
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveWakeUp {
			t.Error("should not wake up when active clusters have capacity")
		}
	}
}

func TestSolve_HibernationDisabled(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("busy", "us-east-1", "healthy", 100, 60, 99, 10, 400),
			cluster("idle", "eu-west-1", "healthy", 100, 3, 99, 10, 400),
		},
		Policy: &globalv1.GlobalPolicySpec{
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled: false,
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveHibernate || d.Type == hubgrpc.DirectiveWakeUp {
			t.Error("should not have lifecycle directives when hibernation disabled")
		}
	}
}

// ---------------------------------------------------------------------------
// Combined traffic + lifecycle
// ---------------------------------------------------------------------------

func TestSolve_CombinedDirectives(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("active1", "us-east-1", "healthy", 100, 60, 99, 10, 400),
			cluster("active2", "eu-west-1", "healthy", 100, 50, 98, 15, 200),
			cluster("idle", "ap-south-1", "healthy", 100, 3, 99, 10, 400),
		},
		Policy: &globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{
				Strategy:                 globalv1.StrategyBalance,
				MaxShiftPerCyclePercent:  25,
				MinDestinationSLOPercent: 90,
			},
			ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
				HibernationEnabled:   true,
				MinActiveClusters:    1,
				IdleThresholdPercent: 10,
			},
		},
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	hasTraffic, hasHibernate := false, false
	for _, d := range res.Directives {
		if d.Type == hubgrpc.DirectiveTrafficShift {
			hasTraffic = true
		}
		if d.Type == hubgrpc.DirectiveHibernate && d.ClusterName == "idle" {
			hasHibernate = true
		}
	}

	if !hasTraffic {
		t.Error("expected traffic_shift directive")
	}
	if !hasHibernate {
		t.Error("expected hibernate directive for idle cluster")
	}
}

// ---------------------------------------------------------------------------
// Helper / scoring tests
// ---------------------------------------------------------------------------

func TestClusterSnapshot_UtilizationPercent(t *testing.T) {
	c := &ClusterSnapshot{TotalCores: 100, UsedCores: 75}
	if util := c.UtilizationPercent(); util != 75 {
		t.Errorf("UtilizationPercent = %v, want 75", util)
	}
}

func TestClusterSnapshot_FreeCores(t *testing.T) {
	c := &ClusterSnapshot{TotalCores: 100, UsedCores: 60}
	if free := c.FreeCores(); free != 40 {
		t.Errorf("FreeCores = %v, want 40", free)
	}
}

func TestClusterSnapshot_ZeroCores(t *testing.T) {
	c := &ClusterSnapshot{TotalCores: 0, UsedCores: 0}
	if util := c.UtilizationPercent(); util != 0 {
		t.Errorf("UtilizationPercent with zeroCores = %v, want 0", util)
	}
}

func TestStrategyWeights_AllStrategies(t *testing.T) {
	strategies := []globalv1.TrafficStrategy{
		globalv1.StrategyLatency,
		globalv1.StrategyCost,
		globalv1.StrategyCarbon,
		globalv1.StrategyBalance,
	}

	for _, strat := range strategies {
		wLat, wCost, wCarbon, wSLO := strategyWeights(strat)
		sum := wLat + wCost + wCarbon + wSLO
		if sum < 0.99 || sum > 1.01 {
			t.Errorf("strategy %s: weights sum = %v, want 1.0", strat, sum)
		}
	}
}

func TestScoresToWeights_EqualScores(t *testing.T) {
	scores := []ClusterScore{
		{Name: "a", Total: 0.5},
		{Name: "b", Total: 0.5},
	}
	w := scoresToWeights(scores, 25)
	if w["a"] != 50 || w["b"] != 50 {
		t.Errorf("expected 50/50, got %v/%v", w["a"], w["b"])
	}
}

func TestScoresToWeights_ZeroScores(t *testing.T) {
	scores := []ClusterScore{
		{Name: "a", Total: 0},
		{Name: "b", Total: 0},
	}
	w := scoresToWeights(scores, 25)
	if w["a"]+w["b"] == 0 {
		t.Error("should produce non-zero weights even with zero scores")
	}
}

func TestSnapshotFromProfile(t *testing.T) {
	cp := &globalv1.ClusterProfile{}
	cp.Name = "test-cluster"
	cp.Spec.Provider = globalv1.ProviderAWS
	cp.Spec.Region = "us-east-1"
	cp.Spec.CarbonIntensityGCO2PerKWh = 400
	cp.Status.Health = globalv1.ClusterHealthy
	cp.Status.SLOCompliancePercent = 99.5
	cp.Status.HourlyCostUSD = 12.0
	cp.Status.Capacity = &globalv1.ClusterCapacityStatus{
		TotalCores: 64, UsedCores: 32,
		TotalMemoryGiB: 256, UsedMemoryGiB: 128,
		NodeCount: 8,
	}

	snap := SnapshotFromProfile(cp)
	if snap.Name != "test-cluster" {
		t.Errorf("Name = %q", snap.Name)
	}
	if snap.Provider != "aws" {
		t.Errorf("Provider = %q", snap.Provider)
	}
	if snap.TotalCores != 64 {
		t.Errorf("TotalCores = %v", snap.TotalCores)
	}
	if snap.SLOCompliancePercent != 99.5 {
		t.Errorf("SLOCompliancePercent = %v", snap.SLOCompliancePercent)
	}
}

func TestSolve_Timestamp(t *testing.T) {
	s := newSolver()
	res, err := s.Solve(&SolverInput{Policy: balancedPolicy()})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	expected := fixedNow()
	if !res.Timestamp.Equal(expected) {
		t.Errorf("Timestamp = %v, want %v", res.Timestamp, expected)
	}
}

func TestSolve_Summary_Format(t *testing.T) {
	s := newSolver()
	input := &SolverInput{
		Clusters: []*ClusterSnapshot{
			cluster("a", "r1", "healthy", 100, 50, 99, 10, 400),
			cluster("b", "r2", "healthy", 100, 50, 99, 10, 400),
		},
		Policy: balancedPolicy(),
	}

	res, err := s.Solve(input)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if res.Summary == "" {
		t.Error("summary should not be empty")
	}
	// Should contain "directives:" prefix.
	if len(res.Summary) < 12 {
		t.Errorf("summary too short: %q", res.Summary)
	}
}
