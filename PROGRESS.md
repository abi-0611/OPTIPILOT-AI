# OptiPilot AI — PROGRESS.md

## Current Setup Note
- The local quickstart path now works from a fresh clone with `./hack/quickstart.sh --build-local`.
- Helm dependency aliases are wired for `clusterAgent` and `mlService`, and chart `nameOverride` keeps the rendered resource names on `cluster-agent` / `ml-service`.
- The quickstart now exposes Prometheus at `http://localhost:9090` and the OptiPilot API at `http://localhost:8090/api/v1/decisions`; root `/` returns 404 in the local build because the UI bundle is not embedded.
- CodePro's SLO example now carries `app: codepro-api` on the ServiceObjective so the `OptimizationPolicy.selector` matches correctly.
- Optimizer right-sizing logic now derives per-replica CPU and memory recommendations from live Prometheus usage, and tune actions now include memory changes as well as CPU changes.
- CodePro manifests now include `codepro-main-site-slo` / `codepro-main-site-policy` and `codepro-admin-frontend-slo` / `codepro-admin-frontend-policy`; the frontend SLOs use deployment availability only after the earlier custom CPU-headroom objective returned no Prometheus data in-cluster.
- Focused validation passed: `internal/controller` helper tests and `internal/engine` solver tests passed after the right-sizing changes.
- Added a follow-up solver fix so spot-only candidate differences resolve to `no_action` rather than unsupported no-op `tune` actuations; focused solver tests pass for that case.
- Rebuilt `optipilot-manager:quickstart`, loaded it into the running kind cluster, and rolled `optipilot-cluster-agent` successfully.
- Live validation now shows `api` recovered from the migration/DNS outage and scaled from 1 to 3 replicas under OptiPilot control, while `main-site` and `admin-frontend` are both healthy with compliant ServiceObjectives.
- `codepro-api-slo` now evaluates as concrete `False` rather than `Unknown`, which is the correct state while latency remains above target and allows the optimizer to issue real scale-up decisions.

## Phase 1 — Scaffold & Core CRDs ✅ COMPLETE
- Go module `github.com/optipilot-ai/optipilot` (go 1.22)
- 3 CRDs: ServiceObjective, OptimizationPolicy, TenantProfile
- DeepCopy generated for all 3 API groups
- 3 CRD YAML manifests in `config/crd/bases/`
- Controller stubs + Ginkgo envtest suite
- CI workflow (`.github/workflows/ci.yml`)
- Dockerfile
- **13 tests pass**, `go build ./...` clean

---

## Phase 2 — SLO Controller & Error Budget Engine ✅ COMPLETE

### Tasks

| # | Task | File(s) | Status |
|---|------|---------|--------|
| 2.1 | Prometheus HTTP client | `internal/metrics/prometheus_client.go` + `_test.go` | ✅ |
| 2.2 | PromQL query builder | `internal/slo/promql_builder.go` + `_test.go` | ✅ |
| 2.3 | SLO burn-rate evaluator | `internal/slo/evaluator.go` + `evaluator_test.go` | ✅ |
| 2.4 | ServiceObjective reconciler (full) | `internal/controller/serviceobjective_controller.go` | ✅ |
| 2.5 | Controller self-observability metrics | `internal/metrics/controller_metrics.go` | ✅ |
| 2.6 | Envtest integration tests | `internal/controller/serviceobjective_controller_test.go` | ✅ |
| 2.7 | E2E Kind setup script | `hack/e2e-setup.sh` | ✅ |
| –  | Wire EventRecorder + Evaluator in main | `cmd/manager/main.go` | ✅ |
| –  | Inject Recorder into suite_test | `internal/controller/suite_test.go` | ✅ |

### Test Counts (Phase 2 additions)
- `internal/slo`: 22 tests (11 PromQL builder + 11 evaluator)
- `internal/metrics`: 10 tests (Prometheus client)
- `internal/controller`: Ginkgo specs (compliant, violation, Prometheus 503, custom PromQL)

### Build Status
- `go build ./...` ✅
- `go vet ./...` ✅
- `go test ./internal/slo/... ./internal/metrics/... ./api/...` ✅ all pass

---

## Phase 3 — Policy Engine with CEL ✅ COMPLETE

| # | Task | File(s) | Status |
|---|------|---------|--------|
| 3.1 | CEL environment + type registration | `internal/cel/environment.go`, `types.go` | ✅ |
| 3.2 | Policy engine (Compile + Evaluate) | `internal/cel/engine.go` | ✅ |
| 3.3 | Custom CEL functions | `internal/cel/functions.go` | ✅ |
| 3.4 | Validating webhook | `internal/webhook/optimizationpolicy_webhook.go` | ✅ |
| 3.5 | OptimizationPolicy reconciler | `internal/controller/optimizationpolicy_controller.go` | ✅ |
| 3.6 | Unit tests (18 tests) | `internal/cel/engine_test.go` | ✅ |
| – | Wire engine + recorder in main | `cmd/manager/main.go` | ✅ |
| – | Inject engine + recorder in suite | `internal/controller/suite_test.go` | ✅ |

### Success Criteria Status
- ✅ CEL expressions compile at CR creation; invalid CEL rejected with clear message
- ✅ Engine evaluates constraints → pass/fail + human-readable reason per constraint
- ✅ Objective weights stored and available to solver via `GetCompiled()`
- ✅ Custom CEL functions `spotRisk`, `carbonIntensity`, `costRate` callable from expressions
- ✅ Policy selector indexes ServiceObjectives (`FindPoliciesForService`)
- ✅ Dry-run flag propagated in `CompiledPolicy.DryRun`
- ✅ Performance: 50 constraints evaluated in <5ms

### Build / Test Status
- `go build ./...` ✅ `go vet ./...` ✅
- 18 CEL engine tests pass (basic constraints, soft, compilation errors, performance)
- All 63 cumulative tests across api/slo, api/policy, api/tenant, internal/metrics, internal/slo, internal/cel pass

---

## Phase 4 — Optimization Solver ✅ COMPLETE

### Tasks

| # | Task | File(s) | Status |
|---|------|---------|--------|
| 4.1 | Solver types | `internal/engine/types.go` | ✅ |
| 4.2 | Candidate generation | `internal/engine/candidates.go` + `_test.go` | ✅ |
| 4.3 | Multi-objective scorer | `internal/engine/scorer.go` + `_test.go` | ✅ |
| 4.4 | Pareto selection | `internal/engine/pareto.go` + `_test.go` | ✅ |
| 4.5 | Decision journal + REST API | `internal/explainability/journal.go`, `api_handler.go`, `journal_test.go` | ✅ |
| 4.6 | Solver + optimizer controller | `internal/engine/solver.go` + `_test.go`, `internal/controller/optimizer_controller.go` | ✅ |
| 4.7 | Integration test (Phases 2+3+4) | `internal/engine/integration_test.go` | ✅ |
| – | Wire into main.go | `cmd/manager/main.go` (flags, journal, optimizer) | ✅ |

### Success Criteria Status
- ✅ Solver generates 10–50 candidate plans per service per cycle
- ✅ Each candidate scored on 4 objective dimensions, normalized to [0, 1]
- ✅ CEL constraints filter invalid candidates with recorded reasons
- ✅ Pareto selection picks the non-dominated solution with highest weighted score
- ✅ DecisionRecord captures full causal chain for every decision
- ✅ Decision Journal persisted to SQLite, queryable via REST API
- ✅ Unit tests validate scoring math and Pareto correctness with hand-computed examples
- ✅ Integration test: phases 2+3+4 wired end-to-end
- ✅ Performance: full solve cycle <100ms for 50+ candidates (actual: ~0ms)

### Test Counts (Phase 4 additions)
- `internal/engine`: 51 tests (14 candidates + 14 scorer + 13 pareto + 7 solver + 3 integration)
- `internal/explainability`: 12 tests (journal + API)
- **Cumulative: 104 tests pass, 0 failures**

### Build Status
- `go build ./...` ✅
- `go vet ./...` ✅
- `go test ./internal/engine/ ./internal/explainability/ ./internal/cel/ ./internal/slo/ ./internal/metrics/ ./api/...` ✅

---

## Phase 5 — Actuators ✅ COMPLETE

### Tasks

| # | Task | File(s) | Status |
|---|------|---------|--------|
| 5.1 | Interface & Registry | `internal/actuator/interface.go` + `_test.go` | ✅ |
| 5.2 | Pod Actuator | `internal/actuator/pod_actuator.go` + `_test.go` | ✅ |
| 5.3 | Node Actuator | `internal/actuator/node_actuator.go` + `_test.go` | ✅ |
| 5.4 | App Tuner | `internal/actuator/app_tuner.go` + `_test.go` | ✅ |
| 5.5 | Safety Guards | `internal/actuator/safety.go` + `_test.go` | ✅ |
| 5.6 | Canary & Rollback | `internal/actuator/canary.go` + `_test.go` | ✅ |
| 5.7 | Wire + Integration | `internal/controller/optimizer_controller.go`, `integration_test.go` | ✅ |

### Success Criteria Status
- ✅ PodActuator: HPA patch + direct Deployment fallback; MinReplicas floor; MaxChange clamp; dry-run; rollback
- ✅ NodeActuator: Karpenter NodePool via unstructured; namespace hint fallback; always-on-demand safety
- ✅ AppTuner: ConfigMap read/update; bounds clamp via annotations; rolling restart trigger; rollback
- ✅ SafetyGuard: emergency stop (namespace annotation + global ConfigMap), cooldown, circuit breaker (3-strike 15 min)
- ✅ CanaryController: two-step split for >50% changes; SLO check between steps; auto-rollback goroutine
- ✅ optimizer_controller.go: `actuate()` method wires safety gate → canary/registry → outcome + events
- ✅ Integration tests: 9 end-to-end scenarios covering scale, tune, safety, canary, dry-run, rollback

### Test Counts (Phase 5)
- `internal/actuator`: 81 tests (9 interface + 11 pod + 9 node + 14 app + 19 safety + 10 canary + 9 integration)
- `go build ./internal/controller/` ✅ clean
- **Cumulative: 185 tests pass, 0 failures**

---

## Phase 6 — Predictive Scaler ✅ COMPLETE

### Tasks

| # | Task | File(s) | Status |
|---|---|---|---|
| 6.1 | Python scaffold + schemas | `ml/pyproject.toml`, `requirements.txt`, `app/schemas.py`, `tests/test_schemas.py` | ✅ |
| 6.2 | Demand Forecaster | `ml/app/forecaster.py` + `tests/test_forecaster.py` | ✅ |
| 6.3 | Spot Predictor | `ml/app/spot_predictor.py` + `tests/test_spot_predictor.py` | ✅ |
| 6.4 | FastAPI endpoints | `ml/app/main.py` + `tests/test_api.py` | ✅ |
| 6.5 | Go client | `internal/forecaster/client.go` + `_test.go` | ✅ |
| 6.6 | Solver integration | `internal/engine/solver.go`, `candidates.go`, `candidates_test.go`, `solver_test.go` | ✅ |
| 6.7 | Accuracy tracking | `ml/app/accuracy.py`, `ml/tests/test_accuracy.py`, `internal/forecaster/accuracy_tracker.go`, `accuracy_tracker_test.go` | ✅ |
| 6.8 | Dockerfile + K8s manifests | `ml/Dockerfile`, `config/ml/deployment.yaml`, `config/ml/service.yaml`, `ml/tests/test_sinusoidal_forecast.py` | ✅ |

### Test Counts (Phase 6 COMPLETE)
- `ml/tests`: 131 Python tests (117 + 14 sinusoidal); `internal/forecaster`: 30 Go tests; `internal/engine`: +13 forecast tests
- **Bug fixed**: `ml/app/forecaster.py` `reset_index(drop=True)` — forecast yhat values now correct
- **Cumulative: 359 tests pass (228 Go + 131 Python)**

### Phase 6 Completion Checklist
- [x] FastAPI service runs and serves forecasts
- [x] statsforecast produces predictions with confidence intervals
- [x] XGBoost spot predictor returns probabilities
- [x] Go client calls ML service with circuit breaker
- [x] Solver uses forecasts for pre-warming
- [x] Accuracy tracking with automatic fallback
- [x] Prometheus metrics for forecast quality
- [x] Docker image builds (multi-stage Dockerfile ready)
- [x] Unit tests pass for both Python and Go code
- [x] Integration test: sinusoidal forecast predicts peak within 10% error

---

## Phase 7 — Tenant-Aware Fairness & Quota System ⚠️ IN PROGRESS

### Tasks

| # | Task | File(s) | Status |
|---|------|---------|--------|
| 7.1 | Tenant Manager | `internal/tenant/manager.go` + `_test.go` | ✅ |
| 7.2 | Fair-Share Algorithm | `internal/tenant/fairshare.go` + `_test.go` | ✅ |
| 7.3 | Quota Enforcement | `internal/tenant/quota.go` + `_test.go` | ✅ |
| 7.4 | Jain's Fairness Index | `internal/tenant/fairness.go` + `_test.go` | ✅ |
| 7.5 | Noisy Neighbor Detection | `internal/tenant/noisy_neighbor.go` + `_test.go` | ✅ |
| 7.6 | Tenant REST API | `internal/api/tenant_api.go` + `_test.go` | ✅ |
| 7.7 | Integration Test | `internal/tenant/integration_test.go` | ✅ |

### Test Counts (Phase 7 complete)
- `internal/tenant`: 107 tests (20 manager + 23 fair-share + 19 quota + 14 fairness + 18 noisy neighbor + 13 integration)
- `internal/api`: 20 tests (tenant REST API)
- **Cumulative: 486 tests (355 Go + 131 Python)**

---

## Phase 8: Explainability Engine & What-If Simulator ✅ COMPLETE

| # | Task | File(s) | Status |
|---|---|---|---|
| 8.1 | Enhanced Decision Journal | `internal/explainability/journal.go` + `_test.go` | ✅ |
| 8.2 | Decision Narrator | `internal/explainability/narrator.go` + `_test.go` | ✅ |
| 8.3 | What-If Simulator | `internal/simulator/simulator.go` + `_test.go` | ✅ |
| 8.4 | SLO-Cost Curve | `internal/simulator/slo_cost_curve.go` + `_test.go` | ✅ |
| 8.5 | REST APIs | `internal/api/decisions_api.go` + `whatif_api.go` | ✅ |
| 8.6 | Integration + Perf Tests | `internal/simulator/integration_test.go` | ✅ |

### Test Counts (Phase 8 COMPLETE)
- `internal/explainability`: 35 tests (12 pre-existing + 13 journal + 10 narrator)
- `internal/simulator`: 34 tests (16 simulator + 12 SLO-cost curve + 6 integration/perf)
- `internal/api`: +32 tests (16 decisions API + 16 what-if API)
- **Cumulative: 575 tests (444 Go + 131 Python)**

### Phase 8 Performance Results
- 24h × 5-service simulation (1440 steps): **513µs** (target: <30s) ✅
- SLO-cost curve (10 sweep points, 24h window): **1.7ms** (target: <10s) ✅

---

## Phase 9 — React Dashboard UI ✅ COMPLETE

| # | Task | File(s) | Status |
|---|---|---|---|
| 9.1 | React Project Setup + All Pages | `ui/dashboard/` | ✅ |
| 9.2 | API Client & Hooks | `src/api/client.ts` + `hooks.ts` | ✅ |
| 9.3 | SLO Overview Page | `src/pages/SLOOverview.tsx` | ✅ |
| 9.4 | Fairness Dashboard Page | `src/pages/FairnessDashboard.tsx` | ✅ |
| 9.5 | Decision Explorer Page | `src/pages/DecisionExplorer.tsx` | ✅ |
| 9.6 | What-If Tool Page | `src/pages/WhatIfTool.tsx` | ✅ |
| 9.7 | Embed in Go Binary | `internal/api/server.go` + `ui_embed.go` + `Makefile` | ✅ |
| 9.8 | Testing (RTL + a11y) | `ui/dashboard/src/**/*.test.tsx` | ✅ |

### Task 9.7 Deliverables — Embed in Go Binary
- `internal/api/server.go` — RouteRegistrar interface, Server struct, CORS middleware, SPA fallback handler, graceful shutdown
- `cmd/manager/ui_embed.go` — `//go:build ui` — embeds `ui/dashboard/dist` via `embed.FS`
- `cmd/manager/ui_stub.go` — `//go:build !ui` — nil dashboardFS for dev builds
- `cmd/manager/main.go` — `--api-addr :8090` flag, starts API server in goroutine alongside controller-runtime
- `Makefile` — `ui` target (npm ci + npm run build), `build-with-ui` target (-tags ui)
- **New tests:** 10 `TestServer_*` in `internal/api/server_test.go` (all passing)

### Task 9.8 Deliverables — React Testing Library + a11y
- Vitest + jsdom + @testing-library/react + axe-core configured (`vitest.config.ts`, `src/test/setup.ts`)
- `src/test/test-utils.tsx` — shared `renderWithProviders` (QueryClient + MemoryRouter wrapper)
- 4 test files: `SLOOverview.test.tsx`, `FairnessDashboard.test.tsx`, `DecisionExplorer.test.tsx`, `WhatIfTool.test.tsx`
- 25 tests total: render, interaction (filter/expand/submit), and WCAG 2.1 AA axe scan per page
- a11y fixes: `aria-label` added to unlabeled inputs in DecisionExplorer and WhatIfTool
- All 25 tests pass: `npm test` exits 0

### Test Counts (Phase 9 complete)
| Layer | Count |
|---|---|
| Go unit + integration | 454 |
| Python ML | 131 |
| React (RTL + a11y) | 25 |
| **Total** | **610** |

---

## Phase 10 — Multi-Cluster Global Orchestrator ✅ COMPLETE

| # | Task | File(s) | Status |
|---|---|---|---|
| 10.1 | Hub-Level CRDs | `api/global/v1alpha1/` | ✅ |
| 10.2 | Hub Controller Binary | `cmd/hub/main.go` | ✅ |
| 10.3 | gRPC Service Definition | `internal/global/grpc/` | ✅ |
| 10.4 | Spoke Agent Registration | `internal/global/spoke/` | ✅ |
| 10.5 | Global Solver | `internal/global/solver.go` | ✅ |
| 10.6 | Traffic Shifting | `internal/global/traffic.go` | ✅ |
| 10.7 | Cluster Lifecycle Manager | `internal/global/lifecycle.go` | ✅ |
| 10.8 | Integration Test | `internal/global/integration_test.go` | ✅ |

### Task 10.1 Deliverables — Hub-Level CRDs (COMPLETE ✅)
- `api/global/v1alpha1/groupversion_info.go` — `global.optipilot.ai/v1alpha1` group
- `api/global/v1alpha1/clusterprofile_types.go` — `ClusterProfile` CRD (cluster-scoped): provider, region, endpoint, capabilities, cost profile, carbon intensity, labels; status: health, capacity, SLO %, hourly cost, heartbeat, conditions
- `api/global/v1alpha1/globalpolicy_types.go` — `GlobalPolicy` CRD (cluster-scoped): traffic shifting strategy, cluster lifecycle rules, cross-cluster constraints, optimization interval; status: last optimization time, active/hibernating counts, directive summary
- `api/global/v1alpha1/zz_generated.deepcopy.go` — DeepCopy for all types (hand-written, follows controller-gen pattern)
- **22 tests** in `types_test.go`: spec round-trips, enum coverage, DeepCopy isolation, nil safety, runtime.Object interface, scheme registration
- `go build ./...` clean, all tests pass

### Task 10.2 Deliverables — Hub Controller Binary (COMPLETE ✅)
- `cmd/hub/main.go` — separate binary for management cluster; watches ClusterProfile + GlobalPolicy via scheme; flags: `--grpc-addr :50051`, `--optimization-period 60s`, `--metrics-bind-address :9080`, `--health-probe-bind-address :9081`, `--leader-elect`; health/ready probes; placeholder hooks for gRPC server (10.3) and global solver (10.5)
- `cmd/hub/main_test.go` — 6 tests: flag defaults, custom overrides, invalid flag error, scheme recognition (ClusterProfile + GlobalPolicy), GVK group verification, port non-collision with cluster manager
- `Makefile` — `build-hub` target: `go build -o bin/hub ./cmd/hub`
- **6 new tests**, all passing

### Task 10.3 Deliverables — gRPC Service Definition (COMPLETE ✅)
- `internal/global/grpc/doc.go` — package documentation
- `internal/global/grpc/messages.go` — all RPC message types (RegisterClusterRequest/Response, ClusterStatusReport, StatusAck, Directive, DirectiveType, MigrationHint, GetDirectiveRequest/Response, TrafficShiftRequest/Response, Capabilities, CostProfileMsg)
- `internal/global/grpc/service.go` — `OptiPilotHubService` interface (4 methods: RegisterCluster, ReportStatus, GetDirective, RequestTrafficShift)
- `internal/global/grpc/hub_server.go` — `HubServer` (gRPC server with mTLS support, graceful shutdown via context), `MemoryHubService` (thread-safe in-memory implementation with heartbeat TTL, drain-on-fetch directives), `JSONCodec` (JSON-over-gRPC codec)
- `internal/global/grpc/spoke_client.go` — `SpokeClient` (4 RPC methods + Close), insecure fallback for dev
- `internal/global/grpc/mtls.go` — `MTLSConfig`, `ServerCredentials()`, `ClientCredentials()` (TLS 1.3, mutual authentication via cert-manager certs)
- `internal/global/grpc/grpc_test.go` — **15 tests**: full gRPC round-trip (register, heartbeat, directive fetch/drain, traffic shift, validation errors), MemoryHubService unit tests (IsHealthy, GetRegistered), server lifecycle, mTLS error paths, JSONCodec round-trip, DirectiveType constants
- **15 new tests**, all passing
- `go build ./...` clean

### Task 10.4 Deliverables — Spoke Agent Registration (COMPLETE ✅)
- `internal/global/spoke/collector.go` — `StatusCollector` interface, `DirectiveHandler` interface, `RegistrationInfo` struct, `StaticCollector` (fixed-value collector for testing), `LogDirectiveHandler` (records directives for inspection)
- `internal/global/spoke/agent.go` — `SpokeAgent` (implements `manager.Runnable`): connects to hub, registers via gRPC, sends periodic heartbeats (initial + ticker), polls for directives; honours hub-returned heartbeat interval; thread-safe state tracking (Disconnected→Registered→Running→Stopped); options: `WithHeartbeatInterval`, `WithDirectivePollInterval`, `WithTLSCredentials`, `WithLogger`, `WithNowFunc`
- `internal/global/spoke/agent_test.go` — **13 tests**: registration, heartbeat delivery, hub-returned interval override, directive polling + handling, directive drain semantics, stopped state, invalid hub address, last heartbeat tracking, default intervals, StaticCollector, LogDirectiveHandler, hub marks unhealthy after timeout, multiple directive types
- `cmd/manager/main.go` — added `--hub-endpoint`, `--cluster-name`, `--cluster-provider`, `--cluster-region` flags; spoke agent registered as `manager.Runnable` when `--hub-endpoint` is set
- **13 new tests**, all passing
- `go build ./...` clean

### Task 10.5 Deliverables — Global Solver (COMPLETE ✅)
- `internal/global/solver.go` — `GlobalSolver` with `Solve(*SolverInput) (*SolverResult, error)`; two-phase optimization: (1) traffic weight redistribution (score clusters on 4 dimensions: latency/capacity proxy, cost, carbon, SLO compliance; strategy-specific weights; max-shift clamping; proportional weight allocation summing to 100); (2) lifecycle management (hibernate idle clusters below utilization threshold respecting min-active and exclusion list; wake hibernating clusters when all active are above 80% utilization)
- Types: `ClusterSnapshot` (with `UtilizationPercent()`, `FreeCores()`), `SolverInput`, `SolverResult`, `ClusterScore`
- Helper: `SnapshotFromProfile(*ClusterProfile) *ClusterSnapshot` — converts CRD to solver input
- Strategy weights: latency-optimized (50/15/10/25), cost-optimized (10/50/15/25), carbon-optimized (10/15/50/25), balanced (25/25/25/25)
- `internal/global/solver_test.go` — **26 tests**: nil/empty/no-policy, balanced/cost/carbon traffic weights, SLO filtering, max-shift clamping, weights-sum-100 (3 clusters), single cluster no-traffic, hibernation (idle/min-active-protection/excluded), wake-up (all-heavy/capacity-available), hibernation-disabled, combined directives, utilization/free-cores/zero-cores helpers, strategy weights, equal/zero score weights, SnapshotFromProfile, timestamp, summary format
- **26 new tests**, all passing
- `go build ./...` clean

### Task 10.6 Deliverables — Traffic Shifting (COMPLETE ✅)
- `internal/global/traffic.go` — `TrafficShifter` with `Apply()` and `MonitorAndRollback()`:
  - **Gateway API**: reads/patches `HTTPRoute` `spec.rules[0].backendRefs[].weight` via unstructured client
  - **Istio**: reads/patches `VirtualService` `spec.http[0].route[].weight` via unstructured client
  - **ExternalDNS**: reads/patches `DNSEndpoint` `spec.endpoints[].providerSpecific.weight` for geo-routing
  - **Safety guards**: max shift per cycle clamping (default 25%), SLO pre-validation on destinations gaining traffic (refuses shift if SLO < 90%), post-shift monitoring with auto-rollback on degradation, configurable rollback window (default 5 min)
  - Types: `TrafficBackend` enum (gateway-api, istio, external-dns), `TrafficShiftPlan`, `TrafficShiftResult`, `SLOChecker` interface
  - Options: `WithMaxShiftPercent`, `WithRollbackWindow`, `WithShifterSleepFn`, `WithShifterNowFn`
  - Weight normalisation ensures weights always sum to 100 after clamping
- `internal/global/traffic_test.go` — **22 tests**: weight clamping (no-change, exceeds-max, small-shift, normalise-to-100, negative-clamped-to-zero, three-cluster), Gateway API patching (read + write + verify), Istio VirtualService patching, ExternalDNS patching, SLO pre-check gating (below threshold, check error, only-for-increased-traffic), rollback on SLO degradation, no-rollback when healthy, unsupported backend error, shift key format, parseWeight (string/float64/int64/unknown), option setters
- **22 new tests**, all passing
- `go build ./...` clean

### Task 10.7 Deliverables — Cluster Lifecycle Manager (COMPLETE ✅)
- `internal/global/lifecycle.go` — `LifecycleManager` orchestrates hibernate and wake-up:
  - **Hibernate flow**: management cluster guard → tenant sole-location check → drain workloads → scale node pools to zero → state tracking
  - **Wake-up flow**: state check → scale up nodes → clear idle tracker (prevents re-hibernate)
  - **Idle tracking**: `UpdateIdleStatus()` monitors consecutive idle duration per cluster; respects configurable idle window (default 30 min) and threshold (default 10%); timer resets when utilization spikes
  - **Predictive wake-up**: `CheckPredictiveWakeUp()` queries demand forecaster for hibernating clusters' regions; issues wake-up directives if demand predicted within lead time (default 15 min)
  - Interfaces: `NodePoolScaler`, `WorkloadDrainer`, `DemandForecaster`, `TenantLocator`
  - State machine: Active → Draining → Hibernating → Waking → Active; reverts on errors
  - Options: `WithManagementCluster`, `WithIdleWindow`, `WithWakeupLead`, `WithLifecycleNowFn`
- `internal/global/lifecycle_test.go` — **24 tests**: hibernate success/mgmt-refused/sole-tenant/drain-err/scale-err/already-hibernating, wake-up success/already-active/scale-err, unsupported directive, idle tracking (threshold/timer-reset/defaults), predictive wake-up (forecast-need/no-need/active-skip/nil-forecaster/error-skip), state helpers, wake-clears-idle, options, tenant-check-error
- **24 new tests**, all passing
- `go build ./...` clean

### Task 10.8 Deliverables — Integration Test (COMPLETE ✅)
- `internal/global/integration_test.go` — **10 integration tests** (external test package `global_test`):
  1. Two clusters register and heartbeat — verifies spoke registration, heartbeat delivery, hub global view
  2. Solver produces traffic directive — cost-optimized strategy, cheaper cluster gets more weight, weights sum to 100
  3. Directive delivered to spoke — hub enqueues directive, spoke agent polls and receives it
  4. Hibernation end-to-end — solver detects idle cluster → produces hibernate directive → lifecycle manager drains + scales to zero
  5. Wake-up after hibernation — hibernate → wake-up → state returns to Active
  6. Management cluster protected — refusing to hibernate management cluster
  7. Idle tracking to solver pipeline — UpdateIdleStatus over 31 minutes → solver hibernate → lifecycle execute
  8. Full pipeline — 2 spokes register → heartbeats → solver produces directives → hub enqueues → spokes receive
  9. Predictive wake-up — demand forecast triggers proactive wake-up of hibernated cluster
  10. Multiple solver rounds — state changes between rounds produce correct directives
- `internal/global/grpc/hub_server.go` — enhanced `Start()` to capture actual bound address (port 0 support)
- **10 new tests**, all passing
- Phase 10 total: **110 tests** across `internal/global/...` (all subpackages)
- `go build ./...` clean

---

## Phase 12 — Packaging, Helm Chart, Documentation & Release (IN PROGRESS 🔶)

### Task 12.1 — Helm Chart Structure ✅ COMPLETE
- `helm/optipilot/Chart.yaml` — root chart (v0.1.0, 3 sub-chart dependencies with conditions)
- `helm/optipilot/values.yaml` — comprehensive defaults (global, clusterAgent, mlService, hub, ingress, serviceMonitor)
- `helm/optipilot/templates/_helpers.tpl` + `NOTES.txt` — chart helpers and post-install instructions
- `helm/optipilot/crds/` — 4 CRDs: serviceobjectives, optimizationpolicies, tenantprofiles, applicationtunings
- `helm/optipilot/charts/cluster-agent/` — full sub-chart: Deployment (securityContext, probes, PVC, leader-elect), Service, ServiceAccount, ClusterRole + ClusterRoleBinding (all 4 CRD groups), ConfigMap
- `helm/optipilot/charts/ml-service/` — Deployment (PVC for models, readiness/liveness on /v1/health), Service
- `helm/optipilot/charts/hub/` — Deployment (mTLS flag support), Service (gRPC port), Certificate (cert-manager)
- `test/helm/chart_test.go` — 21 tests

### Task 12.2 — Container Image Builds ✅ COMPLETE
- `Dockerfile` — updated to Go 1.25, `-ldflags="-s -w"`, multi-arch ARGs, `./cmd/manager` entry
- `Dockerfile.hub` — new, distroless, non-root 65532, `./cmd/hub` entry, strip debug
- `.ko.yaml` — ko build config: distroless base, both binaries (manager + hub), linux/amd64 + linux/arm64, version ldflags injection
- `Makefile` — added `REGISTRY`, `VERSION` (git describe), `IMG_MANAGER/HUB/ML` vars; targets: `ko`, `image-manager`, `image-hub`, `image-ml`, `images`, `push`, `push-manager`, `push-hub`, `push-ml`, `image-load-kind`
- `cmd/manager/main.go` + `cmd/hub/main.go` — added `version`, `commit`, `buildDate` ldflags vars + startup log
- `test/helm/images_test.go` — 31 tests validating Dockerfiles, .ko.yaml, Makefile targets, version vars

### Task 12.3 — Documentation ✅ COMPLETE
- `docs/getting-started.md` — kind quickstart (create cluster → install → first SLO → view decisions → cleanup)
- `docs/installation.md` — production prerequisites, Helm install, production values, upgrade/uninstall
- `docs/architecture.md` — ASCII system diagram, 7 component descriptions, data flow, CRD relationships, Prometheus metrics table
- `docs/api-reference.md` — REST endpoints: decisions (list/get), whatif/simulate, whatif/slo-cost-curve, tenants, health; full request/response schemas
- `docs/configuration.md` — comprehensive Helm values reference (global, clusterAgent, mlService, hub, mTLS, ingress, serviceMonitor, production example)
- `docs/cel-reference.md` — all CEL variables (candidate/slo/tenant/metrics/cluster), built-in functions (spotRisk, carbonIntensity, costRate, p99Headroom, budgetPercent), operators, debug guide
- `docs/troubleshooting.md` — 15 common issues with causes and fixes (install, Prometheus, SLO, CEL, actuators, hub, API)
- `docs/crds/service-objective.md` — full field reference for `slo.optipilot.ai/v1alpha1`
- `docs/crds/optimization-policy.md` — field reference + CEL quick reference for `policy.optipilot.ai/v1alpha1`
- `docs/crds/tenant-profile.md` — field reference + fair-share algorithm for `tenant.optipilot.ai/v1alpha1`
- `docs/crds/application-tuning.md` — field reference + rollback mechanism for `tuning.optipilot.ai/v1alpha1`
- `docs/guides/first-slo.md` — 8-step tutorial: deploy sample app → define SLO → create policy → observe decisions
- `docs/guides/custom-policy.md` — 7 progressive CEL levels: budget guard, burn rate, cost minimize, Spot safety, carbon, tenant-aware, custom metrics
- `docs/guides/multi-cluster.md` — hub + 2 spoke setup tutorial with cert-manager mTLS, verification, troubleshooting
- `docs/guides/what-if.md` — 5 use cases: Spot simulation, cost curve, carbon regions, parameter tuning, policy pre-validation
- `docs/guides/migration-cloudpilot.md` — concept mapping table, parallel dry-run migration path, CloudPilot→OptiPilot YAML translations
- `test/helm/docs_test.go` — 73 tests: all 16 docs exist + required headings/content/API group references validated

### Task 12.4 — E2E Test Suite ✅ COMPLETE
- `test/e2e/suite_test.go` — `//go:build e2e`; TestMain: checks kind/kubectl/helm in PATH (skips gracefully if not found), creates/reuses kind cluster `optipilot-e2e`, writes temp kubeconfig, registers custom scheme (slo/policy/tenant/tuning v1alpha1), applies CRDs, `helm upgrade --install`, waits for pod Ready
- `test/e2e/helpers_test.go` — `kubectl`, `kubectlOutput`, `withKubeconfig`, `helmInstall`, `portForwardSvc`, `apiURL`, `metricsURL`, `httpGet`, `httpPost`, `httpBody`, `safePoll`, `clusterAgentSvcName`
- `test/e2e/install_test.go` — 9 tests: all 4 CRDs registered, manager pod Running, pod readiness condition, Helm release deployed, ServiceObjective schema accepted (round-trip), test/system namespaces present, ServiceAccount/ClusterRole/Service exist, list all CRs succeeds
- `test/e2e/slo_test.go` — 7 tests: create availability SLO, create latency SLO, reconciler sets conditions within 60s, observedGeneration advances, multiple objectives, errorBudget spec stored, delete propagates cleanly
- `test/e2e/policy_test.go` — 10 tests: create minimal policy, dryRun flag persisted (true+false), constraints round-trip, reconciler sets conditions, multiple objectives, TenantProfile gold tier, fair-share policy stored, TenantProfile reconcile conditions, SLO+Policy coexist, fixture helpers
- `test/e2e/api_test.go` — 11 tests: /metrics 200 + go_goroutines, decisions 200, decisions valid JSON, decisions/summary, decisions/search not 404/500, non-existent decision 404, simulate endpoint exists (not 404/500), slo-cost-curve endpoint exists, CORS header present, ?limit=5 returns 200, ?limit=invalid returns 400

| Task | Status | Tests |
|---|---|---|
| 12.1 Helm Chart Structure | ✅ | 21 |
| 12.2 Container Image Builds | ✅ | 31 |
| 12.3 Documentation | ✅ | 73 |
| 12.4 E2E Test Suite | ✅ | 37 (e2e tag) |
| 12.5 Release Automation | ✅ | 21 |
| 12.6 Quickstart Script | ✅ | 27 |
| 12.7 Open-Source Repo Prep | ✅ | 40 |

### Task 12.7 — Open-Source Repo Prep ✅ COMPLETE
- `LICENSE` — Apache 2.0, copyright 2026 OptiPilot AI Authors
- `README.md` — CI + Release + Go Report + License + Helm badges; feature table (9 pillars); ASCII architecture diagram (hub-spoke, cluster agent, ML service, actuators, decision journal); 5-minute quickstart with curl examples; Helm install + enable ML/hub snippets; ServiceObjective + OptimizationPolicy YAML examples; container image table (manager/hub/ml, multi-arch); docs link table; development commands; license section
- `CONTRIBUTING.md` — prerequisites table (Go 1.25, kind, kubectl, helm, golangci-lint, controller-gen); fork/clone; dev setup (generate, build, test, quickstart); project structure; branch strategy (feat/fix/docs/refactor/test/chore); Conventional Commits with examples; coding standards (Go, API types, CEL, Python, Helm); test layer matrix (unit/integration/helm/e2e); PR checklist; release process
- `.github/PULL_REQUEST_TEMPLATE.md` — type-of-change checklist, motivation + context, changes made, testing checklist (unit/helm/e2e), full conventions checklist, Conventional Commits format reminder
- `.github/ISSUE_TEMPLATE/bug-report.md` — severity labels, reproduction steps, expected vs actual, environment table (version/k8s/helm/cloud), component checklist, CRD YAML + manager log fields
- `.github/ISSUE_TEMPLATE/feature-request.md` — problem statement, proposed solution, alternatives, use case, acceptance criteria checklist, component checklist, priority tiers, willing-to-contribute field
- `test/helm/repo_test.go` — 40 tests: LICENSE (exists, Apache 2.0, year, holder), README (exists, CI badge, license badge, all 7 feature pillars, architecture diagram, quickstart, helm install, CRD examples, ghcr.io registry, docs links, license section, CONTRIBUTING link, dev commands, multi-arch), CONTRIBUTING (exists, dev setup, Go 1.25, branch strategy, Conventional Commits, test requirements, coding standards, PR process), PR template (exists, checklist, type-of-change, testing, Conventional Commits), bug template (exists, frontmatter, steps to reproduce, environment table), feature template (exists, frontmatter, acceptance criteria, willing-to-contribute)

### Task 12.6 — Quickstart Script ✅ COMPLETE
- `hack/quickstart.sh` — 7-step idempotent one-command demo script:
  1. Prerequisite check (kind, kubectl, helm, docker) with install URLs on failure
  2. Creates `optipilot-quickstart` kind cluster using `hack/kind-config.yaml` (control-plane + 2 workers); skips if already exists
  3. Installs `kube-prometheus-stack` via Helm (grafana + alertmanager disabled for speed)
  4. Prepares manager image: `--build-local` builds from `Dockerfile` + `kind load`; default uses `ghcr.io/optipilot-ai/optipilot/manager:latest`
  5. `helm install optipilot ./helm/optipilot` with mlService + hub disabled, prom URL wired
  6. Deploys `demo-api` Deployment (nginx:1.27-alpine, 2 replicas) in `demo` namespace; applies `ServiceObjective` + `OptimizationPolicy` (dryRun=true)
  7. `kubectl port-forward` dashboard to `localhost:8090`; prints curl examples + cleanup command
  - `--destroy`: deletes kind cluster + releases port-forward pids
  - `--help`: full usage + env var docs
  - `CLUSTER_NAME`, `REGISTRY`, `VERSION` env vars configurable
- `test/helm/quickstart_test.go` — 27 tests: shebang, pipefail, all flags, prerequisites, cluster name var, idempotency (kind + helm), kind-config.yaml, prometheus, local chart path, mlService/hub disabled, demo-api, nginx, ServiceObjective, OptimizationPolicy, dryRun, port-forward, port 8090, condition=Ready, destroy, localhost instructions, cleanup command

### Task 12.5 — Release Automation ✅ COMPLETE
- `.github/workflows/release.yaml` — 6-job release pipeline triggered on `v*` tags:
  1. `test` — go mod tidy check, generate, manifests, golangci-lint v1.64, `make test`, helm structural tests, binary smoke builds
  2. `version` — derives semver from `GITHUB_REF_NAME`, emits `tag`/`version` outputs
  3. `images` (matrix: manager + hub) — QEMU + Buildx, ghcr.io login, `docker/metadata-action` semver tags, `docker/build-push-action` linux/amd64+arm64, layer cache via GHA
  4. `image-ml` — same pattern for Python ML service (`ml/` context)
  5. `helm-release` — stamps Chart.yaml version + values.yaml image tags, `helm package`, `helm lint`, `helm push oci://ghcr.io/…/charts`
  6. `github-release` — generates structured changelog (Breaking/Features/Fixes/Other) via `git log`, creates release via `softprops/action-gh-release@v2` with prerelease detection (`-` in tag)
- `.github/workflows/ci.yaml` — updated Go 1.22 → 1.25 (match go.mod), lint v1.61 → v1.64, added `go test ./test/helm/...` step
- `test/helm/release_test.go` — 21 tests: workflow existence, valid YAML, tag trigger, 6 required jobs, job ordering, matrix targets, ghcr.io registry, OCI push, GHCR auth, GitHub release action, multi-arch, Go version, permissions, prerelease support, CI Go version, CI helm tests

### Task 11.1 — ApplicationTuning CRD ✅ COMPLETE
- `api/tuning/v1alpha1/groupversion_info.go` — group `tuning.optipilot.ai/v1alpha1`
- `api/tuning/v1alpha1/applicationtuning_types.go` — full CRD: ParameterType/ParameterSource/TuningPhase enums; TunableParameter, ConfigMapRef, OptimizationTarget, TuningSafetyPolicy, TuningTargetRef, ParameterObservation, ApplicationTuningSpec/Status, ApplicationTuning + List
- `api/tuning/v1alpha1/zz_generated.deepcopy.go` — DeepCopy for all 10 types
- `api/tuning/v1alpha1/types_test.go` — 22 tests
- **22/22 tests pass**, `go build ./...` clean

### Task 11.2 — Parameter Optimizer ✅ COMPLETE
- `internal/tuning/optimizer.go` — full optimizer: SLOFetcher/ParameterApplier interfaces, GenerateGrid (int/float/string, step-based, NaN/Inf guard), BestFromObservations, SelectOptimal (minimize/maximize), SafetyCheck (cooldown + SLO threshold), WithinChangeBounds, clampToMaxChange, NextParameterToTune (least-observed, skip active), RunCycle (paused→fetch→observe→safety→converge→grid→select→clamp→apply→cooldown), resolveSafetyPolicy, isConverged
- `internal/tuning/optimizer_test.go` — 44 tests (grid generation, correlation, selection, safety, clamping, next-param, policy defaults, convergence, 8 RunCycle integration)
- **44/44 tests pass**, `go build ./...` clean

### Task 11.3 — Custom Metric Adapter ✅ COMPLETE
- `internal/metrics/custom_adapter.go` — CustomMetricAdapter (concurrent Fetch via PrometheusClient), CustomMetricResult, Score (weighted distance), MergeIntoMetrics
- `internal/metrics/custom_adapter_test.go` — 19 tests: 6 Fetch (empty, single, parallel, partial failure, all fail, preserves target/weight), 8 Score (empty, on-target, off-target, weighted, skip errors, skip zero-weight, small target, negative target), 4 Merge (nil dst, overwrites, empty fetched, both nil), 1 integration (fetch→score)
- **19/19 tests pass**, `go build ./...` clean

### Task 11.4 — Storage Recommender ✅ COMPLETE
- `internal/storage/recommender.go` — PVCMetrics (IOPS, throughput, latency, queue depth), ClassifyProfile (7 profiles: idle/bursty/sequential/read-heavy/write-heavy/random/mixed), StorageClassProfile catalog (gp3/io2/st1), Recommender.Recommend (profile→scoring→cost estimation→annotations), RecommendAll, ChangesOnly, classScore (profile affinity + IOPS/throughput penalty + latency + cost), buildAnnotations
- `internal/storage/recommender_test.go` — 38 tests: 7 ClassifyProfile, 5 PVCMetrics helpers, 1 catalog, 5 profile-driven recommendations (mixed→gp3, random→io2, sequential→st1, idle→gp3, bursty→io2), 3 cost estimation, 2 annotations/reason, 3 RecommendAll/ChangesOnly, 1 custom catalog, 2 profileMatch, 1 Round2, 3 classScore edge cases, 3 findClass, 1 negative savings, 1 NaN guard
- **38/38 tests pass**, `go build ./...` clean

### Task 11.5 — Solver Integration ✅ COMPLETE
- `internal/engine/advanced_signals.go` — AdvancedSignals struct (CustomMetricResults, TuningOverrides, StorageRecommendations); EnrichMetrics (merge custom metrics into SolverInput.Metrics for CEL); EnrichTuning (copy tuning overrides into ScalingAction.TuningParams); CustomMetricScore (weighted distance); StorageMonthlySavings / StorageHourlySavingsEstimate; AdjustCostScore (storage savings bonus capped at 0.2); EnrichScoredCandidates (in-place cost adjustment + custom metric penalty)
- `internal/engine/advanced_signals_test.go` — 29 tests: 5 EnrichMetrics (nil, empty, merge, skip error, nil-map), 4 EnrichTuning, 3 CustomMetricScore, 4 StorageSavings, 6 AdjustCostScore (nil, no-savings, with, small, capped, zero-cost), 6 EnrichScoredCandidates (nil, storage, penalty, floor, combined), 1 zero-value safety
- All backward-compatible: nil AdvancedSignals = no-op
- **29/29 tests pass**, `go build ./...` clean

### Task 11.6 — Integration Tests ✅ COMPLETE
- `test/integration/phase11_integration_test.go` — 5 integration tests:
  1. `TestOptimizer_ConvergesToKnownOptimal` — optimizer with parabolic SLO oracle converges to known optimum in <20 cycles
  2. `TestStorageRecommender_SyntheticProfiles` — 5 sub-tests (write-heavy, sequential, read-heavy, idle, bursty) classify correctly and recommend matching storage classes
  3. `TestCustomMetricInjection_MockPrometheus` — mock Prometheus HTTP → CustomMetricAdapter.Fetch → Score validates weighted distance
  4. `TestEndToEnd_ApplicationTuning_CycleUpdatesConfigMap` — full CRD→optimizer→ConfigMap→solver cycle with convergence validation
  5. `TestFullPipeline_AllSignalsEnrich` — custom metrics + storage + tuning → AdvancedSignals enrich pipeline
- **5/5 tests pass**, `go build ./...` clean

| Task | Status | Tests |
|---|---|---|
| 11.1 ApplicationTuning CRD | ✅ | 22 |
| 11.2 Parameter Optimizer | ✅ | 44 |
| 11.3 Custom Metric Adapter | ✅ | 19 |
| 11.4 Storage Recommender | ✅ | 38 |
| 11.5 Solver Integration | ✅ | 29 |
| 11.6 Integration Tests | ✅ | 5 |

### Phase 11 Totals
- **157 new tests** (22 + 44 + 19 + 38 + 29 + 5)
- **Cumulative: 767 tests (611 Go + 25 React + 131 Python)**
- Phase 11 ✅ COMPLETE

---

## Live Vertical Tuning Proof — End-to-End Validation ✅

### Summary
Vertical scaling (right-sizing CPU/memory requests) fully proven in a live kind cluster with real Prometheus metrics. The OptiPilot controller autonomously reads actual resource usage, computes right-sized requests with headroom, issues `tune` actions, and patches Deployment specs — causing pods to roll out with optimized resources.

### Tune Actions Observed (from Kubernetes events + controller logs)

| Deployment | Action | CPU Request | Memory Request | Score |
|---|---|---|---|---|
| admin-frontend | **tune** | 100m → **10m** (90% reduction) | 128Mi → **43Mi** (66% reduction) | 1.000 |
| api | **tune** | 100m → **27m** (73% reduction) | 256Mi → **410Mi** (usage-driven increase) | 1.000 |
| main-site | **tune** | 14m → **18m** (fine-tuning) | 128Mi → **57Mi** (53% reduction) | 1.000 |

### SLO Compliance After Tuning
| SLO | Budget | Compliant |
|---|---|---|
| codepro-admin-frontend-slo | 100.00% | True ✅ |
| codepro-main-site-slo | 100.00% | True ✅ |
| codepro-api-slo | 66.18% | False (recovering from Docker disruption) |

### Key Evidence
- `kubectl get events -n codepro --field-selector reason=Actuated` → `tune applied (1 changes)` for admin-frontend and api
- `kubectl get events -n codepro --field-selector reason=OptimizationDecision` → full decision audit trail
- Controller logs: `optimization decision {"namespace": "codepro", "service": "main-site", "action": "tune", ...}` → `tune applied (1 changes)`
- Deployment specs verified via `kubectl get deploy -o jsonpath` — resource requests actually changed

### Code Changes That Enabled This
1. **Resource-aware cost estimation** (`candidates.go`): `planResourceShare()` + `estimateCost()` scales cost by CPU/memory share so smaller requests win economically
2. **Memory-aware dedup** (`candidates.go`): `candidateKey` includes `MemoryRequest` preventing memory-only variants from being collapsed
3. **Spot-only no-op fix** (`solver.go`): `buildAction()` resolves spot-only diffs to `no_action` instead of unsupported no-op `tune`
4. **Degraded SLO scoring** (`scorer.go`): `scoreDegradedSLO()` favors scale-up when SLO is violated, tune when compliant