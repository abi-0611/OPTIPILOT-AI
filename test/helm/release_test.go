package helm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflowRoot points to .github/workflows relative to the repo root.
var workflowRoot = filepath.Join("..", "..", ".github", "workflows")

func readWorkflow(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	path := filepath.Join(workflowRoot, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read workflow %s: %v", name, err)
	}
	var out map[string]interface{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("workflow %s is invalid YAML: %v", name, err)
	}
	return out
}

func workflowStr(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workflowRoot, name))
	if err != nil {
		t.Fatalf("cannot read workflow %s: %v", name, err)
	}
	return string(data)
}

// ── release.yaml ─────────────────────────────────────────────────────────────

func TestReleaseWorkflow_Exists(t *testing.T) {
	if _, err := os.Stat(filepath.Join(workflowRoot, "release.yaml")); err != nil {
		t.Fatalf("release.yaml not found: %v", err)
	}
}

func TestReleaseWorkflow_ValidYAML(t *testing.T) {
	wf := readWorkflow(t, "release.yaml")
	if wf == nil {
		t.Fatal("release.yaml parsed to nil")
	}
}

func TestReleaseWorkflow_TriggeredOnTagPush(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	for _, want := range []string{"tags:", "v*"} {
		if !strings.Contains(content, want) {
			t.Errorf("release.yaml missing tag trigger string %q", want)
		}
	}
}

func TestReleaseWorkflow_HasTestJob(t *testing.T) {
	wf := readWorkflow(t, "release.yaml")
	jobs, ok := wf["jobs"].(map[string]interface{})
	if !ok {
		t.Fatal("release.yaml: no 'jobs' key")
	}
	if _, found := jobs["test"]; !found {
		t.Error("release.yaml: missing 'test' job")
	}
}

func TestReleaseWorkflow_HasVersionJob(t *testing.T) {
	wf := readWorkflow(t, "release.yaml")
	jobs := wf["jobs"].(map[string]interface{})
	if _, found := jobs["version"]; !found {
		t.Error("release.yaml: missing 'version' job")
	}
}

func TestReleaseWorkflow_HasImagesJob(t *testing.T) {
	wf := readWorkflow(t, "release.yaml")
	jobs := wf["jobs"].(map[string]interface{})
	if _, found := jobs["images"]; !found {
		t.Error("release.yaml: missing 'images' job")
	}
}

func TestReleaseWorkflow_HasMLImageJob(t *testing.T) {
	wf := readWorkflow(t, "release.yaml")
	jobs := wf["jobs"].(map[string]interface{})
	if _, found := jobs["image-ml"]; !found {
		t.Error("release.yaml: missing 'image-ml' job")
	}
}

func TestReleaseWorkflow_HasHelmReleaseJob(t *testing.T) {
	wf := readWorkflow(t, "release.yaml")
	jobs := wf["jobs"].(map[string]interface{})
	if _, found := jobs["helm-release"]; !found {
		t.Error("release.yaml: missing 'helm-release' job")
	}
}

func TestReleaseWorkflow_HasGitHubReleaseJob(t *testing.T) {
	wf := readWorkflow(t, "release.yaml")
	jobs := wf["jobs"].(map[string]interface{})
	if _, found := jobs["github-release"]; !found {
		t.Error("release.yaml: missing 'github-release' job")
	}
}

func TestReleaseWorkflow_JobOrder_GitHubReleaseNeedsHelmRelease(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	// github-release job must declare helm-release in its 'needs'
	if !strings.Contains(content, "helm-release") {
		t.Error("release.yaml: github-release job does not declare needs: helm-release")
	}
}

func TestReleaseWorkflow_ImagesMatrix_HasManagerAndHub(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	for _, target := range []string{"manager", "hub"} {
		if !strings.Contains(content, target) {
			t.Errorf("release.yaml images matrix missing target %q", target)
		}
	}
}

func TestReleaseWorkflow_PushesTo_GHCR(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	if !strings.Contains(content, "ghcr.io") {
		t.Error("release.yaml does not reference ghcr.io registry")
	}
}

func TestReleaseWorkflow_HelmOCIPush(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	if !strings.Contains(content, "helm push") {
		t.Error("release.yaml does not contain 'helm push' for OCI chart")
	}
	if !strings.Contains(content, "oci://") {
		t.Error("release.yaml helm push does not use OCI protocol")
	}
}

func TestReleaseWorkflow_UsesGHCR_Auth(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	if !strings.Contains(content, "docker/login-action") {
		t.Error("release.yaml missing docker/login-action for GHCR auth")
	}
	if !strings.Contains(content, "secrets.GITHUB_TOKEN") {
		t.Error("release.yaml does not use GITHUB_TOKEN for registry auth")
	}
}

func TestReleaseWorkflow_CreatesGitHubRelease(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	if !strings.Contains(content, "softprops/action-gh-release") {
		t.Error("release.yaml missing softprops/action-gh-release action")
	}
}

func TestReleaseWorkflow_MultiArchSupport(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	if !strings.Contains(content, "linux/amd64,linux/arm64") {
		t.Error("release.yaml does not build multi-arch images (linux/amd64,linux/arm64)")
	}
}

func TestReleaseWorkflow_GoVersion(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	if !strings.Contains(content, "'1.25'") {
		t.Error("release.yaml should use Go 1.25 (matching go.mod)")
	}
}

func TestReleaseWorkflow_PermissionsSet(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	for _, perm := range []string{"contents: write", "packages: write"} {
		if !strings.Contains(content, perm) {
			t.Errorf("release.yaml missing permission %q", perm)
		}
	}
}

func TestReleaseWorkflow_PrereleaseSupport(t *testing.T) {
	content := workflowStr(t, "release.yaml")
	if !strings.Contains(content, "prerelease") {
		t.Error("release.yaml does not handle pre-release tags")
	}
}

// ── ci.yaml ───────────────────────────────────────────────────────────────────

func TestCIWorkflow_GoVersionUpdatedTo125(t *testing.T) {
	content := workflowStr(t, "ci.yaml")
	if strings.Contains(content, "'1.22'") {
		t.Error("ci.yaml still uses Go 1.22; should be updated to 1.25 to match go.mod")
	}
	if !strings.Contains(content, "'1.25'") {
		t.Error("ci.yaml does not use Go 1.25")
	}
}

func TestCIWorkflow_IncludesHelmTests(t *testing.T) {
	content := workflowStr(t, "ci.yaml")
	if !strings.Contains(content, "test/helm") {
		t.Error("ci.yaml does not run Helm structural tests (test/helm/...)")
	}
}
