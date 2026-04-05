# OptiPilot AI â€” CONTEXT.md

## Project
SLO-native Kubernetes optimization platform (12-phase build).

## Module
`github.com/optipilot-ai/optipilot` Â· Go 1.25 Â· controller-runtime v0.19.3

## Repository Layout
```
api/
  slo/v1alpha1/         ServiceObjective CRD types + deepcopy
  policy/v1alpha1/      OptimizationPolicy CRD types + deepcopy
  tenant/v1alpha1/      TenantProfile CRD types + deepcopy
  global/v1alpha1/      ClusterProfile + GlobalPolicy CRD types + deepcopy (Phase 10)
cmd/manager/main.go     Manager entrypoint (all 3 controllers, EventRecorder, SLO Evaluator)
cmd/hub/main.go         Hub controller binary (management cluster, watches ClusterProfile + GlobalPolicy)
internal/
  global/grpc/          gRPC hub-spoke transport (JSON codec, mTLS, HubServer, SpokeClient, MemoryHubService)
  global/spoke/         Spoke agent (registration, heartbeats, directive polling)
  global/               Global solver (cross-cluster traffic weights, hibernation/wake-up)
config/crd/bases/       3 generated CRD YAMLs
config/examples/        Sample CR YAMLs (phase 1)
hack/e2e-setup.sh       Kind + kube-prometheus-stack + sample app e2e bootstrap
internal/
  controller/           Reconcilers (SO full, OpPolicy full, TenantProfile stub, OptimizerController periodic loop)
  cel/                  CEL environment, PolicyEngine, custom functions
  engine/               Candidate generator, scorer, Pareto selector, solver
  explainability/       SQLite decision journal + REST API handler
  metrics/              Prometheus HTTP client + controller-runtime self-observability metrics
  slo/                  PromQL builder + burn-rate evaluator
  webhook/              Validating webhook for OptimizationPolicy
```

## Key Conventions
- `controller-gen crd:allowDangerousTypes=true` required (float64 fields)
- Burn rate model: Google SRE multi-window (burn rate â‰¥ 1.0 = budget consumed at sustainable rate)
- `ParseTarget`: `200ms`â†’0.200, `0.1%`â†’0.001, `99.95%`â†’0.9995, `1000rps`â†’1000.0
- `ErrNoData` / `ErrMultipleResults` sentinel errors from `internal/metrics`
- Controller conditions: `SLOCompliant`, `BudgetExhausted`, `TargetFound`
- Prometheus HTTP client: 5 s TTL in-memory cache, `Query` + `QueryRange` + `Healthy`
- Self-observability metrics: `optipilot_slo_burn_rate`, `optipilot_slo_budget_remaining_ratio`, `optipilot_slo_evaluation_duration_seconds`, `optipilot_slo_evaluation_errors_total`, `optipilot_slo_compliant`

## Phase 3 Additions
- `internal/cel/types.go` â€” context structs (`CandidatePlan`, `CurrentState`, `SLOStatus`, `TenantStatus`, `ForecastResult`, `ClusterState`) with `cel:"..."` tags
- `internal/cel/functions.go` â€” `SpotRiskFunc`, `CarbonIntensityFunc`, `CostRateFunc` heuristic implementations
- `internal/cel/environment.go` â€” `NewOptiPilotEnv()` using `ext.NativeTypes` + `ext.ParseStructTags(true)` + custom function bindings
- `internal/cel/engine.go` â€” `PolicyEngine` with `Compile` / `Evaluate` / `GetCompiled` / `PolicyKey`
- `internal/webhook/optimizationpolicy_webhook.go` â€” validating webhook (`admission.Handler`)
- `internal/controller/optimizationpolicy_controller.go` â€” full reconciler with CEL compile, condition tracking, `FindPoliciesForService`, `countMatchedServices`
- `cmd/manager/main.go` â€” `PolicyEngine` and `Recorder` wired into `OptimizationPolicyReconciler`

## Key API Contracts (Phase 3)
- `cel.PolicyEngine.Compile(*OptimizationPolicy) error`
- `cel.PolicyEngine.Evaluate(policyKey string, ctx EvalContext) (EvalResult, error)`
- `cel.PolicyKey(*OptimizationPolicy) string`
- `EvalContext{Candidate, Current, SLO, Tenant, Forecast, Metrics, Cluster}`
- `CandidatePlan` struct â€” used by Phase 4 solver
- `ext.NativeTypes` + `ext.ParseStructTags(true)` required for `cel:""` tag resolution

## Phase 4 Additions
- `internal/engine/types.go` â€” `SolverInput`, `ScalingAction`, `CandidateScore`, `ScoredCandidate`, `DecisionRecord`, `MatchedPolicy`, `ActionType`
- `internal/engine/candidates.go` â€” `GenerateCandidates()` cartesian product + dedup + pruning + sampling
- `internal/engine/scorer.go` â€” 4-dim scorer (SLO, Cost, Carbon, Fairness) with batch normalization + policy weight aggregation
- `internal/engine/pareto.go` â€” `FindParetoFront()` (4-dim dominance), `SelectBest()` (weighted + disruption tie-break)
- `internal/engine/solver.go` â€” `Solver.Solve()` orchestrates candidates â†’ scoring â†’ CEL filtering â†’ Pareto â†’ action
- `internal/explainability/journal.go` â€” SQLite journal (WAL mode) with Write/Query/GetByID
- `internal/explainability/api_handler.go` â€” REST endpoints `GET /api/v1/decisions` + `GET /api/v1/decisions/{id}`
- `internal/controller/optimizer_controller.go` â€” periodic `manager.Runnable` loop, iterates ServiceObjectives
- `cmd/manager/main.go` â€” `--optimizer-interval`, `--journal-path` flags, journal + optimizer wiring
- Dependencies added: `modernc.org/sqlite v1.48.1` (pure Go), `google/uuid`

## Key API Contracts (Phase 4)
- `engine.Solver.Solve(*SolverInput) (ScalingAction, DecisionRecord, error)`
- `engine.GenerateCandidates(input, maxCandidates) []CandidatePlan`
- `engine.NewScorer(input, objectives).ScoreAll(candidates) []ScoredCandidate`
- `engine.FindParetoFront(scored) []ScoredCandidate`
- `engine.SelectBest(front, currentState) ScoredCandidate`
- `explainability.Journal.Write(DecisionRecord) error`
- `explainability.Journal.Query(QueryFilter) ([]DecisionRecord, error)`
- `ScalingAction` struct â€” consumed by Phase 5 actuators

## Phase 5 Additions (in progress)
- `internal/actuator/interface.go` â€” `Actuator` interface, `Registry` (dispatch + rollback), `ServiceRef`, `ActuationOptions`, `ActuationResult`, `ChangeRecord`; annotation constants `AnnotationPreviousState`, `AnnotationPause`, `AnnotationRestartOnTuning`, `AnnotationVPARecommendation`
- `internal/actuator/pod_actuator.go` â€” `PodActuator`; HPA patch, direct Deployment scale, VPA annotation mode; `MinReplicas` floor, `applyMaxChange()` fractional clamp, dry-run, previous-state annotation rollback
- `internal/actuator/node_actuator.go` â€” `NodeActuator`; Karpenter NodePool via `unstructured.Unstructured` GVK `karpenter.sh/v1 NodePool`; `buildRequirements()` preserves non-capacity reqs, enforces on-demand fallback (even at 100% spot); `applyHint()` fallback stores `AnnotationNodeHint` on Namespace

- `internal/actuator/app_tuner.go` â€” `AppTuner{Client, ConfigMapName}`; reads `{name}-config` ConfigMap (or explicit name); clamps numeric params via `optipilot.ai/min/{key}` / `optipilot.ai/max/{key}` annotations; triggers rolling restart if `AnnotationRestartOnTuning=true` on Deployment; `AnnotationPreviousState` stored on ConfigMap for rollback
- `engine.ScalingAction.TuningParams map[string]string` â€” added for app-level key=value tuning overrides
- `internal/actuator/safety.go` â€” `SafetyGuard`; emergency stop (namespace annotation + global ConfigMap `optipilot-system/optipilot-pause`), cooldown (in-memory map), circuit breaker (3-strike, 15 min pause); `SetClock(fn)` for test injection; `Allow()` composite gate
- `internal/actuator/canary.go` â€” `CanaryController`; two-step canary for >50% changes; `SLOChecker` interface; `SetSleepFn()` + `SetStepDelay()` for test injection; `WatcherActive()` for rollback goroutine probe
- `internal/actuator/integration_test.go` â€” 9 end-to-end integration tests wiring Registry + PodActuator + AppTuner + SafetyGuard + CanaryController
- `internal/controller/optimizer_controller.go` â€” added `ActuatorReg`, `SafetyGuard`, `CanaryCtrl` fields; `actuate()` method wires safety gate â†’ canary/registry dispatch â†’ outcome recording + k8s events

## Key API Contracts (Phase 5 â€” COMPLETE)
- `actuator.Actuator` interface: `Apply(ctx, ServiceRef, ScalingAction, ActuationOptions) (ActuationResult, error)`, `Rollback(ctx, ServiceRef) error`, `CanApply(ScalingAction) bool`
- `actuator.Registry.Apply` â€” dispatches to first `CanApply` actuator; `Registry.Rollback` â€” calls ALL actuators
- `actuator.PodActuator{Client, MinReplicas}` â€” HPA or Deployment mode auto-selected
- `actuator.NodeActuator{Client, NodePoolName}` â€” Karpenter NodePool or namespace hint fallback
- `actuator.AnnotationNodeHint = "optipilot.ai/node-hint"`
- `actuator.SafetyGuard.Allow(ctx, ref, opts) error` â€” composite gate (emergency + cooldown + circuit)
- `actuator.CanaryController.Apply(ctx, ref, action, opts, currentReplicas) (ActuationResult, error)` â€” two-step canary with SLO check
- `actuator.OutcomeImproved / OutcomeDegraded` â€” circuit breaker inputs via `RecordOutcome()`

## Phase 6 Additions (in progress)
- `ml/pyproject.toml` + `ml/requirements.txt` â€” Python project config (FastAPI, statsforecast, XGBoost, Pydantic v2, Prometheus-client)
- `ml/app/schemas.py` â€” Pydantic v2 request/response models: `MetricPoint`, `DemandForecastRequest/Response`, `ForecastPoint`, `PredictionInterval`, `SpotRiskRequest/Response`, `HealthResponse`
- `ml/tests/test_schemas.py` â€” 24 schema validation tests (all pass)

## Key API Contracts (Phase 6 so far)
- `DemandForecastRequest.history: list[MetricPoint]` â€” min 1 point; `horizons_minutes` defaults `[15, 60, 360]`
- `DemandForecastResponse.change_percent` â€” % delta vs tail of history at 15-min horizon
- `SpotRiskRequest` â€” `instance_type`, `az`, `hour_of_day`, `day_of_week`, `recent_interruption_count_7d`, `spot_price_ratio` (>0)
- `SpotRiskResponse.recommended_action: Literal['keep','migrate','switch_to_od']`
- `HealthResponse.status: Literal['ok','degraded']`

- `ml/app/forecaster.py` â€” `DemandForecaster` class: AutoARIMA + AutoETS ensemble (statsforecast v2); SeasonalNaive fallback for <24 points; forward-fill gap handling; `compute_change_percent()`, `compute_confidence()` helpers
- `ml/app/spot_predictor.py` â€” `SpotPredictor` class: XGBoost classifier with synthetic training data, one-hot encoding, save/load JSON, action thresholds (keep <0.3, migrate 0.3â€“0.6, switch_to_od >0.6)
- `ml/tests/test_spot_predictor.py` â€” 23 tests (training, prediction, thresholds, persistence, confidence)

## Key API Contracts (Phase 6 â€” Spot Predictor)
- `SpotPredictor.train(data=None, n_synthetic=5000)` â€” trains XGBClassifier
- `SpotPredictor.predict(instance_type, az, hour, dow, ...) â†’ {interruption_probability, recommended_action, confidence}`
- `SpotPredictor.save(path)` / `.load(path)` â€” JSON + meta.json roundtrip

## Key API Contracts (Phase 6 â€” Forecaster)
- `DemandForecaster.forecast(history_df, horizons_minutes) â†’ (result_df, model_used)`
- `result_df` columns: `ds`, `yhat`, `lo_80`, `hi_80`, `lo_95`, `hi_95`
- `model_used`: `"ensemble"` | `"SeasonalNaive"`
- statsforecast v2 API: `sf.forecast(df=df, h=steps, level=[80,95])`
- `SEASON_LENGTH=12` (1h at 5-min intervals), `MIN_POINTS_FOR_ENSEMBLE=24`

- `ml/app/main.py` â€” FastAPI app: `POST /v1/forecast`, `POST /v1/spot-risk`, `GET /v1/health`, `GET /metrics`; `create_app(skip_lifespan=True)` for tests; `set_forecaster()/set_spot_predictor()` state injectors; NaN sanitiser for JSON responses; 10s timeout per forecast
- `ml/tests/test_api.py` â€” 28 tests (forecast success, validation, 503, spot-risk, health, metrics)

## Key API Contracts (Phase 6 â€” FastAPI)
- `create_app(skip_lifespan=True)` â€” factory used in tests (bypasses model training lifespan)
- `set_forecaster(f)` / `set_spot_predictor(sp)` â€” test injection into module-level `_state`
- `_safe(v)` helper sanitises NaN/Inf floats before JSON serialisation
- `unloaded_client` is **function-scoped** with save/restore to avoid state pollution

- `internal/forecaster/client.go` â€” `Client` struct; `ForecastDemand(ctx, service, metric, history) â†’ *cel.ForecastResult`; `PredictSpotRisk(ctx, instanceType, az) â†’ *SpotRiskResult`; circuit breaker (5 errors/1 min â†’ 30s pause); 1 retry with 200ms backoff on 5xx; `SetNowFn()` + `SetSleepFn()` for tests
- `internal/forecaster/client_test.go` â€” 11 tests (success, empty forecasts, circuit breaker, retry, context cancel, 422 non-retriable, error window eviction)

## Phase 6 Additions â€” Solver Integration
- `internal/engine/candidates.go` â€” Added `PreWarmingCandidates()` (Ã—1.3, Ã—1.5 replicas), `SpotReductionCandidates()` (ratios 0.0, 0.3), constants `PreWarmingChangeThreshold=20.0`, `SpotRiskThreshold=0.6`
- `internal/engine/solver.go` â€” `Solve()` checks `input.Forecast`; if `ChangePercent>20` â†’ injects pre-warming candidates; if `SpotRiskScore>0.6` â†’ injects spot-reduction candidates; nil forecast â†’ reactive-only (no injection)
- `internal/engine/candidates_test.go` â€” 7 new tests for PreWarmingCandidates + SpotReductionCandidates
- `internal/engine/solver_test.go` â€” 6 new tests for forecast integration (pre-warming, nil fallback, low change, spot risk, both triggers, below threshold)

## Key API Contracts (Phase 6 â€” Solver Integration)
- `PreWarmingCandidates(input) []CandidatePlan` â€” generates Ã—1.3 and Ã—1.5 replica candidates after pruning
- `SpotReductionCandidates(input) []CandidatePlan` â€” generates candidates with reduced spot ratios
- `PreWarmingChangeThreshold = 20.0` â€” forecast ChangePercent above this triggers pre-warming
- `SpotRiskThreshold = 0.6` â€” forecast SpotRiskScore above this triggers spot reduction
- Nil forecast â†’ solver proceeds with reactive-only (no injected candidates)

## Key API Contracts (Phase 6 â€” Go Client)
- `forecaster.NewClient(baseURL, ...opts) *Client`
- `Client.ForecastDemand(ctx, service, metric, []MetricPoint) (*cel.ForecastResult, error)` â€” nil, nil when circuit open
- `Client.PredictSpotRisk(ctx, instanceType, az) (*SpotRiskResult, error)` â€” nil, nil when circuit open
- `Client.CircuitOpen() bool`, `Client.SetNowFn(fn)`, `Client.SetSleepFn(fn)`
- `SpotRiskResult.InterruptionProbability float64`, `.RecommendedAction string`, `.Confidence float64`
- 5xx responses are retriable (1 retry); 4xx are not

## Phase 6 Additions â€” Accuracy Tracking
- `ml/app/accuracy.py` â€” `AccuracyTracker` class: per-service sliding window (200 records), MAE/MAPE computation, fallback activation when MAPE >30% for 1 hour sustained, auto-recovery when MAPE drops; injectable clock for tests
- `ml/tests/test_accuracy.py` â€” 21 tests (basic metrics, fallback logic, multi-service isolation, window cap, reset, edge cases)
- `internal/forecaster/accuracy_tracker.go` â€” `AccuracyTracker` struct: thread-safe (mutex), per-service sliding window, MAE/MAPE, `IsFallbackActive()`, `Record/MAE/MAPE/Reset/Services`; options: `WithMAPEThreshold`, `WithSustainDuration`, `WithWindowMax`; `SetNowFn` for tests
- `internal/forecaster/accuracy_tracker_test.go` â€” 19 tests (basic metrics, fallback logic, multi-service, window cap, reset, edge cases)

## Key API Contracts (Phase 6 â€” Accuracy Tracking)
- Python: `AccuracyTracker(mape_threshold=30.0, sustain_seconds=3600.0, window_max=200, clock=None)`
- Python: `.record(service, predicted, actual)`, `.mae(service)`, `.mape(service)`, `.is_fallback_active(service)`, `.reset(service)`, `.services()`
- Go: `NewAccuracyTracker(...AccuracyOption) *AccuracyTracker`
- Go: `.Record(service, predicted, actual)`, `.MAE(service)`, `.MAPE(service)`, `.IsFallbackActive(service)`, `.Reset(service)`, `.Services()`
- Fallback: MAPE >30% sustained for 1h â†’ `IsFallbackActive()` returns true â†’ solver skips pre-warming
- Prometheus metrics (already declared in main.py): `optipilot_forecast_mae`, `optipilot_forecast_mape`, `optipilot_forecast_fallback_active`

## Phase 6 Additions â€” Containerisation
- `ml/Dockerfile` â€” Multi-stage build: `python:3.11-slim` builder with gcc/g++ for native extensions; runtime stage copies `/install` from builder; non-root user (uid 1001); exposes 8080; `uvicorn --factory app.main:create_app`
- `config/ml/deployment.yaml` â€” Deployment (1 replica, `500m`/`1Gi` requests, `2`/`2Gi` limits, readiness + liveness probes on `/v1/health`, Prometheus scrape annotations, ServiceAccount, emptyDir volume for `/models`)
- `config/ml/service.yaml` â€” ClusterIP Service on port 8080
- `ml/tests/test_sinusoidal_forecast.py` â€” 14 tests: `TestSeasonalNaiveAccuracy` (peak/trough within 10%, intervals ordered) + `TestEnsembleContracts` (API shape, finite values, variation)

### Bug fix
- `ml/app/forecaster.py` `_format_output`: changed `reset_index()` to `reset_index(drop=True)` â€” prevents statsforecast v2's RangeIndex becoming an `index` column that was being averaged into yhat, corrupting all forecast values

## Key API Contracts (Phase 6 â€” Deployment)
- `ml/Dockerfile`: `FROM python:3.11-slim AS builder` / `FROM python:3.11-slim AS runtime`; `CMD ["python", "-m", "uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8080"]`
- `config/ml/deployment.yaml`: namespace `optipilot-system`, image `optipilot-ml:latest`, port 8080, `requests.cpu=500m`, `requests.memory=1Gi`
- `config/ml/service.yaml`: type `ClusterIP`, port 8080

## Current Phase
**Phase 7 â€” Tenant-Aware Fairness & Quota System** (Task 7.1 COMPLETE)

## Phase 7 Additions
- `internal/tenant/manager.go` â€” `Manager` struct: `sync.RWMutex`-protected `map[string]*TenantState`; injectable `metrics.PrometheusClient` + `Clock`; `UpdateProfiles()` syncs from TenantProfile CRs; `Refresh(ctx)` queries Prometheus per-tenant (CPU, memory, cost via namespace regex); `Start(ctx)` implements `manager.Runnable` with configurable interval (default 30s); `GetState(name)` / `GetAllStates()` return deep copies
- `internal/tenant/manager_test.go` â€” 20 tests (defaults, options, add/remove/update profiles, full spec, nil budgets, unknown state, copy safety, multi-tenant refresh, partial errors, all errors, timestamp tracking, concurrent access, context cancellation)

## Key API Contracts (Phase 7 â€” Manager)
- `tenant.NewManager(prom, ...ManagerOption) *Manager`
- `Manager.UpdateProfiles([]TenantProfile)` â€” syncs tenant set, parses budgets + fair-share policy
- `Manager.Refresh(ctx) error` â€” queries Prometheus, partial errors tolerated (zeroed values)
- `Manager.GetState(name) *TenantState` â€” returns deep copy or nil
- `Manager.GetAllStates() map[string]*TenantState` â€” snapshot
- `Manager.Start(ctx) error` â€” blocking loop (initial refresh + ticker)
- `TenantState` fields: Name, Tier, Weight, Namespaces, CurrentCores, CurrentMemoryGiB, CurrentCostUSD, MaxCores, MaxMemoryGiB, MaxMonthlyCostUSD, GuaranteedCoresPercent, Burstable, MaxBurstPercent, FairnessScore, AllocationStatus, LastRefreshed
- Prometheus queries: `sum(namespace_cpu_usage_seconds_total{namespace=~"..."})`, `sum(namespace_memory_working_set_bytes{namespace=~"..."})`, `sum(namespace_cost_per_hour{namespace=~"..."})`

## Phase 7 Additions â€” Fair-Share Algorithm
- `internal/tenant/fairshare.go` â€” `ComputeFairShares(clusterCores, []FairShareInput) []ResourceShare`; 3-phase algorithm: guarantee (% of cluster) â†’ burst (weight-proportional) â†’ cap (maxBurstPercent + reclaim/redistribute loop); `AllocationStatusFor(currentCores, share) string` â†’ guaranteed|bursting|throttled|under_allocated
- `internal/tenant/fairshare_test.go` â€” 23 tests (3-tenant spec scenario, cap enforcement, reclamation, edge cases: empty, zero cluster, single tenant, non-burstable, oversubscribed, zero weight/guarantee, allocation status)

## Key API Contracts (Phase 7 â€” Fair-Share)
- `ComputeFairShares(clusterCores float64, inputs []FairShareInput) []ResourceShare`
- `FairShareInput{Name, Weight, GuaranteedCoresPercent, Burstable, MaxBurstPercent, CurrentCores}`
- `ResourceShare{Name, GuaranteedCores, BurstCores, MaxCores, TotalCores}`
- `AllocationStatusFor(currentCores float64, share ResourceShare) string`
- Cap phase iterates up to 10 rounds for convergence; reclaimed excess redistributed by weight

## Phase 7 Additions â€” Quota Enforcement
- `internal/tenant/quota.go` â€” `CheckQuota(state *TenantState, delta ResourceDelta) QuotaResult`; checks cores/memory/cost against hard limits; limit of 0 = unlimited; returns `{Allowed bool, Reason string}`
- `internal/tenant/quota_test.go` â€” 19 tests (per-dimension pass/fail, exact limit, zero limit = unlimited, multi-dimension priority, nil state, zero/negative delta, reason format)

## Key API Contracts (Phase 7 â€” Quota)
- `ResourceDelta{AdditionalCores, AdditionalMemoryGiB, AdditionalCostUSD float64}`
- `QuotaResult{Allowed bool, Reason string}`
- `CheckQuota(state, delta)` â€” stateless, checks in order: cores â†’ memory â†’ cost; first breach returns denial
- Limits of 0 are treated as unlimited (no cap)

## Phase 7 Additions â€” Jain's Fairness Index
- `internal/tenant/fairness.go` â€” `ComputeFairness([]FairnessInput) *FairnessResult`; J(x) = (Î£xi)Â²/(nÃ—Î£xiÂ²) where xi=actual/guaranteed; tenants with 0 guaranteed excluded; Prometheus gauges `optipilot_fairness_index` (global) + `optipilot_tenant_fairness_score` (per-tenant); `RecordFairnessMetrics()` updates gauges
- `internal/tenant/fairness_test.go` â€” 14 tests (perfect equality, worst case 2/3 tenants, proportional, intermediate, bursting, single tenant, empty, no guaranteed, mixed, zero usage, per-tenant scores, nil-safe metrics)

## Key API Contracts (Phase 7 â€” Fairness)
- `FairnessInput{Name, CurrentCores, GuaranteedCores}`
- `FairnessResult{GlobalIndex float64, PerTenant map[string]float64}`
- `ComputeFairness(inputs) *FairnessResult` â€” nil if no tenants have guaranteed>0
- `RecordFairnessMetrics(result)` â€” sets `optipilot_fairness_index` gauge + per-tenant `optipilot_tenant_fairness_score`
- All-zero usage treated as vacuously fair (J=1.0)

## Phase 7 Additions â€” Noisy Neighbor Detection
- `internal/tenant/noisy_neighbor.go` â€” `NoisyNeighborDetector`: tracks per-tenant CPU usage history, `RecordUsage()` on each refresh; `Detect(guaranteed, current)` finds aggressors (>50% growth in 5min) with victims (below guaranteed); `IsNoisy(name)`/`IsVictim(name)` solver signals (expire after 2Ã— window); `RecentAlerts()` ring buffer; `FormatAlert()` human-readable
- `internal/tenant/noisy_neighbor_test.go` â€” 18 tests (growth trigger, below threshold, no victim, self-exclusion, multiple aggressors/victims, solver signals, signal expiry, no history, single snapshot, zero baseline, decreasing usage, old snapshots, alert accumulation, format)

## Key API Contracts (Phase 7 â€” Noisy Neighbor)
- `NoisyNeighborDetector` with injectable `Clock`, `threshold`, `window`
- `RecordUsage(name, cores)` â€” call per refresh cycle per tenant
- `Detect(guaranteedCores, currentCores map[string]float64) []NoisyNeighborAlert`
- `NoisyNeighborAlert{Aggressor, AggressorGrowth, Victims []string, Timestamp}`
- `IsNoisy(name) bool` / `IsVictim(name) bool` â€” solver signals, expire after 2Ã—window
- Aggressor: usage grew >50% in 5min window; Victim: current cores < guaranteed cores
- Zero baseline skipped; aggressor excluded from own victim list

## Phase 7 Additions â€” Tenant REST API
- `internal/api/tenant_api.go` â€” `TenantAPIHandler` with `TenantStateReader` interface; 5 endpoints via `http.ServeMux` Go 1.22+ routing
- `internal/api/tenant_api_test.go` â€” 20 tests (list/get/usage/fairness/impact, 404s, 400s, JSON fields, noisy signal, Prometheus time-series)

## Key API Contracts (Phase 7 â€” REST API)
- `NewTenantAPIHandler(manager TenantStateReader, prom PrometheusClient, detector *NoisyNeighborDetector) *TenantAPIHandler`
- `TenantAPIHandler.RegisterRoutes(mux *http.ServeMux)` â€” 5 routes
- `GET /api/v1/tenants` â†’ `[]tenantSummaryResponse` (all states + noisy/victim signals)
- `GET /api/v1/tenants/{name}` â†’ `tenantDetailResponse` or 404
- `GET /api/v1/tenants/{name}/usage` â†’ `usageResponse` (Prometheus QueryRange 1h; falls back to current snapshot)
- `GET /api/v1/fairness` â†’ `fairnessResponse{timestamp, global_index, per_tenant}`
- `GET /api/v1/fairness/impact/{service}?tenant=X&delta_cores=N` â†’ `impactResponse` with current/projected indices
- `TenantStateReader` interface: `GetState(name) *TenantState`, `GetAllStates() map[string]*TenantState`

## Phase 7 Status
**COMPLETE âœ…** â€” All 7 tasks done. 127 tests across `internal/tenant` (107) and `internal/api` (20).

## Phase 8: Explainability Engine & What-If Simulator

### Task 8.1 â€” Enhanced Decision Journal (COMPLETE âœ…)
- **Modified**: `internal/explainability/journal.go`
- `QueryFilter` gained `Trigger string` field
- `Search(text string, limit int) ([]DecisionRecord, error)` â€” FTS5 (with LIKE fallback)
- `AggregateStats(window time.Duration) (*JournalStats, error)` â€” total, per-hour, avg confidence, top triggers, top services
- `Purge(olderThan time.Time) (int64, error)` â€” hard delete + FTS sync
- New types: `JournalStats`, `TriggerCount`, `ServiceCount`
- Schema: added `reason TEXT` column, `decisions_fts` FTS5 virtual table, trigger index
- `hasFTS bool` on Journal struct â€” graceful degradation if FTS5 unavailable
- 13 new tests (25 total in package)

### Task 8.2 â€” Decision Narrator (COMPLETE âœ…)
- **Created**: `internal/explainability/narrator.go` + `narrator_test.go`
- `NarrateDecision(record DecisionRecord) string` â€” template-based natural language generator
- 6 narrative handlers: `scale_up`, `scale_down`, `no_action`, `tune`, `dry_run`, `rollback`
- Rollback detected by `trigger` containing "rollback"
- Includes: timestamp, reason, SLO burn rate, forecast direction, candidate counts, selected plan (replicas, cost, spot/on-demand mix), trade-off scores, confidence
- 10 tests covering all action types + edge cases (no candidates, SLO at risk, forecast decrease)

### Task 8.3 â€” What-If Simulator (COMPLETE âœ…)
- **Created**: `internal/simulator/simulator.go` + `simulator_test.go`
- `Simulator` struct with injectable `HistoryProvider`, `DecisionProvider`, `SolverFunc`
- `Run(ctx, SimulationRequest) (*SimulationResult, error)` â€” replays historical metrics with alternative solver
- Types: `SimulationRequest{ID, Services, Start, End, Step, Description}`, `SimulationResult{Timeline, OriginalCost, SimulatedCost, CostDeltaPercent, SLOBreaches}`, `SimulatedStep{Snapshot, Original, Simulated}`, `SimulationSnapshot`, `SimulatedAction`, `CostSummary`, `HistoricalDecision`, `DataPoint`
- Fetches 4 metric types per service: CPU, latency p99, error rate, request rate
- Matches simulated steps to actual decisions by (service, truncated timestamp)
- Aggregates: total/avg/peak hourly cost for both original and simulated; cost delta %; SLO breach counts
- 16 tests (basic run, scale-up/down detection, decision matching, cost comparison, SLO breach counting, multi-service, error cases, custom solver, peak cost, etc.)

### Task 8.4 â€” SLO-Cost Curve Generator (COMPLETE âœ…)
- **Created**: `internal/simulator/slo_cost_curve.go` + `slo_cost_curve_test.go`
- `SLOCurveGenerator` with injectable `HistoryProvider`, `DecisionProvider`, `SLOCurveSolverFactory`
- `Generate(ctx, SLOCurveRequest) ([]CurvePoint, error)` â€” sweeps SLO target from min to max in N steps
- Types: `SLOCurveRequest{Service, Start, End, Step, SLOMetric, MinTarget, MaxTarget, Steps}`, `CurvePoint{SLOTarget, ProjectedMonthlyCost, ProjectedCompliancePct, AvgReplicas, SLOBreaches, TotalSteps}`
- `SLOCurveSolverFactory func(sloMetric string, sloTarget float64) SolverFunc` â€” creates parameterized solvers per sweep point
- Monthly cost projection: hourly cost Ã— (730 / windowHours)
- Validation: service required, end > start, min < max (or min == max with Steps==1)
- Defaults: 10 steps, 5-minute step interval
- 12 tests (basic sweep, tighterâ†’more cost, tighterâ†’more breaches, compliance range, avg replicas positive, single step, ascending targets, 3 error cases, default steps, total steps)

### Task 8.5 â€” REST APIs (COMPLETE âœ…)
- **Created**: `internal/api/decisions_api.go` + `decisions_api_test.go`
- `DecisionsAPIHandler` with injectable `DecisionJournal` + `DecisionNarrator` interfaces
- 5 routes: `GET /api/v1/decisions` (+trigger filter), `GET /api/v1/decisions/{id}`, `GET /api/v1/decisions/{id}/explain`, `GET /api/v1/decisions/summary?window=Xh`, `GET /api/v1/decisions/search?q=text`
- **Created**: `internal/api/whatif_api.go` + `whatif_api_test.go`
- `WhatIfAPIHandler` with injectable `HistoryProvider`, `DecisionProvider`, `SolverFunc`, `SLOCurveSolverFactory`
- 3 routes: `POST /api/v1/simulate`, `GET /api/v1/simulate/{id}`, `POST /api/v1/simulate/slo-cost-curve`
- In-memory result store (mutex-protected map keyed by UUID)
- 32 new tests (16 decisions + 16 what-if)

### Task 8.6 â€” Integration + Perf Tests (COMPLETE âœ…)
- **Created**: `internal/simulator/integration_test.go`
- `buildMultiServiceHistory()` helper: deterministic multi-service PromQL-keyed data (CPU ramp with phase offset per service)
- `buildMultiServiceDecisions()` helper: generates actual decisions at CPU > 0.7 thresholds
- History keys use exact PromQL substrings matching the simulator's queries (e.g. `container_cpu_usage_seconds_total{service="svc"}`)
- 4 integration tests: end-to-end pipeline (simulator + SLO curve for 2 services), multi-service cost comparison (expensive vs cheap solver), SLO breach propagation, curve monotonicity across 5 services
- 2 perf tests: 24h Ã— 5-service simulation in <30s (actual: 513Âµs), SLO-cost curve 10 steps in <10s (actual: 1.7ms)
- 6 new tests total

## Phase 8 Status
**COMPLETE âœ…** â€” All 6 tasks done. 34 simulator + 35 explainability + 32 API = 101 new Phase 8 tests.

## Next Phase
**Phase 9 â€” Dashboard UI** (see `prompts/09-dashboard-ui.md`)

---

## Phase 9 â€” React Dashboard UI (IN PROGRESS ðŸ”¶)

### Tasks 9.1 + 9.2 â€” React Scaffold + API Layer (COMPLETE âœ…)

**Stack**: Vite 8 + React 19 + TypeScript 5.9 + Tailwind CSS v4 (`@tailwindcss/vite`) + TanStack Query v5 + React Router v7 + Recharts + lucide-react

**Location**: `ui/dashboard/`

**Design System** â€” "deep space control room":
- Colors: `#080d12` bg-base, `#22d3ee` cyan accent, `#f59e0b` amber alerts, `#10b981` emerald success, `#f43f5e` rose error
- Fonts: Syne (display), DM Sans (body), JetBrains Mono (metrics/mono) â€” loaded via Google Fonts CDN
- `@theme {}` block in `index.css` with all CSS custom properties, no `tailwind.config.js`

**Files created/modified**:
- `vite.config.ts` â€” `@tailwindcss/vite` plugin, `@/` path alias, `/api` proxy â†’ `localhost:8080`
- `tsconfig.app.json` â€” `baseUrl: "."`, `paths: { "@/*": ["./src/*"] }`
- `src/index.css` â€” Google Fonts imports, Tailwind v4 `@theme {}` tokens, scrollbar styles
- `src/lib/utils.ts` â€” `cn()`, `formatCost()`, `formatPercent()`, `formatDuration()`
- `src/api/types.ts` â€” All TS interfaces: TenantState, FairnessResponse, DecisionRecord, SimulationResult, CurvePoint, etc.
- `src/api/client.ts` â€” Typed `api` object with `tenants`, `decisions`, `simulate` namespaces
- `src/api/hooks.ts` â€” TanStack Query hooks for all 8+ endpoints
- `src/components/Layout.tsx` â€” Sidebar (logo, cluster selector, nav links) + header (status pills) + `<Outlet />`
- `src/pages/SLOOverview.tsx` â€” Heatmap (8 services Ã— 3 metrics), 4 stat cards, recent decisions list
- `src/pages/FairnessDashboard.tsx` â€” Noisy-neighbor banners, tenant allocation bars, fairness score cards
- `src/pages/DecisionExplorer.tsx` â€” Filterable decision timeline with expandable narrative + weights + candidates
- `src/pages/WhatIfTool.tsx` â€” Simulation form + results card + SLO-cost curve bar chart
- `src/App.tsx` â€” React Router `<BrowserRouter>` + `<Routes>` wrapping `<Layout />`
- `src/main.tsx` â€” `<QueryClientProvider>` wrapping `<App />`

**Build status**: âœ… `npm run build` clean â€” 0 errors, 0 warnings

### Next Steps in Phase 9
- Task 9.7: Add Recharts line charts for SLO burn rate and fairness index trends
- Task 9.8: Wire up real backend (Go `cmd/server/main.go`) serving the API + static files

## Phase 10 Additions
- `api/global/v1alpha1/groupversion_info.go` â€” `global.optipilot.ai/v1alpha1` GroupVersion, SchemeBuilder, AddToScheme
- `api/global/v1alpha1/clusterprofile_types.go` â€” `ClusterProfile` (cluster-scoped): enums `CloudProvider` (aws/gcp/azure/on-prem/other), `ClusterHealthStatus` (healthy/degraded/unreachable/hibernating/unknown); spec: provider, region, endpoint, capabilities, cost profile, carbon intensity, labels; status: health, capacity (cores/memory/nodes), SLO %, hourly cost, heartbeat, conditions
- `api/global/v1alpha1/globalpolicy_types.go` â€” `GlobalPolicy` (cluster-scoped): enums `TrafficStrategy` (latency-optimized/cost-optimized/carbon-optimized/balanced); spec: traffic shifting (strategy, max shift %, min SLO %, rollback window, cluster selector), cluster lifecycle (hibernation flags, min clusters, idle threshold, wakeup lead), cross-cluster constraints (tenant, regions, providers, max clusters); status: last optimization, active/hibernating counts, directive summary
- `api/global/v1alpha1/zz_generated.deepcopy.go` â€” DeepCopy for all types (hand-written, full slice/map/pointer isolation)
- 22 tests in `types_test.go` covering all types, enums, DeepCopy isolation, nil safety, runtime.Object interface, scheme registration

## Phase 11 Additions â€” Task 11.1 ApplicationTuning CRD
- `api/tuning/v1alpha1/groupversion_info.go` â€” `tuning.optipilot.ai/v1alpha1` GroupVersion, SchemeBuilder, AddToScheme
- `api/tuning/v1alpha1/applicationtuning_types.go` â€” `ApplicationTuning` (namespaced): enums ParameterType (integer/float/string), ParameterSource (configmap/env), TuningPhase (7 phases); TunableParameter with ConfigMapRef/env, min/max/step/allowedValues; OptimizationTarget (minimize/maximize + promQL); TuningSafetyPolicy; ApplicationTuningSpec (targetRef, parameters 1+, safety, interval, maxObservations, paused); ApplicationTuningStatus (phase, currentValues/bestValues maps, observations, conditions); root + list types
- `api/tuning/v1alpha1/zz_generated.deepcopy.go` â€” DeepCopy for all 10 types (map, slice, pointer isolation)
- 22 tests in `types_test.go`

## Phase 11 Additions â€” Task 11.2 Parameter Optimizer
- `internal/tuning/optimizer.go` â€” `Optimizer` struct (SLOFetcher + ParameterApplier interfaces, injectable nowFn); `GenerateGrid` (int/float step-based grid, string AllowedValues, NaN/Inf guard, maxPoints cap); `BestFromObservations` (max SLO pick); `SelectOptimal` (minimize negates, maximize raw, first-grid fallback); `SafetyCheck.CanChange` (cooldown + SLO threshold); `WithinChangeBounds` + `clampToMaxChange` (numeric pct bound, string passthrough); `NextParameterToTune` (least-observed, skip active); `RunCycle` (pausedâ†’fetchSLOâ†’observeâ†’safetyâ†’convergeâ†’gridâ†’selectâ†’clampâ†’applyâ†’cooldown); `resolveSafetyPolicy` (nil defaults 50/5/true/95); `isConverged` (all params â‰¥3 obs)
- `internal/tuning/optimizer_test.go` â€” 44 tests: 8 GenerateGrid, 3 BestFromObservations, 4 SelectOptimal, 4 SafetyCheck, 4 WithinChangeBounds, 4 clampToMaxChange, 3 NextParameterToTune, 3 resolveSafetyPolicy/isConverged, 8 RunCycle integration

## Phase 11 Additions â€” Task 11.3 Custom Metric Adapter
- `internal/metrics/custom_adapter.go` â€” `CustomMetricAdapter` (wraps PrometheusClient); `Fetch(ctx, []CustomMetric) (map[string]float64, []CustomMetricResult)` â€” concurrent goroutine-per-metric queries; `CustomMetricResult{Name, Value, Target, Weight, Err}`; `Score([]CustomMetricResult) float64` â€” weighted distance, denom clamped â‰¥1, skips errors/zero-weight; `MergeIntoMetrics(dst, fetched)` â€” merge into EvalContext.Metrics
- `internal/metrics/custom_adapter_test.go` â€” 19 tests: Fetch (empty, single, parallel, partial failure, all fail, preserves meta), Score (empty, on-target, off-target, weighted, skip errors, skip zero-weight, small/negative target), Merge (nil dst, overwrites, empty, both nil), integration (fetchâ†’score)

## Phase 11 Additions â€” Task 11.4 Storage Recommender
- `internal/storage/recommender.go` â€” `PVCMetrics` (IOPS r/w, throughput r/w, latency r/w, queue depth, current class, capacity); `ClassifyProfile` (idleâ†’burstyâ†’sequentialâ†’read-heavyâ†’write-heavyâ†’randomâ†’mixed); `StorageClassProfile` (name, maxIOPS, maxThroughput, avgLatency, costPerGiBMonth, bestFor); `DefaultCatalog` (gp3/io2/st1 modelled on AWS EBS); `Recommender.Recommend(PVCMetrics) Recommendation` â€” classifyâ†’score all classesâ†’pick lowest penaltyâ†’cost estimateâ†’annotations; `classScore` (profile affinity +100, IOPS penalty *50, throughput penalty *50, latency, cost*10); `RecommendAll`, `ChangesOnly`; `Recommendation` includes annotations: storage-profile, storage-recommended, cost-current, cost-new, savings
- `internal/storage/recommender_test.go` â€” 38 tests

## Phase 11 Additions â€” Task 11.5 Solver Integration
- `internal/engine/advanced_signals.go` â€” `AdvancedSignals{CustomMetricResults, TuningOverrides, StorageRecommendations}`; `EnrichMetrics(input, signals)` merges custom metric values into SolverInput.Metrics for CEL expressions; `EnrichTuning(action, signals)` copies tuning overrides into ScalingAction.TuningParams; `CustomMetricScore(signals)` returns weighted distance; `StorageMonthlySavings/StorageHourlySavingsEstimate`; `AdjustCostScore(original, hourlyCost, signals)` adds storage savings bonus (capped 0.2); `EnrichScoredCandidates(scored, signals)` applies cost adjustment + custom metric penalty in-place
- `internal/engine/advanced_signals_test.go` â€” 29 tests
- All nil-safe: nil AdvancedSignals = no-op, fully backward-compatible

## Phase 11 Additions â€” Task 11.6 Integration Tests
- `test/integration/phase11_integration_test.go` â€” 5 integration tests:
  1. `TestOptimizer_ConvergesToKnownOptimal` â€” parabolic SLO oracle (optimum=5), grid search converges within 20 cycles; validates best value, SLO â‰¥50, observation count
  2. `TestStorageRecommender_SyntheticProfiles` â€” 5 sub-tests (write-heavy, sequential, read-heavy, idle, bursty) with crafted IOPS/throughput data; verifies ClassifyProfile + Recommend class match
  3. `TestCustomMetricInjection_MockPrometheus` â€” mock HTTP Prometheus â†’ CustomMetricAdapter.Fetch â†’ Score; validates weighted distance
  4. `TestEndToEnd_ApplicationTuning_CycleUpdatesConfigMap` â€” single-param CRD (worker_threads) â†’ Optimizer.RunCycle â†’ ConfigMap applier â†’ AdvancedSignals.EnrichTuning; 2 explicit cycles + convergence loop; verifies observations, SLO values, ConfigMap updates, solver enrichment
  5. `TestFullPipeline_AllSignalsEnrich` â€” assembles custom metrics + storage recommendations + tuning overrides â†’ AdvancedSignals enrichment pipeline; validates metrics merge, cost adjustment, tuning params, combined scoring

## Current Phase
**Phase 12 — Packaging, Helm Chart, Documentation & Release** (ALL 7 TASKS COMPLETE — 213 helm tests + 37 E2E tests)

## Phase 12 Additions â€” Task 12.1 Helm Chart Structure
- `helm/optipilot/Chart.yaml` â€” root chart v0.1.0; 3 conditional sub-chart deps (cluster-agent, ml-service, hub)
- `helm/optipilot/values.yaml` â€” global (imageRegistry, prometheusURL, journalBackend, namespace); clusterAgent (enabled, image, args, serviceAccount, rbac, service, resources, persistence, config); mlService (enabled=false, opt-in); hub (enabled=false, opt-in, mtls); ingress; serviceMonitor
- `helm/optipilot/crds/` â€” 4 CRDs: slo.optipilot.ai_serviceobjectives.yaml, policy.optipilot.ai_optimizationpolicies.yaml, tenant.optipilot.ai_tenantprofiles.yaml, tuning.optipilot.ai_applicationtunings.yaml
- `helm/optipilot/charts/cluster-agent/` â€” Deployment (runAsNonRoot, allowPrivilegeEscalation=false, readOnlyRootFilesystem, liveness/readiness on /healthz//readyz, PVC for SQLite, leader-elect arg), Service (metrics+dashboard), ServiceAccount, ClusterRole+ClusterRoleBinding (all 4 API groups + HPA + Karpenter + leases), ConfigMap
- `helm/optipilot/charts/ml-service/` â€” Deployment (readiness on /v1/health, PVC for models), Service
- `helm/optipilot/charts/hub/` â€” Deployment (mTLS tls mount when enabled), Service (gRPC 50051), Certificate (cert-manager.io/v1, conditional on mtls.enabled)
- `test/helm/chart_test.go` â€” 21 structural tests (no Kubernetes required)

## Phase 12 Additions â€” Task 12.2 Container Image Builds
- `Dockerfile` â€” updated: Go 1.25, `-ldflags="-s -w"`, multi-arch TARGETOS/TARGETARCH ARGs, `./cmd/manager`
- `Dockerfile.hub` â€” new: same pattern for `./cmd/hub`, distroless nonroot 65532
- `.ko.yaml` â€” ko build config: defaultBaseImage=distroless/static:nonroot, two builds (manager + hub), platforms linux/amd64+linux/arm64, -X main.version/commit/buildDate ldflags
- `Makefile` â€” new vars: REGISTRY=ghcr.io/optipilot-ai/optipilot, VERSION=git describe, IMG_MANAGER/HUB/ML; new targets: ko (install), image-manager/hub/ml (build), images (all three), push/push-manager/push-hub/push-ml, image-load-kind (kind workflow)
- `cmd/manager/main.go` + `cmd/hub/main.go` â€” `var version, commit, buildDate` ldflags injection + `setupLog.Info("starting...", "version", version, ...)`
- `test/helm/images_test.go` â€” 31 tests: Dockerfile security (distroless, nonroot, CGO=0, strip), .ko.yaml (YAML validity, platforms, ldflags), Makefile targets, version vars in binaries

## Phase 12 Additions â€” Task 12.3 Documentation (âœ… COMPLETE)
- `docs/getting-started.md` â€” kind quickstart: cluster â†’ prometheus â†’ install â†’ first SLO â†’ observe decisions â†’ cleanup
- `docs/installation.md` â€” prerequisites table, helm install, production values.yaml, ML/hub enable, security, upgrade, uninstall
- `docs/architecture.md` â€” ASCII system diagram (hub+cluster-agent+actuators+ML), 7 component descriptions, data flow pipeline, CRD relationships, Prometheus metrics table
- `docs/api-reference.md` â€” REST API: GET/POST /api/v1/decisions, /decisions/{id}, /whatif/simulate, /whatif/slo-cost-curve, /tenants, /tenants/{name}/quota, /healthz, /readyz, /metrics; full JSON schemas
- `docs/configuration.md` â€” all Helm values documented (global, clusterAgent, prometheus, mlService, hub, mtls, ingress, serviceMonitor) with types, defaults, descriptions; full production example
- `docs/cel-reference.md` â€” candidate/slo/tenant/metrics/cluster variable tables, 5 built-in functions (spotRisk, carbonIntensity, costRate, p99Headroom, budgetPercent), operator patterns, debugging guide
- `docs/troubleshooting.md` â€” 15 issue/cause/fix entries (CRD not found, ImagePullBackOff, leader election, Prometheus, SLO, CEL, HPA, Karpenter, hub mTLS)
- `docs/crds/service-objective.md` â€” slo.optipilot.ai/v1alpha1 field reference + burn rate model
- `docs/crds/optimization-policy.md` â€” policy.optipilot.ai/v1alpha1 field reference + CEL quick-reference
- `docs/crds/tenant-profile.md` â€” tenant.optipilot.ai/v1alpha1 field reference + fair-share algorithm (3 phases)
- `docs/crds/application-tuning.md` â€” tuning.optipilot.ai/v1alpha1 field reference + actuator mapping + rollback mechanism
- `docs/guides/first-slo.md` â€” 8-step tutorial: deploy echo-api â†’ ServiceMonitor â†’ ServiceObjective â†’ OptimizationPolicy â†’ view decisions â†’ burn alerts â†’ dry-run â†’ cleanup
- `docs/guides/custom-policy.md` â€” 7 CEL levels: budget guard, burn-rate speed limits, cost minimization, Spot safety, carbon-aware, tenant-aware, custom Prometheus metrics
- `docs/guides/multi-cluster.md` â€” hub + 2 spokes: cert-manager CA, hub install, 2 spoke installs, cross-cluster journal, what-if, mTLS rotation, troubleshooting table
- `docs/guides/what-if.md` â€” 5 use cases: Spot simulation, optimal replicas, carbon regions, parameter tuning, policy pre-validation; interpreting results table
- `docs/guides/migration-cloudpilot.md` â€” concept mapping table, dry-run parallel install, YAML migration examples (ServiceLevelâ†’ServiceObjective, ScalingPolicyâ†’OptimizationPolicy, TenantGroupâ†’TenantProfile), rollback plan
- `test/helm/docs_test.go` â€” 73 tests (package helm_test, helpers: docContent + docContains)

## Phase 10 Additions â€” Task 10.3 gRPC Service Definition
- `internal/global/grpc/doc.go` â€” package documentation
- `internal/global/grpc/messages.go` â€” all RPC message types: `RegisterClusterRequest/Response`, `ClusterStatusReport`, `StatusAck`, `Directive` (5 types: traffic_shift, migration, hibernate, wake_up, noop), `MigrationHint`, `GetDirectiveRequest/Response`, `TrafficShiftRequest/Response`, `Capabilities`, `CostProfileMsg`
- `internal/global/grpc/service.go` â€” `OptiPilotHubService` interface (4 methods)
- `internal/global/grpc/hub_server.go` â€” `HubServer` (gRPC server, manual `grpc.ServiceDesc`, mTLS support, graceful shutdown); `MemoryHubService` (thread-safe in-memory impl, 5-min heartbeat TTL, drain-on-fetch directives); `JSONCodec` (JSON-over-gRPC)
- `internal/global/grpc/spoke_client.go` â€” `SpokeClient` (4 RPC methods, insecure fallback, `grpc.NewClient` + JSON content subtype)
- `internal/global/grpc/mtls.go` â€” `MTLSConfig`, `ServerCredentials()`, `ClientCredentials()` (TLS 1.3, RequireAndVerifyClientCert)
- 15 tests in `grpc_test.go`: full gRPC round-trip, validation errors, directive drain, mTLS error paths, JSONCodec, server lifecycle

## Key API Contracts (Phase 10 â€” gRPC)
- `hubgrpc.NewHubServer(addr, svc, tlsCreds) *HubServer` â€” nil tlsCreds = insecure
- `hubgrpc.NewSpokeClient(addr, tlsCreds) (*SpokeClient, error)` â€” nil tlsCreds = insecure
- `SpokeClient.RegisterCluster(ctx, *RegisterClusterRequest) (*RegisterClusterResponse, error)`
- `SpokeClient.ReportStatus(ctx, *ClusterStatusReport) (*StatusAck, error)`
- `SpokeClient.GetDirective(ctx, clusterName) (*GetDirectiveResponse, error)`
- `SpokeClient.RequestTrafficShift(ctx, *TrafficShiftRequest) (*TrafficShiftResponse, error)`
- `MemoryHubService.EnqueueDirective(clusterName, Directive)` â€” for testing
- `MemoryHubService.IsHealthy(clusterName) bool` â€” checks heartbeat TTL
- Service name: `optipilot.hub.v1.OptiPilotHub` â€” 4 unary methods
- JSON codec registered via `init()` in spoke_client.go

## Phase 10 Additions â€” Task 10.4 Spoke Agent Registration
- `internal/global/spoke/collector.go` â€” `StatusCollector` interface (Collect â†’ ClusterStatusReport), `DirectiveHandler` interface (Handle), `RegistrationInfo` struct, `StaticCollector` (test stub), `LogDirectiveHandler` (test stub)
- `internal/global/spoke/agent.go` â€” `SpokeAgent` implements `manager.Runnable`; lifecycle: connect â†’ register â†’ heartbeat loop + directive poll loop â†’ stopped; honours hub-returned HeartbeatIntervalS; options: `WithHeartbeatInterval`, `WithDirectivePollInterval`, `WithTLSCredentials`, `WithLogger`, `WithNowFunc`; state accessors: `State()`, `LastHeartbeat()`, `HeartbeatCount()`, `DirectiveCount()`, `LastError()`, `HeartbeatInterval()`
- `cmd/manager/main.go` â€” added flags: `--hub-endpoint` (gRPC addr, empty disables spoke), `--cluster-name` (required with hub), `--cluster-provider`, `--cluster-region`; spoke agent added via `mgr.Add(spokeAgent)` when hub-endpoint is set
- 13 tests in `agent_test.go`: full gRPC round-trip registration, heartbeat delivery + hub status update, hub interval override, directive polling/handling/drain, stopped state, invalid hub, last heartbeat, defaults, collectors, hub marks unhealthy

## Key API Contracts (Phase 10 â€” Spoke Agent)
- `spoke.NewSpokeAgent(hubAddr, info, collector, handler, ...opts) *SpokeAgent`
- `SpokeAgent.Start(ctx) error` â€” implements `manager.Runnable`; blocks until ctx cancelled
- `spoke.RegistrationInfo{ClusterName, Provider, Region, Endpoint, CarbonIntensityGCO2, Labels, Capabilities, CostProfile}`
- `spoke.StatusCollector` interface: `Collect(ctx) (*ClusterStatusReport, error)`
- `spoke.DirectiveHandler` interface: `Handle(ctx, Directive) error`
- Agent states: `StateDisconnected` â†’ `StateRegistered` â†’ `StateRunning` â†’ `StateStopped`
- Hub-returned `HeartbeatIntervalS > 0` overrides the agent's configured interval

## Phase 10 Additions â€” Task 10.5 Global Solver
- `internal/global/solver.go` â€” `GlobalSolver` with `Solve(*SolverInput) (*SolverResult, error)`; two-phase: (1) traffic weights via 4-dim scoring (latency proxy, cost, carbon, SLO) with strategy-specific weights + max-shift clamping + proportional allocation summing to 100; (2) lifecycle: hibernate idle clusters (utilization < threshold, respecting min-active + exclusion list), wake hibernating when all active >80% utilization
- Types: `ClusterSnapshot` (UtilizationPercent(), FreeCores()), `SolverInput`, `SolverResult`, `ClusterScore`; `SnapshotFromProfile(*ClusterProfile) *ClusterSnapshot` helper
- 26 tests in `solver_test.go`: traffic weight strategies, SLO filtering, max-shift clamping, hibernation/wake-up, combined directives, scoring helpers

## Key API Contracts (Phase 10 â€” Global Solver)
- `global.NewGlobalSolver() *GlobalSolver`
- `GlobalSolver.Solve(*SolverInput) (*SolverResult, error)`
- `SolverInput{Clusters []*ClusterSnapshot, Policy *GlobalPolicySpec}`
- `SolverResult{Directives []Directive, Summary string, Timestamp time.Time}`
- `SnapshotFromProfile(*ClusterProfile) *ClusterSnapshot`

## Phase 10 Additions â€” Task 10.6 Traffic Shifting
- `internal/global/traffic.go` â€” `TrafficShifter` orchestrates safe, gradual traffic shifts across 3 backends:
  - **Gateway API**: reads/patches `HTTPRoute` `spec.rules[0].backendRefs[].weight` via unstructured client
  - **Istio**: reads/patches `VirtualService` `spec.http[0].route[].weight` via unstructured client
  - **ExternalDNS**: reads/patches `DNSEndpoint` `spec.endpoints[].providerSpecific.weight` for geo-routing
  - Safety: max shift per cycle (default 25%), SLO pre-validation (refuses shift if SLO < 90%), post-shift monitoring with auto-rollback
- Types: `TrafficBackend` (gateway-api/istio/external-dns), `TrafficShiftPlan`, `TrafficShiftResult`, `SLOChecker` interface
- 22 tests in `traffic_test.go`

## Key API Contracts (Phase 10 â€” Traffic Shifting)
- `global.NewTrafficShifter(client, SLOChecker, ...ShifterOption) *TrafficShifter`
- `TrafficShifter.Apply(ctx, TrafficShiftPlan) *TrafficShiftResult`
- `TrafficShifter.MonitorAndRollback(ctx, TrafficShiftPlan, minSLO) *TrafficShiftResult`
- Options: `WithMaxShiftPercent(int32)`, `WithRollbackWindow(time.Duration)`, `WithShifterSleepFn`, `WithShifterNowFn`

## Phase 10 Additions â€” Task 10.7 Cluster Lifecycle Manager
- `internal/global/lifecycle.go` â€” `LifecycleManager` executes hibernate/wake-up directives from the solver:
  - Hibernate: mgmt cluster guard â†’ sole-tenant check â†’ drain â†’ scale to zero; reverts state on error
  - Wake-up: scale up â†’ clear idle tracker; predictive wake-up via `DemandForecaster`
  - Idle tracking: `UpdateIdleStatus()` with configurable threshold + window (default 10% / 30 min)
  - Interfaces: `NodePoolScaler`, `WorkloadDrainer`, `DemandForecaster`, `TenantLocator`
  - States: Active, Draining, Hibernating, Waking
- 24 tests in `lifecycle_test.go`

## Key API Contracts (Phase 10 â€” Lifecycle Manager)
- `global.NewLifecycleManager(scaler, drainer, forecaster, tenants, ...opts) *LifecycleManager`
- `LifecycleManager.ExecuteDirective(ctx, Directive) error`
- `LifecycleManager.UpdateIdleStatus(name, utilPct, policy) bool`
- `LifecycleManager.CheckPredictiveWakeUp(ctx, []*ClusterSnapshot) []Directive`
- `LifecycleManager.ClusterState(name) LifecycleState`
- `LifecycleManager.HibernatingClusters() []string`, `ActiveClusters() []string`
- Strategy weights: latency-optimized (50/15/10/25), cost-optimized (10/50/15/25), carbon-optimized (10/15/50/25), balanced (25/25/25/25)
- Traffic: eligible clusters = healthy/degraded + above minSLO; weights proportional to composite score with max-shift clamp from equal distribution
- Lifecycle: hibernate if util < idleThreshold AND active > minActive AND not excluded; wake if ALL active >80% util AND hibernating cluster exists

## Phase 10 Additions — Task 10.8 Integration Test
- `internal/global/integration_test.go` — 10 integration tests (external `global_test` package) covering the full hub-spoke pipeline:
  - spoke registration + heartbeats, solver traffic/hibernate directives, directive delivery via hub, lifecycle execution (hibernate/wake/management-cluster guard), idle tracking pipeline, predictive wake-up, multi-round solver
- `internal/global/grpc/hub_server.go` — `Start()` now captures actual bound address (port 0 support for testing)
- Phase 10 complete: **110 tests** across `internal/global/...`

## Phase 12 Additions — Task 12.5 Release Automation
- `.github/workflows/release.yaml` — 6-job release pipeline triggered on `v*` tags: `test` (lint + unit + helm tests + binary smoke), `version` (derives semver from GITHUB_REF_NAME), `images` (matrix: manager + hub — QEMU/Buildx, ghcr.io login, semver tags via docker/metadata-action, multi-arch linux/amd64+arm64), `image-ml` (same for `ml/` Python service), `helm-release` (stamps version in Chart.yaml + values.yaml, helm package + lint + OCI push), `github-release` (structured changelog via git log, create release via softprops/action-gh-release@v2 with prerelease detection)
- `.github/workflows/ci.yaml` — updated Go 1.22 → 1.25 (match go.mod), golangci-lint v1.61 → v1.64, added `go test ./test/helm/...` step
- `test/helm/release_test.go` — 21 tests: workflow existence, valid YAML, tag trigger, 6 required jobs, job ordering, ghcr.io registry, OCI push, GHCR auth, GitHub release action, multi-arch, Go version, permissions, prerelease support, CI Go version, CI helm tests

## Phase 12 Additions — Task 12.6 Quickstart Script
- `hack/quickstart.sh` — 7-step idempotent demo: prerequisite check (kind/kubectl/helm/docker) → `kind create cluster optipilot-quickstart` (reuses if exists) → kube-prometheus-stack (grafana+alertmanager off) → image prep (`--build-local` Docker build + kind load, or ghcr.io pull) → `helm install optipilot ./helm/optipilot` (mlService+hub disabled, prometheusURL wired) → demo-api Deployment (nginx:1.27-alpine, 2 replicas) + ServiceObjective + OptimizationPolicy (dryRun=true) → `kubectl port-forward` dashboard to localhost:8090; prints curl examples; `--destroy` deletes cluster + releases ports; `--help` shows full usage; `CLUSTER_NAME`/`REGISTRY`/`VERSION` env vars configurable
- `test/helm/quickstart_test.go` — 27 tests: shebang, pipefail, all flags (--destroy/--build-local/--help), prereqs, CLUSTER_NAME, kind idempotency, kind-config.yaml, prometheus, helm idempotency, local chart path, mlService/hub disabled, demo-api + nginx, ServiceObjective + OptimizationPolicy, dryRun, port-forward + port 8090, condition=Ready wait, destroy, localhost instructions, cleanup command

## Phase 12 Additions — Task 12.7 Open-Source Repo Prep
- `LICENSE` — Apache 2.0, copyright 2026 OptiPilot AI Authors
- `README.md` — CI+Release+License+Helm badges; feature table (9 pillars); ASCII architecture diagram (hub-spoke, cluster agent, ML service, actuators, decision journal); quickstart via `hack/quickstart.sh`; `helm install` OCI command; ServiceObjective + OptimizationPolicy YAML samples; container image table (manager/hub/ml, multi-arch); docs link table; development commands; license section
- `CONTRIBUTING.md` — prerequisites table (Go 1.25, kind, kubectl, helm, golangci-lint), dev setup, project structure, branch/commit conventions (Conventional Commits), coding standards (Go/API/CEL/Python/Helm), test layer matrix, PR checklist, release process
- `.github/PULL_REQUEST_TEMPLATE.md` — type-of-change checklist, motivation, changes, testing checklist (unit/helm/e2e), conventions checklist, Conventional Commits reminder
- `.github/ISSUE_TEMPLATE/bug-report.md` — labels frontmatter, steps to reproduce, expected/actual, environment table (version/k8s/helm/cloud), component checklist, CRD YAML + log fields
- `.github/ISSUE_TEMPLATE/feature-request.md` — problem statement, proposed solution, acceptance criteria, component checklist, priority tiers, willing-to-contribute field
- `test/helm/repo_test.go` — 40 tests: LICENSE (4), README (14), CONTRIBUTING (8), PR template (5), bug template (4), feature template (3); all file existence + content validation

## Phase 12 — COMPLETE ✅
All 7 tasks done. **213 helm tests + 37 E2E tests (build tag e2e)**

| Task | Tests |
|---|---|
| 12.1 Helm Chart Structure | 21 |
| 12.2 Container Image Builds | 31 |
| 12.3 Documentation | 73 |
| 12.4 E2E Test Suite | 37 (e2e) |
| 12.5 Release Automation | 21 |
| 12.6 Quickstart Script | 27 |
| 12.7 Open-Source Repo Prep | 40 |

