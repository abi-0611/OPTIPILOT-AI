# CEL Expression Reference

OptiPilot uses the **Common Expression Language (CEL)** in `OptimizationPolicy` objects to express constraints and objectives. This reference covers all available variables, functions, and operators.

## Variable Namespaces

### `candidate.*` — Proposed Configuration

Variables describing the configuration being evaluated.

| Variable | Type | Description |
|---|---|---|
| `candidate.cpuCores` | float | Total CPU cores across all replicas |
| `candidate.replicas` | int | Number of replicas |
| `candidate.requestCPUCores` | float | Per-pod CPU request |
| `candidate.requestMemoryGiB` | float | Per-pod memory request |
| `candidate.limitCPUCores` | float | Per-pod CPU limit |
| `candidate.limitMemoryGiB` | float | Per-pod memory limit |
| `candidate.instanceType` | string | Node instance type (e.g., `m5.large`) |
| `candidate.spotEnabled` | bool | Whether Spot/Preemptible is enabled |
| `candidate.region` | string | Cloud region |
| `candidate.az` | string | Availability zone |

### `slo.*` — SLO Status

Variables reflecting the current `ServiceObjective` status.

| Variable | Type | Description |
|---|---|---|
| `slo.availability` | float | Current availability ratio (0.0–1.0) |
| `slo.latencyP99Ms` | float | 99th percentile latency in milliseconds |
| `slo.latencyP95Ms` | float | 95th percentile latency in milliseconds |
| `slo.errorBudgetRemaining` | float | Remaining error budget (0.0–1.0) |
| `slo.burnRate1h` | float | 1-hour error budget burn rate |
| `slo.burnRate6h` | float | 6-hour error budget burn rate |
| `slo.burnRate24h` | float | 24-hour error budget burn rate |
| `slo.name` | string | ServiceObjective name |
| `slo.namespace` | string | ServiceObjective namespace |

### `tenant.*` — Tenant Context

Variables about the tenant submitting the request.

| Variable | Type | Description |
|---|---|---|
| `tenant.name` | string | Tenant name from TenantProfile |
| `tenant.tier` | string | `premium`, `standard`, or `economy` |
| `tenant.currentCores` | float | Current CPU consumption |
| `tenant.maxCores` | float | Configured max CPU budget |
| `tenant.currentCostUSD` | float | Current monthly cost |
| `tenant.maxCostUSD` | float | Monthly cost budget |
| `tenant.fairShareWeight` | float | Fair-share weight |
| `tenant.allocationStatus` | string | `guaranteed`, `bursting`, or `throttled` |

### `metrics[]` — Prometheus Metrics

Access arbitrary Prometheus metrics by name. Metrics are resolved at evaluation time via the embedded Prometheus client.

```cel
metrics["http_server_requests_total"] > 1000
metrics["jvm_memory_used_bytes{area='heap'}"] < 5e8
```

If a metric is not found, the value is `0.0`. Use `has(metrics, "name")` to check existence before comparing.

### `cluster.*` — Cluster State

Variables describing the current cluster state.

| Variable | Type | Description |
|---|---|---|
| `cluster.availableCores` | float | Total schedulable CPU cores |
| `cluster.usedCoresPercent` | float | CPU utilization 0–100 |
| `cluster.availableMemoryGiB` | float | Total schedulable memory |
| `cluster.spotCapacityScore` | float | Spot availability score (0–1) |
| `cluster.name` | string | Cluster name |
| `cluster.region` | string | Cluster AWS/GCP/Azure region |

## Built-in Functions

### `spotRisk(instanceType: string, region: string) → float`

Returns the current Spot interruption probability for the instance type and region. Value is between 0.0 (no risk) and 1.0 (always interrupted).

```cel
// Reject Spot if interruption risk > 20%
!candidate.spotEnabled || spotRisk(candidate.instanceType, candidate.region) < 0.2
```

Data source: AWS Spot Advisor / GCP Preemptible history (refreshed every 15 minutes).

### `carbonIntensity(region: string) → float`

Returns current grid carbon intensity for the region in gCO₂eq/kWh. Source: Electricity Maps API (refreshed every 30 minutes).

```cel
// Prefer low-carbon regions
carbonIntensity(candidate.region) < 200.0
```

### `costRate(instanceType: string, region: string, spot: bool) → float`

Returns the hourly cost of a single instance in USD.

```cel
// Constraint: total hourly cost must not exceed $10
costRate(candidate.instanceType, candidate.region, candidate.spotEnabled) 
  * double(candidate.replicas) < 10.0
```

### `p99Headroom(latencyMs: float) → float`

Returns the headroom in milliseconds between current p99 latency and the SLO target.

```cel
p99Headroom(slo.latencyP99Ms) > 20.0
```

### `budgetPercent() → float`

Returns the remaining error budget as a percentage (0–100).

```cel
// Use aggressive optimizations only when budget is healthy
budgetPercent() > 50.0
```

## Operators and Patterns

### Guards on Error Budget

```cel
// Never optimize when budget is nearly exhausted
slo.errorBudgetRemaining > 0.1
```

### Multi-condition Constraints

CEL conditions are ANDed — all constraints must evaluate to `true` for an action to pass:

```cel
// In OptimizationPolicy constraints list:
- expression: "slo.burnRate1h < 2.0"
  message: "Burn rate too high"
- expression: "candidate.cpuCores <= 32.0"
  message: "Cannot exceed 32 cores"
- expression: "tenant.currentCostUSD + (costRate(candidate.instanceType, candidate.region, false) * 720.0) <= tenant.maxCostUSD"
  message: "Would exceed monthly budget"
```

### Minimizing Objectives

Objectives are expressed as CEL strings where **lower is better**:

```cel
# Minimize cost
"costRate(candidate.instanceType, candidate.region, candidate.spotEnabled) * double(candidate.replicas)"

# Minimize latency
"slo.latencyP99Ms"

# Minimize carbon emissions
"carbonIntensity(candidate.region) * candidate.cpuCores"
```

Multiple objectives are combined using weighted Pareto optimization.

## Debugging CEL Expressions

Use the what-if dry-run API to test CEL evaluation with real cluster state:

```bash
curl -X POST http://localhost:8090/api/v1/whatif/simulate \
  -H 'Content-Type: application/json' \
  -d '{
    "policyName": "prod-policy",
    "scenario": {
      "replicas": 5,
      "instanceType": "m5.xlarge",
      "spotEnabled": true
    }
  }'
```

The response includes `celTrace` showing which constraint passed or failed and the evaluated variable values.

## Common Mistakes

| Mistake | Fix |
|---|---|
| Type mismatch: `candidate.replicas > 0.5` | Use `double(candidate.replicas) > 0.5` |
| Accessing missing metric | Use `has(metrics, "name")` guard |
| Dividing without null-guard | Check denominator > 0 before dividing |
| Using string comparison on float | Always use numeric operators on numeric fields |
