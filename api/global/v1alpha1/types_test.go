package v1alpha1_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	globalv1 "github.com/optipilot-ai/optipilot/api/global/v1alpha1"
)

// ---------------------------------------------------------------------------
// ClusterProfile — spec & enum round-trips
// ---------------------------------------------------------------------------

func TestClusterProfile_Defaults(t *testing.T) {
	cp := globalv1.ClusterProfile{
		Spec: globalv1.ClusterProfileSpec{
			Provider: globalv1.ProviderAWS,
			Region:   "us-east-1",
			Endpoint: "spoke:50051",
		},
	}
	if cp.Spec.Provider != globalv1.ProviderAWS {
		t.Fatal("wrong provider")
	}
	if cp.Status.Health != "" {
		t.Fatal("expected zero-value health")
	}
}

func TestClusterProfile_AllProviders(t *testing.T) {
	providers := []globalv1.CloudProvider{
		globalv1.ProviderAWS, globalv1.ProviderGCP, globalv1.ProviderAzure,
		globalv1.ProviderOnPrem, globalv1.ProviderOther,
	}
	for _, p := range providers {
		cp := globalv1.ClusterProfile{
			Spec: globalv1.ClusterProfileSpec{Provider: p, Region: "r", Endpoint: "e:1"},
		}
		if cp.Spec.Provider != p {
			t.Errorf("round-trip failed for provider %s", p)
		}
	}
}

func TestClusterProfile_AllHealthStatuses(t *testing.T) {
	statuses := []globalv1.ClusterHealthStatus{
		globalv1.ClusterHealthy, globalv1.ClusterDegraded,
		globalv1.ClusterUnreachable, globalv1.ClusterHibernating, globalv1.ClusterUnknown,
	}
	for _, s := range statuses {
		cp := globalv1.ClusterProfile{
			Status: globalv1.ClusterProfileStatus{Health: s},
		}
		if cp.Status.Health != s {
			t.Errorf("round-trip failed for health %s", s)
		}
	}
}

func TestClusterProfile_Capabilities(t *testing.T) {
	cp := globalv1.ClusterProfile{Spec: globalv1.ClusterProfileSpec{
		Provider: globalv1.ProviderGCP, Region: "europe-west1", Endpoint: "hub:50051",
		Capabilities: &globalv1.ClusterCapabilities{
			GPUEnabled: true, SpotEnabled: true, IstioEnabled: true, GatewayAPIEnabled: false,
			MaxNodes: 100,
		},
	}}
	if !cp.Spec.Capabilities.GPUEnabled {
		t.Fatal("expected GPUEnabled=true")
	}
	if cp.Spec.Capabilities.MaxNodes != 100 {
		t.Fatalf("expected MaxNodes=100, got %d", cp.Spec.Capabilities.MaxNodes)
	}
}

func TestClusterProfile_CostProfile(t *testing.T) {
	cp := globalv1.ClusterProfile{Spec: globalv1.ClusterProfileSpec{
		Provider: globalv1.ProviderAzure, Region: "eastus", Endpoint: "spoke:50051",
		CostProfile: &globalv1.CostProfile{
			CoreCostPerHourUSD:      "0.048",
			MemoryGiBCostPerHourUSD: "0.006",
			GPUCostPerHourUSD:       "0.90",
			SpotDiscountPercent:     70,
		},
	}}
	if cp.Spec.CostProfile.CoreCostPerHourUSD != "0.048" {
		t.Fatal("wrong core cost")
	}
	if cp.Spec.CostProfile.SpotDiscountPercent != 70 {
		t.Fatal("wrong spot discount")
	}
}

func TestClusterProfile_CarbonIntensity(t *testing.T) {
	cp := globalv1.ClusterProfile{Spec: globalv1.ClusterProfileSpec{
		Provider:                  globalv1.ProviderGCP,
		Region:                    "us-central1",
		Endpoint:                  "spoke:50051",
		CarbonIntensityGCO2PerKWh: 80.5,
	}}
	if cp.Spec.CarbonIntensityGCO2PerKWh != 80.5 {
		t.Fatalf("wrong carbon intensity: %f", cp.Spec.CarbonIntensityGCO2PerKWh)
	}
}

func TestClusterProfile_StatusWithCapacity(t *testing.T) {
	now := metav1.NewTime(time.Now())
	cp := globalv1.ClusterProfile{Status: globalv1.ClusterProfileStatus{
		Health: globalv1.ClusterHealthy,
		Capacity: &globalv1.ClusterCapacityStatus{
			TotalCores: 64, UsedCores: 42.5,
			TotalMemoryGiB: 256, UsedMemoryGiB: 180,
			NodeCount: 8,
		},
		SLOCompliancePercent: 99.7,
		HourlyCostUSD:        3.84,
		LastHeartbeat:        &now,
	}}
	if cp.Status.Capacity.NodeCount != 8 {
		t.Fatalf("expected 8 nodes, got %d", cp.Status.Capacity.NodeCount)
	}
	if cp.Status.SLOCompliancePercent != 99.7 {
		t.Fatalf("wrong SLO: %f", cp.Status.SLOCompliancePercent)
	}
	if cp.Status.LastHeartbeat.IsZero() {
		t.Fatal("expected non-zero heartbeat")
	}
}

func TestClusterProfile_Labels(t *testing.T) {
	cp := globalv1.ClusterProfile{Spec: globalv1.ClusterProfileSpec{
		Provider: globalv1.ProviderAWS, Region: "eu-west-1", Endpoint: "s:1",
		Labels: map[string]string{"env": "staging", "tier": "gold"},
	}}
	if cp.Spec.Labels["env"] != "staging" {
		t.Fatal("wrong label")
	}
	if len(cp.Spec.Labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(cp.Spec.Labels))
	}
}

// ---------------------------------------------------------------------------
// ClusterProfile — DeepCopy isolation
// ---------------------------------------------------------------------------

func TestClusterProfile_DeepCopy_Isolation(t *testing.T) {
	now := metav1.NewTime(time.Now())
	orig := &globalv1.ClusterProfile{
		Spec: globalv1.ClusterProfileSpec{
			Provider:     globalv1.ProviderAWS,
			Region:       "us-east-1",
			Endpoint:     "spoke:50051",
			Capabilities: &globalv1.ClusterCapabilities{GPUEnabled: true, MaxNodes: 50},
			CostProfile:  &globalv1.CostProfile{CoreCostPerHourUSD: "0.048"},
			Labels:       map[string]string{"env": "prod"},
		},
		Status: globalv1.ClusterProfileStatus{
			Health:               globalv1.ClusterHealthy,
			SLOCompliancePercent: 99.5,
			LastHeartbeat:        &now,
		},
	}

	clone := orig.DeepCopy()

	// Mutate the original — clone must not be affected.
	orig.Spec.Labels["env"] = "staging"
	orig.Spec.Capabilities.MaxNodes = 200
	orig.Status.SLOCompliancePercent = 50.0

	if clone.Spec.Labels["env"] != "prod" {
		t.Fatal("Labels not independent after DeepCopy")
	}
	if clone.Spec.Capabilities.MaxNodes != 50 {
		t.Fatal("Capabilities not independent after DeepCopy")
	}
	if clone.Status.SLOCompliancePercent != 99.5 {
		t.Fatal("Status not independent after DeepCopy")
	}
}

func TestClusterProfileList_DeepCopy(t *testing.T) {
	list := &globalv1.ClusterProfileList{Items: []globalv1.ClusterProfile{
		{Spec: globalv1.ClusterProfileSpec{Provider: globalv1.ProviderAWS, Region: "us-east-1", Endpoint: "a:1"}},
		{Spec: globalv1.ClusterProfileSpec{Provider: globalv1.ProviderGCP, Region: "us-central1", Endpoint: "b:1"}},
	}}
	clone := list.DeepCopy()
	if len(clone.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(clone.Items))
	}
	if clone.Items[1].Spec.Provider != globalv1.ProviderGCP {
		t.Fatal("wrong provider in cloned list")
	}
}

// ---------------------------------------------------------------------------
// GlobalPolicy — spec & enum round-trips
// ---------------------------------------------------------------------------

func TestGlobalPolicy_Defaults(t *testing.T) {
	gp := globalv1.GlobalPolicy{Spec: globalv1.GlobalPolicySpec{
		OptimizationIntervalSeconds: 60,
	}}
	if gp.Spec.OptimizationIntervalSeconds != 60 {
		t.Fatal("wrong interval")
	}
}

func TestGlobalPolicy_AllStrategies(t *testing.T) {
	strategies := []globalv1.TrafficStrategy{
		globalv1.StrategyLatency, globalv1.StrategyCost,
		globalv1.StrategyCarbon, globalv1.StrategyBalance,
	}
	for _, s := range strategies {
		gp := globalv1.GlobalPolicy{Spec: globalv1.GlobalPolicySpec{
			TrafficShifting: &globalv1.TrafficShiftingSpec{Strategy: s},
		}}
		if gp.Spec.TrafficShifting.Strategy != s {
			t.Errorf("round-trip failed for strategy %s", s)
		}
	}
}

func TestGlobalPolicy_TrafficShiftingSpec(t *testing.T) {
	gp := globalv1.GlobalPolicy{Spec: globalv1.GlobalPolicySpec{
		TrafficShifting: &globalv1.TrafficShiftingSpec{
			Strategy:                 globalv1.StrategyLatency,
			MaxShiftPerCyclePercent:  25,
			MinDestinationSLOPercent: 95.0,
			RollbackWindowSeconds:    300,
		},
	}}
	ts := gp.Spec.TrafficShifting
	if ts.MaxShiftPerCyclePercent != 25 {
		t.Fatalf("wrong max shift: %d", ts.MaxShiftPerCyclePercent)
	}
	if ts.MinDestinationSLOPercent != 95.0 {
		t.Fatalf("wrong min SLO: %f", ts.MinDestinationSLOPercent)
	}
	if ts.RollbackWindowSeconds != 300 {
		t.Fatalf("wrong rollback window: %d", ts.RollbackWindowSeconds)
	}
}

func TestGlobalPolicy_ClusterLifecycleSpec(t *testing.T) {
	gp := globalv1.GlobalPolicy{Spec: globalv1.GlobalPolicySpec{
		ClusterLifecycle: &globalv1.ClusterLifecycleSpec{
			HibernationEnabled:   true,
			MinActiveClusters:    2,
			IdleThresholdPercent: 10,
			IdleWindowMinutes:    30,
			WakeupLeadMinutes:    15,
			ExcludedClusters:     []string{"hub", "management"},
		},
	}}
	lc := gp.Spec.ClusterLifecycle
	if !lc.HibernationEnabled {
		t.Fatal("expected HibernationEnabled=true")
	}
	if lc.MinActiveClusters != 2 {
		t.Fatalf("expected MinActiveClusters=2, got %d", lc.MinActiveClusters)
	}
	if len(lc.ExcludedClusters) != 2 {
		t.Fatalf("expected 2 excluded, got %d", len(lc.ExcludedClusters))
	}
}

func TestGlobalPolicy_CrossClusterConstraints(t *testing.T) {
	gp := globalv1.GlobalPolicy{Spec: globalv1.GlobalPolicySpec{
		CrossClusterConstraints: []globalv1.CrossClusterConstraint{
			{
				Name:                 "eu-residency",
				TenantName:           "alpha",
				RequiredRegions:      []string{"europe-west1", "europe-west3"},
				ForbiddenProviders:   []globalv1.CloudProvider{globalv1.ProviderAWS},
				MaxClustersPerTenant: 3,
			},
			{Name: "no-gpu-for-bronze"},
		},
	}}
	if len(gp.Spec.CrossClusterConstraints) != 2 {
		t.Fatal("wrong constraint count")
	}
	c := gp.Spec.CrossClusterConstraints[0]
	if c.TenantName != "alpha" {
		t.Fatal("wrong tenant")
	}
	if len(c.RequiredRegions) != 2 {
		t.Fatal("wrong regions count")
	}
	if c.MaxClustersPerTenant != 3 {
		t.Fatalf("expected MaxClustersPerTenant=3, got %d", c.MaxClustersPerTenant)
	}
}

func TestGlobalPolicy_Status(t *testing.T) {
	now := metav1.NewTime(time.Now())
	gp := globalv1.GlobalPolicy{Status: globalv1.GlobalPolicyStatus{
		LastOptimizationTime: &now,
		ActiveClusters:       3,
		HibernatingClusters:  1,
		LastDirectiveSummary: "shifted 10% traffic from cluster-a to cluster-b",
	}}
	if gp.Status.ActiveClusters != 3 {
		t.Fatalf("expected 3 active, got %d", gp.Status.ActiveClusters)
	}
	if gp.Status.HibernatingClusters != 1 {
		t.Fatalf("expected 1 hibernating, got %d", gp.Status.HibernatingClusters)
	}
	if gp.Status.LastDirectiveSummary == "" {
		t.Fatal("expected non-empty summary")
	}
}

// ---------------------------------------------------------------------------
// GlobalPolicy — DeepCopy isolation
// ---------------------------------------------------------------------------

func TestGlobalPolicy_DeepCopy_Isolation(t *testing.T) {
	orig := &globalv1.GlobalPolicy{Spec: globalv1.GlobalPolicySpec{
		TrafficShifting:  &globalv1.TrafficShiftingSpec{Strategy: globalv1.StrategyCost, MaxShiftPerCyclePercent: 20},
		ClusterLifecycle: &globalv1.ClusterLifecycleSpec{HibernationEnabled: true, ExcludedClusters: []string{"hub"}},
		CrossClusterConstraints: []globalv1.CrossClusterConstraint{
			{Name: "eu-only", RequiredRegions: []string{"eu-west-1"}},
		},
	}}

	clone := orig.DeepCopy()

	// Mutate original — clone must not be affected.
	orig.Spec.TrafficShifting.MaxShiftPerCyclePercent = 50
	orig.Spec.ClusterLifecycle.ExcludedClusters[0] = "mutated"
	orig.Spec.CrossClusterConstraints[0].RequiredRegions[0] = "ap-southeast-1"

	if clone.Spec.TrafficShifting.MaxShiftPerCyclePercent != 20 {
		t.Fatal("TrafficShifting not independent")
	}
	if clone.Spec.ClusterLifecycle.ExcludedClusters[0] != "hub" {
		t.Fatal("ExcludedClusters not independent")
	}
	if clone.Spec.CrossClusterConstraints[0].RequiredRegions[0] != "eu-west-1" {
		t.Fatal("Constraints not independent")
	}
}

func TestGlobalPolicyList_DeepCopy(t *testing.T) {
	list := &globalv1.GlobalPolicyList{Items: []globalv1.GlobalPolicy{
		{Spec: globalv1.GlobalPolicySpec{OptimizationIntervalSeconds: 30}},
		{Spec: globalv1.GlobalPolicySpec{OptimizationIntervalSeconds: 60}},
	}}
	clone := list.DeepCopy()
	if len(clone.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(clone.Items))
	}
	if clone.Items[1].Spec.OptimizationIntervalSeconds != 60 {
		t.Fatal("wrong interval in copy")
	}
}

// ---------------------------------------------------------------------------
// runtime.Object interface + nil safety
// ---------------------------------------------------------------------------

func TestClusterProfile_DeepCopyObject(t *testing.T) {
	cp := &globalv1.ClusterProfile{Spec: globalv1.ClusterProfileSpec{
		Provider: globalv1.ProviderGCP, Region: "us-central1", Endpoint: "g:1",
	}}
	var obj runtime.Object = cp.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*globalv1.ClusterProfile); !ok {
		t.Fatal("DeepCopyObject did not return *ClusterProfile")
	}
}

func TestGlobalPolicy_DeepCopyObject(t *testing.T) {
	gp := &globalv1.GlobalPolicy{Spec: globalv1.GlobalPolicySpec{
		OptimizationIntervalSeconds: 60,
	}}
	var obj runtime.Object = gp.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*globalv1.GlobalPolicy); !ok {
		t.Fatal("DeepCopyObject did not return *GlobalPolicy")
	}
}

func TestNilDeepCopies(t *testing.T) {
	var cp *globalv1.ClusterProfile
	if cp.DeepCopy() != nil {
		t.Fatal("nil ClusterProfile.DeepCopy should return nil")
	}
	var gp *globalv1.GlobalPolicy
	if gp.DeepCopy() != nil {
		t.Fatal("nil GlobalPolicy.DeepCopy should return nil")
	}
	var cl *globalv1.ClusterProfileList
	if cl.DeepCopy() != nil {
		t.Fatal("nil ClusterProfileList.DeepCopy should return nil")
	}
	var gl *globalv1.GlobalPolicyList
	if gl.DeepCopy() != nil {
		t.Fatal("nil GlobalPolicyList.DeepCopy should return nil")
	}
}

// ---------------------------------------------------------------------------
// Scheme registration
// ---------------------------------------------------------------------------

func TestSchemeRegistration(t *testing.T) {
	s := runtime.NewScheme()
	if err := globalv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}
	gvk := globalv1.GroupVersion.WithKind("ClusterProfile")
	if !s.Recognizes(gvk) {
		t.Fatalf("scheme does not recognize %s", gvk)
	}
	gvk = globalv1.GroupVersion.WithKind("GlobalPolicy")
	if !s.Recognizes(gvk) {
		t.Fatalf("scheme does not recognize %s", gvk)
	}
}
