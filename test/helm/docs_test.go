package helm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docContent reads a documentation file and returns its contents.
// Uses a distinct name to avoid collision with chart_test.go and images_test.go helpers.
func docContent(t *testing.T, relPath string) string {
	t.Helper()
	root := filepath.Join("..", "..", "docs")
	full := filepath.Join(root, relPath)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("doc file not found: %s (%v)", relPath, err)
	}
	return string(data)
}

// docContains asserts that the documentation file at relPath contains substr.
func docContains(t *testing.T, relPath, substr string) {
	t.Helper()
	content := docContent(t, relPath)
	if !strings.Contains(content, substr) {
		t.Errorf("docs/%s does not contain %q", relPath, substr)
	}
}

// ── File existence ──────────────────────────────────────────────────────────

func TestDocFiles_AllExist(t *testing.T) {
	files := []string{
		"getting-started.md",
		"installation.md",
		"architecture.md",
		"api-reference.md",
		"configuration.md",
		"cel-reference.md",
		"troubleshooting.md",
		"crds/service-objective.md",
		"crds/optimization-policy.md",
		"crds/tenant-profile.md",
		"crds/application-tuning.md",
		"guides/first-slo.md",
		"guides/custom-policy.md",
		"guides/multi-cluster.md",
		"guides/what-if.md",
		"guides/migration-cloudpilot.md",
	}
	for _, f := range files {
		f := f
		t.Run(f, func(t *testing.T) {
			t.Parallel()
			docContent(t, f) // fails if file missing
		})
	}
}

// ── Getting Started ─────────────────────────────────────────────────────────

func TestGettingStarted_HasKindCluster(t *testing.T) {
	docContains(t, "getting-started.md", "kind")
}

func TestGettingStarted_HasHelmInstallCommand(t *testing.T) {
	docContains(t, "getting-started.md", "helm install")
}

func TestGettingStarted_ReferencesSLO(t *testing.T) {
	docContains(t, "getting-started.md", "ServiceObjective")
}

func TestGettingStarted_HasCleanup(t *testing.T) {
	docContains(t, "getting-started.md", "Cleanup")
}

// ── Installation ────────────────────────────────────────────────────────────

func TestInstallation_HasPrerequisites(t *testing.T) {
	docContains(t, "installation.md", "Prerequisites")
}

func TestInstallation_HasHelmCommand(t *testing.T) {
	docContains(t, "installation.md", "helm install optipilot")
}

func TestInstallation_HasOptipilotSystemNamespace(t *testing.T) {
	docContains(t, "installation.md", "optipilot-system")
}

func TestInstallation_HasUninstallSection(t *testing.T) {
	docContains(t, "installation.md", "helm uninstall")
}

// ── Architecture ────────────────────────────────────────────────────────────

func TestArchitecture_HasDiagram(t *testing.T) {
	docContains(t, "architecture.md", "Cluster Agent")
}

func TestArchitecture_HasDataFlow(t *testing.T) {
	docContains(t, "architecture.md", "Prometheus")
}

func TestArchitecture_MentionsCEL(t *testing.T) {
	docContains(t, "architecture.md", "CEL")
}

func TestArchitecture_MentionsHub(t *testing.T) {
	docContains(t, "architecture.md", "hub")
}

// ── CRD: ServiceObjective ────────────────────────────────────────────────────

func TestCRDServiceObjective_HasAPIGroup(t *testing.T) {
	docContains(t, "crds/service-objective.md", "slo.optipilot.ai/v1alpha1")
}

func TestCRDServiceObjective_HasKind(t *testing.T) {
	docContains(t, "crds/service-objective.md", "ServiceObjective")
}

func TestCRDServiceObjective_HasSpecFields(t *testing.T) {
	docContains(t, "crds/service-objective.md", "burn rate")
}

func TestCRDServiceObjective_HasStatusSection(t *testing.T) {
	docContains(t, "crds/service-objective.md", "status.")
}

// ── CRD: OptimizationPolicy ──────────────────────────────────────────────────

func TestCRDOptimizationPolicy_HasAPIGroup(t *testing.T) {
	docContains(t, "crds/optimization-policy.md", "policy.optipilot.ai/v1alpha1")
}

func TestCRDOptimizationPolicy_HasCELSection(t *testing.T) {
	docContains(t, "crds/optimization-policy.md", "expression")
}

func TestCRDOptimizationPolicy_HasConstraints(t *testing.T) {
	docContains(t, "crds/optimization-policy.md", "constraints")
}

func TestCRDOptimizationPolicy_HasObjectives(t *testing.T) {
	docContains(t, "crds/optimization-policy.md", "objectives")
}

// ── CRD: TenantProfile ──────────────────────────────────────────────────────

func TestCRDTenantProfile_HasAPIGroup(t *testing.T) {
	docContains(t, "crds/tenant-profile.md", "tenant.optipilot.ai/v1alpha1")
}

func TestCRDTenantProfile_HasTierField(t *testing.T) {
	docContains(t, "crds/tenant-profile.md", "tier")
}

func TestCRDTenantProfile_HasFairShareSection(t *testing.T) {
	docContains(t, "crds/tenant-profile.md", "fairSharePolicy")
}

func TestCRDTenantProfile_HasResourceBudget(t *testing.T) {
	docContains(t, "crds/tenant-profile.md", "resourceBudget")
}

// ── CRD: ApplicationTuning ──────────────────────────────────────────────────

func TestCRDApplicationTuning_HasAPIGroup(t *testing.T) {
	docContains(t, "crds/application-tuning.md", "tuning.optipilot.ai/v1alpha1")
}

func TestCRDApplicationTuning_HasSafetyPolicy(t *testing.T) {
	docContains(t, "crds/application-tuning.md", "safetyPolicy")
}

func TestCRDApplicationTuning_HasOptimizerPhase(t *testing.T) {
	docContains(t, "crds/application-tuning.md", "optimizerPhase")
}

func TestCRDApplicationTuning_HasRollback(t *testing.T) {
	docContains(t, "crds/application-tuning.md", "rollback")
}

// ── CEL Reference ───────────────────────────────────────────────────────────

func TestCELRef_HasCandidateVars(t *testing.T) {
	docContains(t, "cel-reference.md", "candidate.")
}

func TestCELRef_HasSLOVars(t *testing.T) {
	docContains(t, "cel-reference.md", "slo.")
}

func TestCELRef_HasTenantVars(t *testing.T) {
	docContains(t, "cel-reference.md", "tenant.")
}

func TestCELRef_HasSpotRiskFunction(t *testing.T) {
	docContains(t, "cel-reference.md", "spotRisk")
}

func TestCELRef_HasCarbonIntensityFunction(t *testing.T) {
	docContains(t, "cel-reference.md", "carbonIntensity")
}

func TestCELRef_HasCostRateFunction(t *testing.T) {
	docContains(t, "cel-reference.md", "costRate")
}

func TestCELRef_HasMetricsVar(t *testing.T) {
	docContains(t, "cel-reference.md", "metrics[")
}

// ── API Reference ───────────────────────────────────────────────────────────

func TestAPIRef_HasDecisionsEndpoint(t *testing.T) {
	docContains(t, "api-reference.md", "/api/v1/decisions")
}

func TestAPIRef_HasWhatIfEndpoint(t *testing.T) {
	docContains(t, "api-reference.md", "/api/v1/whatif/simulate")
}

func TestAPIRef_HasSLOCostCurveEndpoint(t *testing.T) {
	docContains(t, "api-reference.md", "/api/v1/whatif/slo-cost-curve")
}

func TestAPIRef_HasHealthEndpoints(t *testing.T) {
	docContains(t, "api-reference.md", "/healthz")
}

func TestAPIRef_HasTenantsEndpoint(t *testing.T) {
	docContains(t, "api-reference.md", "/api/v1/tenants")
}

// ── Configuration ────────────────────────────────────────────────────────────

func TestConfig_HasGlobalSection(t *testing.T) {
	docContains(t, "configuration.md", "global.namespace")
}

func TestConfig_HasClusterAgentSection(t *testing.T) {
	docContains(t, "configuration.md", "clusterAgent.enabled")
}

func TestConfig_HasMLServiceSection(t *testing.T) {
	docContains(t, "configuration.md", "mlService.enabled")
}

func TestConfig_HasHubSection(t *testing.T) {
	docContains(t, "configuration.md", "hub.enabled")
}

func TestConfig_HasMTLSSection(t *testing.T) {
	docContains(t, "configuration.md", "mtls.enabled")
}

func TestConfig_HasPrometheusSection(t *testing.T) {
	docContains(t, "configuration.md", "global.prometheusURL")
}

func TestConfig_HasServiceMonitorSection(t *testing.T) {
	docContains(t, "configuration.md", "serviceMonitor.enabled")
}

// ── Guide: First SLO ────────────────────────────────────────────────────────

func TestGuideFirstSLO_HasServiceObjectiveYAML(t *testing.T) {
	docContains(t, "guides/first-slo.md", "slo.optipilot.ai/v1alpha1")
}

func TestGuideFirstSLO_HasOptimizationPolicyYAML(t *testing.T) {
	docContains(t, "guides/first-slo.md", "policy.optipilot.ai/v1alpha1")
}

func TestGuideFirstSLO_HasPortForward(t *testing.T) {
	docContains(t, "guides/first-slo.md", "kubectl port-forward")
}

func TestGuideFirstSLO_HasCleanup(t *testing.T) {
	docContains(t, "guides/first-slo.md", "kubectl delete namespace")
}

// ── Guide: Custom Policy ─────────────────────────────────────────────────────

func TestGuideCustomPolicy_HasLevels(t *testing.T) {
	docContains(t, "guides/custom-policy.md", "Level 1")
}

func TestGuideCustomPolicy_HasSpotExample(t *testing.T) {
	docContains(t, "guides/custom-policy.md", "spotRisk")
}

func TestGuideCustomPolicy_HasCarbonExample(t *testing.T) {
	docContains(t, "guides/custom-policy.md", "carbonIntensity")
}

func TestGuideCustomPolicy_HasDryRunSection(t *testing.T) {
	docContains(t, "guides/custom-policy.md", "dryRun")
}

// ── Guide: Multi-Cluster ─────────────────────────────────────────────────────

func TestGuideMultiCluster_HasHubInstallCmd(t *testing.T) {
	docContains(t, "guides/multi-cluster.md", "hub.enabled=true")
}

func TestGuideMultiCluster_HasSpokeInstallCmd(t *testing.T) {
	docContains(t, "guides/multi-cluster.md", "hub.enabled=false")
}

func TestGuideMultiCluster_HasMTLSSection(t *testing.T) {
	docContains(t, "guides/multi-cluster.md", "mTLS")
}

func TestGuideMultiCluster_HasTroubleshootingTable(t *testing.T) {
	docContains(t, "guides/multi-cluster.md", "Troubleshooting")
}

// ── Guide: What-If ───────────────────────────────────────────────────────────

func TestGuideWhatIf_HasSimulateCall(t *testing.T) {
	docContains(t, "guides/what-if.md", "whatif/simulate")
}

func TestGuideWhatIf_HasSLOCostCurve(t *testing.T) {
	docContains(t, "guides/what-if.md", "slo-cost-curve")
}

func TestGuideWhatIf_HasFeasibleField(t *testing.T) {
	docContains(t, "guides/what-if.md", "feasible")
}

func TestGuideWhatIf_HasDashboardSection(t *testing.T) {
	docContains(t, "guides/what-if.md", "Dashboard")
}

// ── Guide: Migration ─────────────────────────────────────────────────────────

func TestGuideMigration_HasConceptMapping(t *testing.T) {
	docContains(t, "guides/migration-cloudpilot.md", "ServiceObjective")
}

func TestGuideMigration_HasServiceLevelMigration(t *testing.T) {
	docContains(t, "guides/migration-cloudpilot.md", "ServiceLevel")
}

func TestGuideMigration_HasDryRunStep(t *testing.T) {
	docContains(t, "guides/migration-cloudpilot.md", "dryRun")
}

func TestGuideMigration_HasUninstallStep(t *testing.T) {
	docContains(t, "guides/migration-cloudpilot.md", "helm uninstall")
}

func TestGuideMigration_HasRollbackSection(t *testing.T) {
	docContains(t, "guides/migration-cloudpilot.md", "Rollback")
}

// ── Troubleshooting ──────────────────────────────────────────────────────────

func TestTroubleshooting_HasCRDSection(t *testing.T) {
	docContains(t, "troubleshooting.md", "CRD Not Found")
}

func TestTroubleshooting_HasPrometheusSection(t *testing.T) {
	docContains(t, "troubleshooting.md", "Prometheus")
}

func TestTroubleshooting_HasLeaderElection(t *testing.T) {
	docContains(t, "troubleshooting.md", "leader")
}

func TestTroubleshooting_HasCELSection(t *testing.T) {
	docContains(t, "troubleshooting.md", "CEL")
}

func TestTroubleshooting_HasDiagnosticsCommand(t *testing.T) {
	docContains(t, "troubleshooting.md", "kubectl get pods")
}

func TestTroubleshooting_HasMultiClusterSection(t *testing.T) {
	docContains(t, "troubleshooting.md", "Multi-Cluster")
}
