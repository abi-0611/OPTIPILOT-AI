# OptiPilot AI

[![CI](https://github.com/optipilot-ai/optipilot/actions/workflows/ci.yaml/badge.svg)](https://github.com/optipilot-ai/optipilot/actions/workflows/ci.yaml)
[![Release](https://github.com/optipilot-ai/optipilot/actions/workflows/release.yaml/badge.svg)](https://github.com/optipilot-ai/optipilot/actions/workflows/release.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/optipilot-ai/optipilot)](https://goreportcard.com/report/github.com/optipilot-ai/optipilot)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Helm Chart](https://img.shields.io/badge/Helm-OCI-blue)](https://ghcr.io/optipilot-ai/optipilot/charts/optipilot)

**OptiPilot AI** is a self-hosted, SLO-native Kubernetes optimization platform. It continuously evaluates your service-level objectives and uses a multi-objective solver — with ML-powered demand forecasting, tenant fairness, and full explainability — to recommend (or automatically apply) right-sizing, spot-to-on-demand rebalancing, and configuration tuning decisions.

---

## Features

| Pillar | What it does |
|--------|-------------|
| **SLO Evaluation** | Multi-window burn-rate model (Google SRE), PromQL-backed, alerting on budget exhaustion |
| **CEL Policy Engine** | Declarative optimization constraints with `spotRisk()`, `carbonIntensity()`, `costRate()` built-ins |
| **Multi-Objective Solver** | Pareto-optimal candidate selection across SLO compliance, cost, carbon footprint, and tenant fairness |
| **Actuators** | HPA patch, direct Deployment scale, Karpenter NodePool, ConfigMap-based app tuning, canary rollout with SLO-gated promotion |
| **ML Forecasting** | AutoARIMA + AutoETS ensemble demand forecasting; XGBoost spot interruption predictor |
| **Tenant Fairness** | Jain's fairness index, three-phase fair-share allocation, noisy-neighbor detection, per-tenant quota enforcement |
| **Explainability** | SQLite decision journal, natural-language narrator, what-if simulator, SLO-cost curve generator |
| **Multi-Cluster** | Hub-spoke architecture over gRPC (JSON codec, optional mTLS), cross-cluster traffic shifting, hibernate/wake lifecycle |
| **Parameter Tuning** | ApplicationTuning CRD for continuous bayesian-style grid search over ConfigMap parameters |

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          Management Cluster                              │
│                                                                          │
│   ┌──────────────────┐       gRPC (mTLS optional)                        │
│   │  OptiPilot Hub   │◄──────────────────────────────────────────────┐  │
│   │  GlobalPolicy    │                                                │  │
│   │  ClusterProfile  │                                                │  │
│   └──────────────────┘                                                │  │
└───────────────────────────────────────────────────────────────────────┼──┘
                                                                        │
┌──────────────── Spoke Cluster ─────────────────────────────────────────┼──┐
│                                                                        │  │
│  ┌─────────────────────────────────────────────────────────────────┐  │  │
│  │                    Cluster Agent (controller)                    │  │  │
│  │                                                                  │  │  │
│  │  SLO Evaluator ──► CEL Policy Engine ──► Multi-Objective Solver │  │  │
│  │       │                                        │                 │  │  │
│  │  Prometheus                           Decision Journal           │  │  │
│  │  (PromQL)          ML Service ◄───────(SQLite + REST API)        │──┘  │
│  │                    (FastAPI)           What-If Simulator          │     │
│  │                    Forecaster          Narrator                   │     │
│  │                    SpotPredictor                                  │     │
│  │                         │                                         │     │
│  │             ┌───────────▼────────────┐                           │     │
│  │             │       Actuators        │                           │     │
│  │             │  HPA │ Karpenter       │                           │     │
│  │             │  AppTuner │ Canary     │                           │     │
│  │             └────────────────────────┘                           │     │
│  └─────────────────────────────────────────────────────────────────┘     │
│                                                                           │
│  CRDs: ServiceObjective  OptimizationPolicy  TenantProfile  AppTuning    │
└───────────────────────────────────────────────────────────────────────────┘
```

**Data flow:** Prometheus metrics → SLO burn-rate evaluation → CEL policy filtering → candidate generation → ML forecast injection → Pareto-optimal solver → safety gate (cooldown + circuit breaker) → optional canary → actuation → decision journal → explainability API.

---

## Quick Start (5 minutes)

**Requirements:** `kind`, `kubectl`, `helm`, `docker`

```bash
git clone https://github.com/optipilot-ai/optipilot.git
cd optipilot
./hack/quickstart.sh
```

The script will:
1. Create a `kind` cluster (`optipilot-quickstart`)
2. Install Prometheus via Helm
3. Install OptiPilot from the local chart
4. Deploy a sample `demo-api` application
5. Apply a `ServiceObjective` + `OptimizationPolicy`
6. Port-forward the dashboard API to `http://localhost:8090`

**Explore decisions in real time:**

```bash
# List decisions made by the solver
curl -s http://localhost:8090/api/v1/decisions | python3 -m json.tool

# Run a what-if simulation
curl -s -X POST http://localhost:8090/api/v1/simulate \
  -H 'Content-Type: application/json' \
  -d '{"services":["demo-api"],"description":"spot ratio test"}' \
  | python3 -m json.tool

# Tear down
./hack/quickstart.sh --destroy
```

---

## Installation

### Prerequisites

- Kubernetes 1.29+
- Prometheus Operator (or `kube-prometheus-stack`)
- Helm 3.17+

### Install via Helm

```bash
helm install optipilot oci://ghcr.io/optipilot-ai/optipilot/charts/optipilot \
  --version 0.1.0 \
  --namespace optipilot-system \
  --create-namespace \
  --set global.prometheusURL=http://prometheus-operated.monitoring.svc:9090
```

### Enable ML Forecasting

```bash
helm upgrade optipilot oci://ghcr.io/optipilot-ai/optipilot/charts/optipilot \
  --namespace optipilot-system \
  --set mlService.enabled=true
```

### Enable Multi-Cluster Hub

```bash
helm upgrade optipilot oci://ghcr.io/optipilot-ai/optipilot/charts/optipilot \
  --namespace optipilot-system \
  --set hub.enabled=true \
  --set hub.mtls.enabled=true
```

See [docs/installation.md](docs/installation.md) for full production configuration, security hardening, and upgrade procedures.

---

## Custom Resources

### ServiceObjective

Defines an SLO with multi-window burn-rate alerting:

```yaml
apiVersion: slo.optipilot.ai/v1alpha1
kind: ServiceObjective
metadata:
  name: checkout-slo
  namespace: ecommerce
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: checkout-service
  objectives:
    - metric: latency_p99
      target: "200ms"
      window: "5m"
    - metric: availability
      target: "99.95%"
      window: "30d"
  errorBudget:
    total: "0.05%"
    burnRateAlerts:
      - severity: critical
        shortWindow: "2m"
        longWindow: "15m"
        factor: 14.4
  evaluationInterval: "30s"
```

### OptimizationPolicy

Declarative CEL-based optimization constraints:

```yaml
apiVersion: policy.optipilot.ai/v1alpha1
kind: OptimizationPolicy
metadata:
  name: cost-sensitive
spec:
  selector:
    matchLabels:
      tier: non-critical
  objectives:
    - name: cost
      weight: 0.6
      direction: minimize
    - name: slo_compliance
      weight: 0.4
      direction: maximize
  constraints:
    - expr: "spotRisk(candidate.instance_type, candidate.az) < 0.3"
      reason: "Avoid high-interruption spot instances"
      hard: true
    - expr: "candidate.replicas >= 2"
      reason: "Minimum 2 replicas"
      hard: true
  dryRun: false
```

See [docs/crds/](docs/crds/) for full field references for all four CRDs.

---

## Container Images

| Image | Description |
|-------|-------------|
| `ghcr.io/optipilot-ai/optipilot/manager:latest` | Cluster agent (Go, distroless, multi-arch) |
| `ghcr.io/optipilot-ai/optipilot/hub:latest` | Hub controller for multi-cluster (Go, distroless, multi-arch) |
| `ghcr.io/optipilot-ai/optipilot/ml:latest` | ML forecasting service (Python 3.11, FastAPI) |

All images are built for `linux/amd64` and `linux/arm64`.

---

## Documentation

| Doc | Description |
|-----|-------------|
| [Getting Started](docs/getting-started.md) | 5-minute kind quickstart |
| [Installation](docs/installation.md) | Production prerequisites, Helm values, security |
| [Architecture](docs/architecture.md) | System design, components, data flow |
| [CRD Reference](docs/crds/) | ServiceObjective, OptimizationPolicy, TenantProfile, ApplicationTuning |
| [API Reference](docs/api-reference.md) | REST API endpoints and schemas |
| [Configuration](docs/configuration.md) | All Helm values documented |
| [CEL Reference](docs/cel-reference.md) | CEL variables and built-in functions |
| [Guides](docs/guides/) | Tutorials: first SLO, custom policy, multi-cluster, what-if, migration |
| [Troubleshooting](docs/troubleshooting.md) | Common issues and fixes |

---

## Development

```bash
# Clone and set up
git clone https://github.com/optipilot-ai/optipilot.git && cd optipilot
go mod download

# Generate code (after API type changes)
make generate && make manifests

# Run unit tests
go test ./...

# Run Helm + docs structural tests
go test ./test/helm/... -v

# Run E2E tests (requires kind)
./hack/quickstart.sh --build-local
go test -tags e2e ./test/e2e/... -v

# Lint
golangci-lint run
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for full contribution guidelines, branch strategy, commit conventions, and PR process.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
