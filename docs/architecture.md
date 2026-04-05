# Architecture

OptiPilot AI is a controller-based Kubernetes optimization platform that continuously evaluates SLOs, generates scaling decisions using a multi-objective solver, and actuates changes safely.

## System Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  Management Cluster (Hub — optional)                                        │
│  ┌──────────────────────────────────────────────────────────────┐           │
│  │  Hub Controller (cmd/hub)                                    │           │
│  │  ┌──────────────┐  ┌──────────────┐  ┌───────────────────┐  │           │
│  │  │ GlobalSolver │  │ TrafficShift │  │LifecycleManager   │  │           │
│  │  └──────────────┘  └──────────────┘  └───────────────────┘  │           │
│  │  gRPC HubServer (:50051) — spoke registration + directives   │           │
│  └──────────────────────────────────────────────────────────────┘           │
└─────────────────────────────────────────────────────────────────────────────┘
             ▲ gRPC spoke agent heartbeats + directive polling
             │
┌────────────┴────────────────────────────────────────────────────────────────┐
│  Workload Cluster (Cluster Agent — always on)                               │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────┐            │
│  │  Cluster Agent (cmd/manager)                                │            │
│  │                                                             │            │
│  │  ┌─────────────┐   ┌──────────────┐   ┌────────────────┐   │            │
│  │  │  SO Ctrl    │   │  OP Ctrl     │   │  TP Ctrl       │   │            │
│  │  │ (SLO eval)  │   │ (CEL policy) │   │ (tenant quota) │   │            │
│  │  └──────┬──────┘   └──────┬───────┘   └────────┬───────┘   │            │
│  │         │                 │                     │           │            │
│  │         └─────────────────┴──────────┬──────────┘           │            │
│  │                                      ▼                      │            │
│  │                         ┌────────────────────┐              │            │
│  │                         │  Optimization      │              │            │
│  │                         │  Solver            │              │            │
│  │                         │  (Pareto + CEL)    │              │            │
│  │                         └─────────┬──────────┘              │            │
│  │                                   ▼                         │            │
│  │                    ┌──────────────────────────┐             │            │
│  │                    │  Safety Gate             │             │            │
│  │                    │  (emergency/cooldown/CB) │             │            │
│  │                    └──────────┬───────────────┘             │            │
│  │                               ▼                             │            │
│  │           ┌────────────────────────────────────┐            │            │
│  │           │  Actuator Registry                 │            │            │
│  │           │  ┌──────────┐ ┌──────┐ ┌────────┐  │            │            │
│  │           │  │  Pod     │ │ Node │ │AppTune │  │            │            │
│  │           │  │ Actuator │ │ Act. │ │        │  │            │            │
│  │           │  └──────────┘ └──────┘ └────────┘  │            │            │
│  │           └────────────────────────────────────┘            │            │
│  │                                                             │            │
│  │  ┌──────────────┐  ┌─────────────┐  ┌───────────────────┐  │            │
│  │  │ ML Forecaster│  │  Tenant     │  │  Decision Journal  │  │            │
│  │  │  Client      │  │  Manager    │  │  (SQLite/Postgres) │  │            │
│  │  └──────────────┘  └─────────────┘  └───────────────────┘  │            │
│  │                                                             │            │
│  │  REST API + Dashboard (:8090)                               │            │
│  └─────────────────────────────────────────────────────────────┘            │
└─────────────────────────────────────────────────────────────────────────────┘
         │ scrapes                          │ deploys
         ▼                                  ▼
   Prometheus                        Kubernetes workloads
```

## Core Components

### 1. SLO Controller (`internal/controller/serviceobjective_controller.go`)

Reconciles `ServiceObjective` CRDs. On each reconcile cycle:
1. Queries Prometheus for the target metric using the built PromQL expression
2. Evaluates burn rate (multi-window: 1h/6h/3d) against the SLO target
3. Updates `status.budgetRemaining` and conditions (`SLOCompliant`, `BudgetExhausted`)
4. Triggers the optimizer controller when budget drops below threshold

### 2. Policy Engine (`internal/cel/engine.go`)

Compiles and evaluates CEL expressions from `OptimizationPolicy` CRDs. Each policy constraint is evaluated against a `CandidatePlan` at solve time. Invalid CEL rejected at admission via validating webhook.

### 3. Optimization Solver (`internal/engine/solver.go`)

Multi-objective solver pipeline:
1. **Candidate generation** — cartesian product of replica/instance/spot options, pre-warming injection (forecast-based), spot reduction candidates (risk-based)
2. **Scoring** — 4-dimensional scoring: SLO compliance (0–1), cost efficiency (0–1), carbon intensity (0–1), fairness index (0–1)
3. **CEL filtering** — all compiled policy constraints evaluated; failing candidates dropped with recorded reason
4. **Pareto selection** — non-dominated frontier with weighted sum tie-breaking
5. **Decision recording** — full causal chain persisted to SQLite/Postgres journal

### 4. Actuator Registry (`internal/actuator/`)

Three actuators, dispatched in priority order:
- **PodActuator** — patches HPA `minReplicas`/`maxReplicas`; falls back to direct Deployment scale; supports VPA annotation mode
- **NodeActuator** — patches Karpenter `NodePool` capacity/instance requirements; falls back to namespace hint annotation
- **AppTuner** — reads/writes ConfigMap keys for application-level parameter tuning; supports rolling restart trigger

All actuations pass through **SafetyGuard** (emergency stop + cooldown + 3-strike circuit breaker) and optionally through **CanaryController** (two-step 50% split with SLO-based promotion).

### 5. ML Service (`ml/`)

Python FastAPI service providing:
- `POST /v1/forecast` — demand forecaster (AutoARIMA + AutoETS ensemble via statsforecast, SeasonalNaive fallback)
- `POST /v1/spot-risk` — XGBoost spot instance interruption predictor
- Accuracy tracking with automatic fallback disablement when MAPE > 30% sustained 1h

### 6. Tenant Fairness (`internal/tenant/`)

- **Fair-share algorithm** — 3-phase: guarantee → burst → cap with reclamation
- **Jain's fairness index** — measures allocation equity across tenants
- **Noisy-neighbor detection** — identifies tenants overconsuming relative to fair share

### 7. Multi-Cluster Hub (`internal/global/`)

Optional hub controller with gRPC spoke registration. Hub runs:
- **GlobalSolver** — cross-cluster traffic weight redistribution (4 strategies: latency/cost/carbon/balanced) + cluster lifecycle management (hibernate/wake)
- **TrafficShifter** — patches Gateway API HTTPRoutes, Istio VirtualServices, or ExternalDNS DNSEndpoints
- **LifecycleManager** — drain + scale-to-zero hibernation; predictive wake-up via ML forecaster

## Data Flow

```
Prometheus metrics
    → SLO Controller (burn rate evaluation)
    → Optimizer Controller trigger
    → Solver.Solve()
        → GenerateCandidates() [+ ML pre-warming / spot candidates]
        → NewScorer().ScoreAll() [SLO, Cost, Carbon, Fairness]
        → PolicyEngine.Evaluate() [CEL constraint filtering]
        → FindParetoFront() + SelectBest()
    → DecisionRecord → Journal.Write()
    → SafetyGuard.Allow()
    → [CanaryController.Apply()]
    → ActuatorRegistry.Apply()
    → Kubernetes API (HPA / Deployment / NodePool / ConfigMap)
```

## CRD Relationships

```
ServiceObjective ──── drives ────► Optimizer (periodic loop)
OptimizationPolicy ── applied to ► All solve cycles for matching services
TenantProfile ──────── informs ──► FairShare allocation per namespace
ApplicationTuning ──── managed by► Optimizer (parameter grid search)
```

## Storage

| Backend | Use case | Config |
|---|---|---|
| SQLite (default) | Single replica, dev/small prod | `global.journalBackend: sqlite` |
| PostgreSQL | Multi-replica, production HA | `global.journalBackend: postgres` + DSN |

## Observability

All components expose Prometheus metrics on `:8080/metrics`:
- `optipilot_slo_burn_rate{service, window}` — current burn rate
- `optipilot_slo_budget_remaining_ratio{service}` — 0–1 budget remaining
- `optipilot_slo_evaluation_duration_seconds` — histogram
- `optipilot_slo_evaluation_errors_total` — counter
- `optipilot_slo_compliant{service}` — boolean gauge
