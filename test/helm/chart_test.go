// Package helm_test validates the OptiPilot Helm chart structure and YAML content.
// These tests run without a Kubernetes cluster by parsing chart templates directly.
package helm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// chartRoot is resolved at test-time relative to this file.
var chartRoot = filepath.Join("..", "..", "helm", "optipilot")

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func readYAML(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]interface{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

func fileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file %s to exist: %v", path, err)
	}
}

func fileContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("%s: expected to contain %q", path, substr)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestChartYAML_RequiredFields(t *testing.T) {
	chart := readYAML(t, filepath.Join(chartRoot, "Chart.yaml"))

	requiredFields := []string{"apiVersion", "name", "description", "type", "version", "appVersion"}
	for _, f := range requiredFields {
		if chart[f] == nil {
			t.Errorf("Chart.yaml missing required field: %s", f)
		}
	}
	if chart["name"] != "optipilot" {
		t.Errorf("Chart.yaml name=%v, want optipilot", chart["name"])
	}
	if chart["type"] != "application" {
		t.Errorf("Chart.yaml type=%v, want application", chart["type"])
	}
}

func TestChartYAML_Dependencies(t *testing.T) {
	chart := readYAML(t, filepath.Join(chartRoot, "Chart.yaml"))
	deps, ok := chart["dependencies"].([]interface{})
	if !ok || len(deps) != 3 {
		t.Fatalf("expected 3 dependencies, got %v", chart["dependencies"])
	}
	names := make([]string, 0, 3)
	for _, d := range deps {
		dep := d.(map[string]interface{})
		names = append(names, dep["name"].(string))
	}
	wantNames := []string{"cluster-agent", "ml-service", "hub"}
	for i, want := range wantNames {
		if names[i] != want {
			t.Errorf("dependency[%d] name=%s, want %s", i, names[i], want)
		}
	}
}

func TestChartYAML_DependencyAliases(t *testing.T) {
	chart := readYAML(t, filepath.Join(chartRoot, "Chart.yaml"))
	deps := chart["dependencies"].([]interface{})
	aliases := map[string]string{}
	for _, d := range deps {
		dep := d.(map[string]interface{})
		name := dep["name"].(string)
		if alias, ok := dep["alias"].(string); ok {
			aliases[name] = alias
		}
	}
	if aliases["cluster-agent"] != "clusterAgent" {
		t.Errorf("cluster-agent alias=%q, want clusterAgent", aliases["cluster-agent"])
	}
	if aliases["ml-service"] != "mlService" {
		t.Errorf("ml-service alias=%q, want mlService", aliases["ml-service"])
	}
}

func TestValuesYAML_Structure(t *testing.T) {
	values := readYAML(t, filepath.Join(chartRoot, "values.yaml"))

	topLevelKeys := []string{"global", "clusterAgent", "mlService", "hub", "ingress", "serviceMonitor"}
	for _, k := range topLevelKeys {
		if values[k] == nil {
			t.Errorf("values.yaml missing top-level key: %s", k)
		}
	}
}

func TestValuesYAML_GlobalDefaults(t *testing.T) {
	values := readYAML(t, filepath.Join(chartRoot, "values.yaml"))
	global, ok := values["global"].(map[string]interface{})
	if !ok {
		t.Fatal("values.yaml global is not a map")
	}
	if global["imageRegistry"] != "ghcr.io" {
		t.Errorf("global.imageRegistry=%v, want ghcr.io", global["imageRegistry"])
	}
	if global["journalBackend"] != "sqlite" {
		t.Errorf("global.journalBackend=%v, want sqlite", global["journalBackend"])
	}
	if global["namespace"] != "optipilot-system" {
		t.Errorf("global.namespace=%v, want optipilot-system", global["namespace"])
	}
}

func TestValuesYAML_MLServiceDisabledByDefault(t *testing.T) {
	values := readYAML(t, filepath.Join(chartRoot, "values.yaml"))
	mlService := values["mlService"].(map[string]interface{})
	if mlService["enabled"] != false {
		t.Errorf("mlService.enabled=%v, want false (opt-in)", mlService["enabled"])
	}
}

func TestValuesYAML_HubDisabledByDefault(t *testing.T) {
	values := readYAML(t, filepath.Join(chartRoot, "values.yaml"))
	hub := values["hub"].(map[string]interface{})
	if hub["enabled"] != false {
		t.Errorf("hub.enabled=%v, want false (opt-in)", hub["enabled"])
	}
}

func TestValuesYAML_ClusterAgentEnabledByDefault(t *testing.T) {
	values := readYAML(t, filepath.Join(chartRoot, "values.yaml"))
	ca := values["clusterAgent"].(map[string]interface{})
	if ca["enabled"] != true {
		t.Errorf("clusterAgent.enabled=%v, want true", ca["enabled"])
	}
}

func TestValuesYAML_ClusterAgentNameOverride(t *testing.T) {
	values := readYAML(t, filepath.Join(chartRoot, "values.yaml"))
	ca := values["clusterAgent"].(map[string]interface{})
	if ca["nameOverride"] != "cluster-agent" {
		t.Errorf("clusterAgent.nameOverride=%v, want cluster-agent", ca["nameOverride"])
	}
}

func TestValuesYAML_MLServiceNameOverride(t *testing.T) {
	values := readYAML(t, filepath.Join(chartRoot, "values.yaml"))
	mlService := values["mlService"].(map[string]interface{})
	if mlService["nameOverride"] != "ml-service" {
		t.Errorf("mlService.nameOverride=%v, want ml-service", mlService["nameOverride"])
	}
}

func TestCRDs_AllFourPresent(t *testing.T) {
	crds := []string{
		"slo.optipilot.ai_serviceobjectives.yaml",
		"policy.optipilot.ai_optimizationpolicies.yaml",
		"tenant.optipilot.ai_tenantprofiles.yaml",
		"tuning.optipilot.ai_applicationtunings.yaml",
	}
	for _, crd := range crds {
		fileExists(t, filepath.Join(chartRoot, "crds", crd))
	}
}

func TestCRDs_ValidAPIExtensionsV1(t *testing.T) {
	crds := []string{
		"slo.optipilot.ai_serviceobjectives.yaml",
		"policy.optipilot.ai_optimizationpolicies.yaml",
		"tenant.optipilot.ai_tenantprofiles.yaml",
		"tuning.optipilot.ai_applicationtunings.yaml",
	}
	for _, crd := range crds {
		m := readYAML(t, filepath.Join(chartRoot, "crds", crd))
		if m["apiVersion"] != "apiextensions.k8s.io/v1" {
			t.Errorf("%s: apiVersion=%v, want apiextensions.k8s.io/v1", crd, m["apiVersion"])
		}
		if m["kind"] != "CustomResourceDefinition" {
			t.Errorf("%s: kind=%v, want CustomResourceDefinition", crd, m["kind"])
		}
	}
}

func TestCRD_ApplicationTuning_SpecScope(t *testing.T) {
	m := readYAML(t, filepath.Join(chartRoot, "crds", "tuning.optipilot.ai_applicationtunings.yaml"))
	spec := m["spec"].(map[string]interface{})
	if spec["scope"] != "Namespaced" {
		t.Errorf("ApplicationTuning scope=%v, want Namespaced", spec["scope"])
	}
	names := spec["names"].(map[string]interface{})
	if names["kind"] != "ApplicationTuning" {
		t.Errorf("names.kind=%v, want ApplicationTuning", names["kind"])
	}
}

func TestSubChart_ClusterAgent_FilesExist(t *testing.T) {
	base := filepath.Join(chartRoot, "charts", "cluster-agent")
	files := []string{
		"Chart.yaml",
		"values.yaml",
		filepath.Join("templates", "_helpers.tpl"),
		filepath.Join("templates", "deployment.yaml"),
		filepath.Join("templates", "service.yaml"),
		filepath.Join("templates", "serviceaccount.yaml"),
		filepath.Join("templates", "clusterrole.yaml"),
		filepath.Join("templates", "configmap.yaml"),
	}
	for _, f := range files {
		fileExists(t, filepath.Join(base, f))
	}
}

func TestSubChart_MLService_FilesExist(t *testing.T) {
	base := filepath.Join(chartRoot, "charts", "ml-service")
	files := []string{
		"Chart.yaml",
		"values.yaml",
		filepath.Join("templates", "deployment.yaml"),
		filepath.Join("templates", "service.yaml"),
	}
	for _, f := range files {
		fileExists(t, filepath.Join(base, f))
	}
}

func TestSubChart_Hub_FilesExist(t *testing.T) {
	base := filepath.Join(chartRoot, "charts", "hub")
	files := []string{
		"Chart.yaml",
		"values.yaml",
		filepath.Join("templates", "deployment.yaml"),
		filepath.Join("templates", "service-grpc.yaml"),
		filepath.Join("templates", "certificate.yaml"),
	}
	for _, f := range files {
		fileExists(t, filepath.Join(base, f))
	}
}

func TestDeployment_ClusterAgent_SecurityContext(t *testing.T) {
	path := filepath.Join(chartRoot, "charts", "cluster-agent", "templates", "deployment.yaml")
	fileContains(t, path, "runAsNonRoot: true")
	fileContains(t, path, "allowPrivilegeEscalation: false")
	fileContains(t, path, `drop: ["ALL"]`)
}

func TestDeployment_ClusterAgent_HealthProbes(t *testing.T) {
	path := filepath.Join(chartRoot, "charts", "cluster-agent", "templates", "deployment.yaml")
	fileContains(t, path, "livenessProbe:")
	fileContains(t, path, "readinessProbe:")
	fileContains(t, path, "/healthz")
	fileContains(t, path, "/readyz")
}

func TestDeployment_ClusterAgent_LeaderElect(t *testing.T) {
	path := filepath.Join(chartRoot, "charts", "cluster-agent", "templates", "deployment.yaml")
	fileContains(t, path, "leader-elect")
}

func TestDeployment_MLService_HealthProbes(t *testing.T) {
	path := filepath.Join(chartRoot, "charts", "ml-service", "templates", "deployment.yaml")
	fileContains(t, path, "/v1/health")
}

func TestClusterRole_AllCRDGroups(t *testing.T) {
	path := filepath.Join(chartRoot, "charts", "cluster-agent", "templates", "clusterrole.yaml")
	groups := []string{
		"slo.optipilot.ai",
		"policy.optipilot.ai",
		"tenant.optipilot.ai",
		"tuning.optipilot.ai",
	}
	for _, g := range groups {
		fileContains(t, path, g)
	}
}

func TestNOTES_ContainsDashboardInstructions(t *testing.T) {
	path := filepath.Join(chartRoot, "templates", "NOTES.txt")
	fileContains(t, path, "port-forward")
	fileContains(t, path, "8090")
}

func TestHub_Certificate_CertManagerAPI(t *testing.T) {
	path := filepath.Join(chartRoot, "charts", "hub", "templates", "certificate.yaml")
	fileContains(t, path, "cert-manager.io/v1")
	fileContains(t, path, "mtls.enabled")
}

func TestHubService_gRPCPort(t *testing.T) {
	path := filepath.Join(chartRoot, "charts", "hub", "templates", "service-grpc.yaml")
	fileContains(t, path, "grpc")
	// Port is a Helm template value reference
	fileContains(t, path, "service.grpcPort")
}
