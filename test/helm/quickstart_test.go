package helm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quickstartContent(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "hack", "quickstart.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hack/quickstart.sh not found: %v", err)
	}
	return string(data)
}

func quickstartContains(t *testing.T, substr, reason string) {
	t.Helper()
	if !strings.Contains(quickstartContent(t), substr) {
		t.Errorf("quickstart.sh: %s (missing %q)", reason, substr)
	}
}

func TestQuickstart_Exists(t *testing.T) {
	path := filepath.Join("..", "..", "hack", "quickstart.sh")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("hack/quickstart.sh does not exist: %v", err)
	}
}

func TestQuickstart_HasBashShebang(t *testing.T) {
	content := quickstartContent(t)
	if !strings.HasPrefix(content, "#!/usr/bin/env bash") {
		t.Error("quickstart.sh must start with #!/usr/bin/env bash")
	}
}

func TestQuickstart_SetEUOPipefail(t *testing.T) {
	quickstartContains(t, "set -euo pipefail", "must use 'set -euo pipefail' for safety")
}

func TestQuickstart_HasDestroyFlag(t *testing.T) {
	quickstartContains(t, "--destroy", "must handle --destroy flag for cleanup")
}

func TestQuickstart_HasHelpFlag(t *testing.T) {
	quickstartContains(t, "--help", "must support --help / -h flag")
}

func TestQuickstart_HasBuildLocalFlag(t *testing.T) {
	quickstartContains(t, "--build-local", "must support --build-local flag for local image builds")
}

func TestQuickstart_HasPrerequisiteChecks(t *testing.T) {
	content := quickstartContent(t)
	for _, prereq := range []string{"kind", "kubectl", "helm", "docker"} {
		if !strings.Contains(content, prereq) {
			t.Errorf("quickstart.sh must check for prerequisite %q", prereq)
		}
	}
}

func TestQuickstart_HasClusterNameVariable(t *testing.T) {
	quickstartContains(t, "CLUSTER_NAME", "must use CLUSTER_NAME variable for portability")
}

func TestQuickstart_IdempotentKindCluster(t *testing.T) {
	quickstartContains(t, "kind get clusters", "must check for existing cluster before creating (idempotent)")
}

func TestQuickstart_CreatesKindCluster(t *testing.T) {
	quickstartContains(t, "kind create cluster", "must create a kind cluster")
}

func TestQuickstart_UsesKindConfigYaml(t *testing.T) {
	quickstartContains(t, "kind-config.yaml", "should use kind-config.yaml for consistent cluster topology")
}

func TestQuickstart_InstallsPrometheus(t *testing.T) {
	quickstartContains(t, "kube-prometheus-stack", "must install kube-prometheus-stack")
}

func TestQuickstart_IdempotentHelmRelease(t *testing.T) {
	quickstartContains(t, "helm status", "must check for existing helm release before installing (idempotent)")
}

func TestQuickstart_InstallsFromLocalHelmChart(t *testing.T) {
	quickstartContains(t, "helm/optipilot", "must install from the local helm/optipilot chart")
}

func TestQuickstart_MLServiceDisabledByDefault(t *testing.T) {
	quickstartContains(t, "mlService.enabled=false", "must disable ML service by default for faster demo startup")
}

func TestQuickstart_HubDisabledByDefault(t *testing.T) {
	quickstartContains(t, "hub.enabled=false", "must disable hub by default (single-cluster demo)")
}

func TestQuickstart_DeploysSampleApp(t *testing.T) {
	quickstartContains(t, "demo-api", "must deploy sample application 'demo-api'")
}

func TestQuickstart_SampleAppUsesNginx(t *testing.T) {
	quickstartContains(t, "nginx", "sample app should use nginx as a lightweight demo container")
}

func TestQuickstart_CreatesServiceObjective(t *testing.T) {
	quickstartContains(t, "ServiceObjective", "must create a ServiceObjective CR")
}

func TestQuickstart_CreatesOptimizationPolicy(t *testing.T) {
	quickstartContains(t, "OptimizationPolicy", "must create an OptimizationPolicy CR")
}

func TestQuickstart_DryRunEnabledForDemo(t *testing.T) {
	quickstartContains(t, "dryRun: true", "demo policy must use dryRun: true to avoid unintended actuation")
}

func TestQuickstart_PortForwardsDashboard(t *testing.T) {
	quickstartContains(t, "port-forward", "must port-forward the dashboard")
}

func TestQuickstart_DashboardPort8090(t *testing.T) {
	quickstartContains(t, "8090", "must port-forward to dashboard port 8090")
}

func TestQuickstart_WaitsForPodReady(t *testing.T) {
	quickstartContains(t, "condition=Ready", "must wait for manager pod Ready before port-forwarding")
}

func TestQuickstart_DeletesClusterOnDestroy(t *testing.T) {
	quickstartContains(t, "kind delete cluster", "--destroy must delete the kind cluster")
}

func TestQuickstart_PrintsLocalhostInstructions(t *testing.T) {
	quickstartContains(t, "localhost", "must print demo instructions with localhost URLs")
}

func TestQuickstart_ShowsDestroyCommand(t *testing.T) {
	content := quickstartContent(t)
	if !strings.Contains(content, "--destroy") || !strings.Contains(content, "quickstart.sh") {
		t.Error("quickstart.sh must show the --destroy cleanup command in its output")
	}
}
