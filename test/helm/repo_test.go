package helm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoFile(t *testing.T, relPath string) string {
	t.Helper()
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("file not found: %s (%v)", relPath, err)
	}
	return string(data)
}

func repoFileExists(relPath string) bool {
	root := filepath.Join("..", "..")
	_, err := os.Stat(filepath.Join(root, relPath))
	return err == nil
}

func repoContains(t *testing.T, relPath, substr, reason string) {
	t.Helper()
	content := repoFile(t, relPath)
	if !strings.Contains(content, substr) {
		t.Errorf("%s: %s (missing %q)", relPath, reason, substr)
	}
}

// ── LICENSE ──────────────────────────────────────────────────────────────────

func TestRepoPrep_LicenseExists(t *testing.T) {
	if !repoFileExists("LICENSE") {
		t.Fatal("LICENSE file not found")
	}
}

func TestRepoPrep_LicenseIsApache2(t *testing.T) {
	repoContains(t, "LICENSE", "Apache License", "LICENSE must be Apache 2.0")
	repoContains(t, "LICENSE", "Version 2.0", "LICENSE must declare Version 2.0")
}

func TestRepoPrep_LicenseCopyrightYear(t *testing.T) {
	repoContains(t, "LICENSE", "2026", "LICENSE must include copyright year")
}

func TestRepoPrep_LicenseCopyrightHolder(t *testing.T) {
	repoContains(t, "LICENSE", "OptiPilot", "LICENSE must name the copyright holder")
}

// ── README.md ─────────────────────────────────────────────────────────────────

func TestRepoPrep_ReadmeExists(t *testing.T) {
	if !repoFileExists("README.md") {
		t.Fatal("README.md not found")
	}
}

func TestRepoPrep_ReadmeHasCIBadge(t *testing.T) {
	repoContains(t, "README.md", "ci.yaml/badge.svg", "README must have CI badge")
}

func TestRepoPrep_ReadmeHasLicenseBadge(t *testing.T) {
	repoContains(t, "README.md", "Apache", "README must have license badge")
}

func TestRepoPrep_ReadmeHasFeatureTable(t *testing.T) {
	for _, feature := range []string{"SLO", "CEL", "Solver", "Actuator", "Forecast", "Fairness", "Explainability"} {
		repoContains(t, "README.md", feature, "README feature table must mention "+feature)
	}
}

func TestRepoPrep_ReadmeHasArchitectureDiagram(t *testing.T) {
	content := repoFile(t, "README.md")
	if !strings.Contains(content, "```") || !strings.Contains(content, "Cluster Agent") {
		t.Error("README must include an ASCII architecture diagram with 'Cluster Agent'")
	}
}

func TestRepoPrep_ReadmeHasQuickStart(t *testing.T) {
	repoContains(t, "README.md", "quickstart.sh", "README must reference the quickstart script")
}

func TestRepoPrep_ReadmeHasHelmInstall(t *testing.T) {
	repoContains(t, "README.md", "helm install", "README must show helm install command")
}

func TestRepoPrep_ReadmeHasCRDExamples(t *testing.T) {
	for _, crd := range []string{"ServiceObjective", "OptimizationPolicy"} {
		repoContains(t, "README.md", crd, "README must include "+crd+" CRD example")
	}
}

func TestRepoPrep_ReadmeHasContainerImages(t *testing.T) {
	repoContains(t, "README.md", "ghcr.io/optipilot-ai/optipilot", "README must list container image registry")
}

func TestRepoPrep_ReadmeHasDocsLinks(t *testing.T) {
	for _, doc := range []string{"docs/getting-started.md", "docs/installation.md", "docs/architecture.md"} {
		repoContains(t, "README.md", doc, "README must link to "+doc)
	}
}

func TestRepoPrep_ReadmeHasLicenseSection(t *testing.T) {
	repoContains(t, "README.md", "Apache 2.0", "README must mention Apache 2.0 license")
}

func TestRepoPrep_ReadmeHasContributingLink(t *testing.T) {
	repoContains(t, "README.md", "CONTRIBUTING.md", "README must link to CONTRIBUTING.md")
}

func TestRepoPrep_ReadmeHasDevelopmentSection(t *testing.T) {
	repoContains(t, "README.md", "go test", "README must include development/testing commands")
}

func TestRepoPrep_ReadmeHasMultiArchImages(t *testing.T) {
	repoContains(t, "README.md", "linux/amd64", "README must mention multi-arch image support")
}

// ── CONTRIBUTING.md ───────────────────────────────────────────────────────────

func TestRepoPrep_ContributingExists(t *testing.T) {
	if !repoFileExists("CONTRIBUTING.md") {
		t.Fatal("CONTRIBUTING.md not found")
	}
}

func TestRepoPrep_ContributingHasDevSetup(t *testing.T) {
	repoContains(t, "CONTRIBUTING.md", "Development Setup", "CONTRIBUTING must have development setup section")
}

func TestRepoPrep_ContributingHasGoVersion(t *testing.T) {
	repoContains(t, "CONTRIBUTING.md", "1.25", "CONTRIBUTING must specify Go 1.25 requirement")
}

func TestRepoPrep_ContributingHasBranchStrategy(t *testing.T) {
	repoContains(t, "CONTRIBUTING.md", "feat/", "CONTRIBUTING must document branch naming (feat/ prefix)")
}

func TestRepoPrep_ContributingHasConventionalCommits(t *testing.T) {
	repoContains(t, "CONTRIBUTING.md", "Conventional Commits", "CONTRIBUTING must reference Conventional Commits")
}

func TestRepoPrep_ContributingHasTestRequirements(t *testing.T) {
	repoContains(t, "CONTRIBUTING.md", "Testing Requirements", "CONTRIBUTING must have testing requirements section")
}

func TestRepoPrep_ContributingHasCodingStandards(t *testing.T) {
	repoContains(t, "CONTRIBUTING.md", "Coding Standards", "CONTRIBUTING must have coding standards section")
}

func TestRepoPrep_ContributingHasPRProcess(t *testing.T) {
	repoContains(t, "CONTRIBUTING.md", "Pull Request", "CONTRIBUTING must describe PR process")
}

// ── .github/ files ────────────────────────────────────────────────────────────

func TestRepoPrep_PRTemplateExists(t *testing.T) {
	if !repoFileExists(".github/PULL_REQUEST_TEMPLATE.md") {
		t.Fatal(".github/PULL_REQUEST_TEMPLATE.md not found")
	}
}

func TestRepoPrep_PRTemplateHasChecklist(t *testing.T) {
	repoContains(t, ".github/PULL_REQUEST_TEMPLATE.md", "- [ ]", "PR template must have checklist items")
}

func TestRepoPrep_PRTemplateHasTypeOfChange(t *testing.T) {
	repoContains(t, ".github/PULL_REQUEST_TEMPLATE.md", "Type of Change", "PR template must have type-of-change section")
}

func TestRepoPrep_PRTemplateHasTestingSection(t *testing.T) {
	repoContains(t, ".github/PULL_REQUEST_TEMPLATE.md", "Testing", "PR template must have testing section")
}

func TestRepoPrep_PRTemplateHasConventionalCommitNote(t *testing.T) {
	repoContains(t, ".github/PULL_REQUEST_TEMPLATE.md", "Conventional Commits", "PR template must reference Conventional Commits")
}

func TestRepoPrep_BugReportTemplateExists(t *testing.T) {
	if !repoFileExists(".github/ISSUE_TEMPLATE/bug-report.md") {
		t.Fatal(".github/ISSUE_TEMPLATE/bug-report.md not found")
	}
}

func TestRepoPrep_BugReportHasFrontmatter(t *testing.T) {
	repoContains(t, ".github/ISSUE_TEMPLATE/bug-report.md", "name: Bug Report", "bug template must have frontmatter name")
	repoContains(t, ".github/ISSUE_TEMPLATE/bug-report.md", "labels:", "bug template must have labels in frontmatter")
}

func TestRepoPrep_BugReportHasStepsToReproduce(t *testing.T) {
	repoContains(t, ".github/ISSUE_TEMPLATE/bug-report.md", "Steps to Reproduce", "bug template must ask for reproduction steps")
}

func TestRepoPrep_BugReportHasEnvironmentTable(t *testing.T) {
	repoContains(t, ".github/ISSUE_TEMPLATE/bug-report.md", "OptiPilot version", "bug template must have environment table with version field")
}

func TestRepoPrep_FeatureRequestTemplateExists(t *testing.T) {
	if !repoFileExists(".github/ISSUE_TEMPLATE/feature-request.md") {
		t.Fatal(".github/ISSUE_TEMPLATE/feature-request.md not found")
	}
}

func TestRepoPrep_FeatureRequestHasFrontmatter(t *testing.T) {
	repoContains(t, ".github/ISSUE_TEMPLATE/feature-request.md", "name: Feature Request", "feature template must have frontmatter name")
}

func TestRepoPrep_FeatureRequestHasAcceptanceCriteria(t *testing.T) {
	repoContains(t, ".github/ISSUE_TEMPLATE/feature-request.md", "Acceptance Criteria", "feature template must have acceptance criteria section")
}

func TestRepoPrep_FeatureRequestHasWillingToContributeField(t *testing.T) {
	repoContains(t, ".github/ISSUE_TEMPLATE/feature-request.md", "willing to contribute", "feature template must ask if reporter will contribute")
}
