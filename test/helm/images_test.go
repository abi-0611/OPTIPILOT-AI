// Package helm_test validates OptiPilot container image build configuration:
// Dockerfiles, ko config, and Makefile image targets.
// These tests run without Docker by parsing file contents directly.
package helm_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var repoRoot = filepath.Join("..", "..")

func readRaw(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertContains(t *testing.T, path, substr string) {
	t.Helper()
	content := readRaw(t, path)
	if !strings.Contains(content, substr) {
		t.Errorf("%s: expected to contain %q", filepath.Base(path), substr)
	}
}

// ---------------------------------------------------------------------------
// Dockerfile — manager
// ---------------------------------------------------------------------------

func TestDockerfile_Manager_MultistageBuilder(t *testing.T) {
	path := filepath.Join(repoRoot, "Dockerfile")
	content := readRaw(t, path)

	if !strings.Contains(content, "AS builder") {
		t.Error("Dockerfile: missing multi-stage build (AS builder)")
	}
	if !strings.Contains(content, "distroless") {
		t.Error("Dockerfile: runtime stage should use distroless base image")
	}
}

func TestDockerfile_Manager_NonrootUser(t *testing.T) {
	assertContains(t, filepath.Join(repoRoot, "Dockerfile"), "65532")
}

func TestDockerfile_Manager_CGODisabled(t *testing.T) {
	assertContains(t, filepath.Join(repoRoot, "Dockerfile"), "CGO_ENABLED=0")
}

func TestDockerfile_Manager_LDFlagsStripDebug(t *testing.T) {
	assertContains(t, filepath.Join(repoRoot, "Dockerfile"), "-ldflags")
	assertContains(t, filepath.Join(repoRoot, "Dockerfile"), "-s -w")
}

func TestDockerfile_Manager_GoVersion(t *testing.T) {
	content := readRaw(t, filepath.Join(repoRoot, "Dockerfile"))
	re := regexp.MustCompile(`FROM golang:([\d.]+)`)
	m := re.FindStringSubmatch(content)
	if m == nil {
		t.Fatal("Dockerfile: no golang base image found")
	}
	// Must be Go 1.20+
	if !strings.HasPrefix(m[1], "1.2") {
		t.Errorf("Dockerfile: golang version %s, want 1.20+", m[1])
	}
}

func TestDockerfile_Manager_BuildsManagerBinary(t *testing.T) {
	assertContains(t, filepath.Join(repoRoot, "Dockerfile"), "./cmd/manager")
}

func TestDockerfile_Manager_MultiArchArgs(t *testing.T) {
	path := filepath.Join(repoRoot, "Dockerfile")
	assertContains(t, path, "TARGETOS")
	assertContains(t, path, "TARGETARCH")
}

// ---------------------------------------------------------------------------
// Dockerfile.hub
// ---------------------------------------------------------------------------

func TestDockerfile_Hub_Exists(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot, "Dockerfile.hub")); err != nil {
		t.Fatalf("Dockerfile.hub does not exist: %v", err)
	}
}

func TestDockerfile_Hub_MultistageDistroless(t *testing.T) {
	path := filepath.Join(repoRoot, "Dockerfile.hub")
	assertContains(t, path, "AS builder")
	assertContains(t, path, "distroless")
	assertContains(t, path, "65532")
}

func TestDockerfile_Hub_BuildsHubBinary(t *testing.T) {
	assertContains(t, filepath.Join(repoRoot, "Dockerfile.hub"), "./cmd/hub")
}

func TestDockerfile_Hub_LDFlagsStripDebug(t *testing.T) {
	assertContains(t, filepath.Join(repoRoot, "Dockerfile.hub"), "-s -w")
}

// ---------------------------------------------------------------------------
// ML service Dockerfile (in ml/)
// ---------------------------------------------------------------------------

func TestDockerfile_ML_Exists(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot, "ml", "Dockerfile")); err != nil {
		t.Fatalf("ml/Dockerfile does not exist: %v", err)
	}
}

func TestDockerfile_ML_MultistageSlimBase(t *testing.T) {
	path := filepath.Join(repoRoot, "ml", "Dockerfile")
	content := readRaw(t, path)
	if !strings.Contains(content, "AS builder") && !strings.Contains(content, "AS runtime") {
		t.Error("ml/Dockerfile: expected multi-stage build")
	}
	if !strings.Contains(content, "slim") && !strings.Contains(content, "python:3") {
		t.Error("ml/Dockerfile: expected Python slim base image")
	}
}

func TestDockerfile_ML_NonrootUser(t *testing.T) {
	path := filepath.Join(repoRoot, "ml", "Dockerfile")
	content := readRaw(t, path)
	// Non-root: either USER directive or uid 1001
	if !strings.Contains(content, "USER") && !strings.Contains(content, "1001") {
		t.Error("ml/Dockerfile: expected non-root USER directive")
	}
}

func TestDockerfile_ML_ExposesPort(t *testing.T) {
	path := filepath.Join(repoRoot, "ml", "Dockerfile")
	content := readRaw(t, path)
	if !strings.Contains(content, "EXPOSE") && !strings.Contains(content, "8080") {
		t.Error("ml/Dockerfile: expected EXPOSE 8080 or port 8080 reference")
	}
}

// ---------------------------------------------------------------------------
// .ko.yaml
// ---------------------------------------------------------------------------

func TestKoYAML_Exists(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot, ".ko.yaml")); err != nil {
		t.Fatalf(".ko.yaml does not exist: %v", err)
	}
}

func TestKoYAML_ValidYAML(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".ko.yaml"))
	if err != nil {
		t.Fatalf("read .ko.yaml: %v", err)
	}
	var out map[string]interface{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf(".ko.yaml invalid YAML: %v", err)
	}
}

func TestKoYAML_DistrolessBaseImage(t *testing.T) {
	assertContains(t, filepath.Join(repoRoot, ".ko.yaml"), "distroless")
}

func TestKoYAML_BothBinaries(t *testing.T) {
	content := readRaw(t, filepath.Join(repoRoot, ".ko.yaml"))
	for _, binary := range []string{"./cmd/manager", "./cmd/hub"} {
		if !strings.Contains(content, binary) {
			t.Errorf(".ko.yaml: missing build entry for %s", binary)
		}
	}
}

func TestKoYAML_MultiArchPlatforms(t *testing.T) {
	content := readRaw(t, filepath.Join(repoRoot, ".ko.yaml"))
	for _, arch := range []string{"linux/amd64", "linux/arm64"} {
		if !strings.Contains(content, arch) {
			t.Errorf(".ko.yaml: missing platform %s", arch)
		}
	}
}

func TestKoYAML_LDFlagsVersionInjection(t *testing.T) {
	content := readRaw(t, filepath.Join(repoRoot, ".ko.yaml"))
	if !strings.Contains(content, "main.version") {
		t.Error(".ko.yaml: expected -X main.version ldflags for version injection")
	}
}

// ---------------------------------------------------------------------------
// Makefile image targets
// ---------------------------------------------------------------------------

func makefileTargets(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(repoRoot, "Makefile")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open Makefile: %v", err)
	}
	defer f.Close()

	targets := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	re := regexp.MustCompile(`^([a-zA-Z_-]+):`)
	for scanner.Scan() {
		line := scanner.Text()
		m := re.FindStringSubmatch(line)
		if m != nil {
			targets[m[1]] = true
		}
	}
	return targets
}

func TestMakefile_ImageTargetsExist(t *testing.T) {
	targets := makefileTargets(t)
	required := []string{"image-manager", "image-hub", "image-ml", "images", "push", "push-manager", "push-hub", "push-ml", "image-load-kind"}
	for _, target := range required {
		if !targets[target] {
			t.Errorf("Makefile: missing target %q", target)
		}
	}
}

func TestMakefile_ImagesTargetDependsOnAll(t *testing.T) {
	content := readRaw(t, filepath.Join(repoRoot, "Makefile"))
	// 'images' target should depend on image-manager, image-hub, image-ml
	re := regexp.MustCompile(`(?m)^images:.*`)
	m := re.FindString(content)
	if m == "" {
		t.Fatal("Makefile: images target not found")
	}
	for _, dep := range []string{"image-manager", "image-hub", "image-ml"} {
		if !strings.Contains(m, dep) {
			t.Errorf("Makefile: images target missing dependency on %s (line: %s)", dep, m)
		}
	}
}

func TestMakefile_KoTarget(t *testing.T) {
	targets := makefileTargets(t)
	if !targets["ko"] {
		t.Error("Makefile: missing 'ko' tool installation target")
	}
}

func TestMakefile_RegistryVar(t *testing.T) {
	content := readRaw(t, filepath.Join(repoRoot, "Makefile"))
	if !strings.Contains(content, "REGISTRY") {
		t.Error("Makefile: missing REGISTRY variable")
	}
	if !strings.Contains(content, "ghcr.io") {
		t.Error("Makefile: REGISTRY should default to ghcr.io")
	}
}

func TestMakefile_VersionVar(t *testing.T) {
	content := readRaw(t, filepath.Join(repoRoot, "Makefile"))
	if !strings.Contains(content, "VERSION") {
		t.Error("Makefile: missing VERSION variable")
	}
	// Should derive version from git tags
	if !strings.Contains(content, "git describe") {
		t.Error("Makefile: VERSION should use 'git describe' for tag-based versioning")
	}
}

// ---------------------------------------------------------------------------
// Build-time version variables in binaries
// ---------------------------------------------------------------------------

func TestManagerMain_HasVersionVars(t *testing.T) {
	path := filepath.Join(repoRoot, "cmd", "manager", "main.go")
	content := readRaw(t, path)
	for _, v := range []string{"version", "commit", "buildDate"} {
		if !strings.Contains(content, v) {
			t.Errorf("cmd/manager/main.go: missing build-time variable %q", v)
		}
	}
}

func TestHubMain_HasVersionVars(t *testing.T) {
	path := filepath.Join(repoRoot, "cmd", "hub", "main.go")
	content := readRaw(t, path)
	for _, v := range []string{"version", "commit", "buildDate"} {
		if !strings.Contains(content, v) {
			t.Errorf("cmd/hub/main.go: missing build-time variable %q", v)
		}
	}
}

func TestGoBuilds_Clean(t *testing.T) {
	// Validate that both binaries are mentioned in build targets
	makefile := readRaw(t, filepath.Join(repoRoot, "Makefile"))
	if !strings.Contains(makefile, "./cmd/manager") {
		t.Error("Makefile: ./cmd/manager not in build target")
	}
	if !strings.Contains(makefile, "./cmd/hub") {
		t.Error("Makefile: ./cmd/hub not in build target")
	}
}
