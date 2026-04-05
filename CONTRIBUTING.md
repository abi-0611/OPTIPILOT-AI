# Contributing to OptiPilot AI

Thank you for your interest in contributing! This guide covers everything you need to get a development environment running, submit a change, and pass review.

---

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Project Structure](#project-structure)
- [Making Changes](#making-changes)
- [Coding Standards](#coding-standards)
- [Testing Requirements](#testing-requirements)
- [Submitting a Pull Request](#submitting-a-pull-request)
- [Release Process](#release-process)

---

## Code of Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold these standards. Please report unacceptable behavior to the maintainers.

---

## Getting Started

### Prerequisites

| Tool | Minimum Version | Notes |
|------|----------------|-------|
| Go | 1.25 | `go install` any missing tools |
| Docker | 24+ | Required for container builds and kind |
| kind | 0.25+ | Local Kubernetes for E2E tests |
| kubectl | 1.31+ | Cluster interaction |
| Helm | 3.17+ | Chart packaging and deployment |
| golangci-lint | 1.64+ | Lint enforcement |
| controller-gen | 0.16+ | CRD and DeepCopy generation |

Install all Go-based tools via the Makefile:

```bash
make setup-tools
```

### Fork and Clone

```bash
# Fork the repository at https://github.com/optipilot-ai/optipilot
git clone https://github.com/<your-username>/optipilot.git
cd optipilot
git remote add upstream https://github.com/optipilot-ai/optipilot.git
```

---

## Development Setup

### 1. Install dependencies

```bash
go mod download
```

### 2. Generate code

Whenever you modify API types in `api/`, regenerate DeepCopy functions and CRD manifests:

```bash
make generate    # controller-gen object (DeepCopy)
make manifests   # controller-gen CRD + RBAC manifests
```

### 3. Build binaries

```bash
make build        # manager binary → bin/manager
make build-hub    # hub binary    → bin/hub
```

### 4. Run unit tests

```bash
make test                           # all unit tests (excludes e2e)
go test ./test/helm/... -v          # Helm + docs structural tests
```

### 5. Run the full stack locally

```bash
./hack/quickstart.sh                # kind + Prometheus + OptiPilot demo
./hack/quickstart.sh --destroy      # tear down
```

### 6. Build and load images into kind

```bash
make image-manager   # build manager image with ko
make image-hub       # build hub image with ko
make image-ml        # build Python ML image with Docker
kind load docker-image <image> --name optipilot-e2e
```

---

## Project Structure

```
api/
  slo/v1alpha1/           ServiceObjective CRD types + DeepCopy
  policy/v1alpha1/        OptimizationPolicy CRD types + DeepCopy
  tenant/v1alpha1/        TenantProfile CRD types + DeepCopy
  tuning/v1alpha1/        ApplicationTuning CRD types + DeepCopy
  global/v1alpha1/        ClusterProfile + GlobalPolicy types + DeepCopy
cmd/
  manager/main.go         All-in-one controller binary entry point
  hub/main.go             Hub binary entry point (multi-cluster)
internal/
  cel/                    CEL environment, PolicyEngine, custom functions
  engine/                 Solver: candidates, scorer, Pareto selector
  actuator/               HPA, Karpenter, AppTuner, SafetyGuard, Canary
  controller/             Kubernetes reconcilers
  explainability/         SQLite decision journal + narrator
  forecaster/             Go client for ML service + accuracy tracking
  metrics/                Prometheus HTTP client + custom adapter
  simulator/              What-if simulator + SLO-cost curve generator
  slo/                    PromQL builder + burn-rate evaluator
  tenant/                 Fair-share algorithm, quota, noisy-neighbor
  tuning/                 Parameter optimizer
  global/                 Hub-spoke gRPC, global solver, spoke agent
  api/                    REST API handlers (decisions, what-if, tenants)
  webhook/                Validating webhooks
ml/                       Python ML service (FastAPI, statsforecast, XGBoost)
helm/optipilot/           Helm chart with sub-charts
test/
  e2e/                    End-to-end tests (//go:build e2e, requires kind)
  helm/                   Structural tests for Helm chart, docs, scripts
  integration/            Integration tests for multi-component pipelines
docs/                     Documentation (Markdown)
hack/                     Development scripts
```

---

## Making Changes

### Branch Strategy

Use short-lived feature branches off `main`:

```bash
git fetch upstream
git checkout -b feat/my-feature upstream/main
```

Branch naming convention:

| Prefix | Use for |
|--------|---------|
| `feat/` | New features |
| `fix/` | Bug fixes |
| `docs/` | Documentation only |
| `refactor/` | Code restructuring (no behavior change) |
| `test/` | Test additions only |
| `chore/` | Build, CI, tooling |

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short description>

[optional body]

[optional footer: Fixes #123]
```

**Types:** `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`, `perf`

**Scopes:** `slo`, `policy`, `tenant`, `tuning`, `engine`, `actuator`, `ml`, `hub`, `api`, `helm`, `docs`, `ci`

**Examples:**
```
feat(slo): add multi-window burn rate alert thresholds
fix(engine): prevent NaN in Pareto dominance check when cost is zero
docs(cel): add costRate function examples for spot-aware policies
```

---

## Coding Standards

### Go

- Follow standard Go formatting — code must pass `gofmt` and `goimports`.
- All exported symbols must have Go doc comments.
- Error values must be handled; never use `_` for error returns at system boundaries.
- Use `context.Context` as the first argument for all functions that perform I/O.
- Interfaces are defined in the consuming package, not the implementing package.
- Table-driven tests are preferred. Use `t.Run(name, ...)` for subtests.
- Avoid global mutable state; inject dependencies through structs or function arguments.
- Use `sync.RWMutex` for concurrent access to shared maps/slices.

### API Types

- All new fields must have `+kubebuilder:validation:` markers.
- Float64 fields require `+kubebuilder:validation:Type=number` and the Makefile flag `CRD_OPTIONS=crd:allowDangerousTypes=true` is already set.
- Run `make generate && make manifests` after any type change.
- Do not change field names of existing CRD types — this is a breaking change.

### CEL Policies

- All custom CEL functions must be covered by unit tests in `internal/cel/`.
- New CEL variables must be documented in `docs/cel-reference.md`.

### Python (ml/)

- Follow PEP 8. Use `ruff` for formatting.
- All public functions must have type annotations (Pydantic v2 models for API boundaries).
- New endpoints must have corresponding tests in `ml/tests/`.

### Helm

- All new Helm values must be documented in `docs/configuration.md`.
- Run `helm lint ./helm/optipilot` before submitting chart changes.
- Structural tests in `test/helm/` must be updated for new chart features.

---

## Testing Requirements

All PRs **must** include tests. The CI will reject PRs where coverage of changed files drops.

### Test Layers

| Layer | Location | When Required |
|-------|----------|---------------|
| Unit | `*_test.go` alongside source | Always — for all new functions |
| Integration | `test/integration/` | For multi-component interactions |
| Helm structural | `test/helm/` | For chart, doc, or script changes |
| E2E | `test/e2e/` (build tag `e2e`) | For API surface changes |

### Running Tests

```bash
# Unit + integration (fast, no cluster)
go test ./...

# Helm + docs structural tests
go test ./test/helm/... -v

# E2E (requires kind cluster)
./hack/quickstart.sh
go test -tags e2e ./test/e2e/... -v

# With race detector
go test -race ./...
```

### Test Conventions

- Test function names: `TestTypeName_Scenario` (e.g. `TestScorer_ParetoSelectsLowestCost`)
- Table-driven tests use `[]struct{ name, input, want }` with `t.Run(tc.name, ...)`
- Use `t.Helper()` in test helpers
- Mocks are defined inline or in `*_test.go` files (no separate mock packages)
- Tests must not depend on external services; use `httptest.NewServer` for HTTP mocks

---

## Submitting a Pull Request

1. **Ensure CI passes locally:**

   ```bash
   make generate manifests
   golangci-lint run
   go test ./...
   go test ./test/helm/...
   ```

2. **Push your branch and open a PR:**

   ```bash
   git push origin feat/my-feature
   ```

   Open a PR against the `main` branch on GitHub. Fill in the pull request template completely.

3. **PR Review process:**
   - At least **1 maintainer approval** is required to merge.
   - CI must be green (unit tests, lint, helm structural tests).
   - Address all reviewer comments before requesting re-review.
   - Squash-merge is preferred for clean history; the PR title becomes the commit message.

4. **After merge:** Your branch will be deleted automatically.

---

## Release Process

Releases are automated via `.github/workflows/release.yaml`:

1. A maintainer creates and pushes a semver tag: `git tag v0.3.1 && git push origin v0.3.1`
2. The workflow runs all tests, builds multi-arch container images, packages the Helm chart, pushes to OCI registry, and creates a GitHub release with changelog.
3. Pre-release tags (e.g. `v0.4.0-rc.1`) produce pre-release GitHub releases and are not published as `latest`.

---

## Getting Help

- Open a [Discussion](https://github.com/optipilot-ai/optipilot/discussions) for questions.
- File a [Bug Report](.github/ISSUE_TEMPLATE/bug-report.md) for reproducible issues.
- File a [Feature Request](.github/ISSUE_TEMPLATE/feature-request.md) for new ideas.

Thank you for contributing to OptiPilot AI!
