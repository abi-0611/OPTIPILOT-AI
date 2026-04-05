# ServiceObjective CRD Reference

**API Group:** `slo.optipilot.ai/v1alpha1`  
**Kind:** `ServiceObjective`  
**Scope:** Namespaced

## Example

```yaml
apiVersion: slo.optipilot.ai/v1alpha1
kind: ServiceObjective
metadata:
  name: payment-service-slo
  namespace: production
spec:
  targetRef:
    name: payment-service
    kind: Deployment
  objectives:
    - metricName: error_rate
      target: "0.1%"
      window: 1h
    - metricName: latency_p99
      target: "200ms"
      window: 5m
  costConstraint:
    maxHourlyCostUSD: "10.00"
  customMetrics:
    - name: cache_hit_ratio
      query: 'sum(rate(cache_hits_total[5m])) / sum(rate(cache_requests_total[5m]))'
      target: 0.8
      weight: 0.5
```

## Spec Fields

### `spec.targetRef`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | ✓ | Name of the target workload |
| `kind` | string | ✓ | `Deployment`, `StatefulSet`, or `Rollout` |
| `namespace` | string | | Defaults to `metadata.namespace` |

### `spec.objectives[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `metricName` | string | ✓ | Well-known metric name (see below) |
| `target` | string | ✓ | Target threshold (e.g., `"0.1%"`, `"200ms"`, `"99.9%"`) |
| `window` | string | ✓ | Evaluation window (e.g., `1h`, `5m`, `3d`) |
| `promQLExpr` | string | | Override auto-generated PromQL — uses this expression directly |

**Well-known `metricName` values:**
- `error_rate` — `sum(rate(http_requests_total{status=~"5.."}[window])) / sum(rate(http_requests_total[window]))`
- `latency_p99` — `histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket[window])) by (le))`
- `availability` — `1 - error_rate`

### `spec.costConstraint`

| Field | Type | Required | Description |
|---|---|---|---|
| `maxHourlyCostUSD` | string | ✓ | Maximum acceptable hourly cost in USD |

### `spec.customMetrics[]`

Injects arbitrary Prometheus queries into the CEL evaluation context.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | ✓ | Metric name (accessible as `metrics["name"]` in CEL) |
| `query` | string | ✓ | PromQL expression |
| `target` | float | | Target value for score normalization |
| `weight` | float | | Weight in composite score (default `1.0`) |

## Status Fields

| Field | Type | Description |
|---|---|---|
| `status.budgetRemaining` | string | Error budget remaining as percentage |
| `status.currentBurnRate` | string | Current multi-window burn rate |
| `status.lastEvaluationTime` | dateTime | Timestamp of last evaluation |
| `status.conditions[]` | Condition | `SLOCompliant`, `BudgetExhausted`, `TargetFound` |

## Printer Columns

```
NAME                    TARGET              BUDGET   COMPLIANT   AGE
payment-service-slo     payment-service     94.2%    True        3d
```

## Burn Rate Model

OptiPilot uses the Google SRE multi-window burn rate model:
- Window 1: 1 hour (fast burn detection)
- Window 2: 6 hours (medium burn)
- Window 3: 3 days (slow creep detection)

A burn rate ≥ 1.0 means the SLO budget is being consumed at exactly the sustainable rate. Rates above 14.4× trigger a page-worthy alert.
