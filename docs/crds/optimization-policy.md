# OptimizationPolicy CRD Reference

**API Group:** `policy.optipilot.ai/v1alpha1`  
**Kind:** `OptimizationPolicy`  
**Scope:** Namespaced

## Example

```yaml
apiVersion: policy.optipilot.ai/v1alpha1
kind: OptimizationPolicy
metadata:
  name: cost-aware-scaling
  namespace: production
spec:
  selector:
    matchLabels:
      team: platform
  constraints:
    - name: min-replicas
      expression: "candidate.replicas >= 2"
      message: "Always maintain at least 2 replicas for HA"
    - name: max-cost
      expression: "candidate.hourlyCostUSD <= 20.0"
      message: "Hourly cost must not exceed $20"
    - name: no-spot-when-slo-low
      expression: "slo.budgetRemaining >= 0.2 || candidate.spotRatio == 0.0"
      message: "Disable spot instances when SLO budget below 20%"
      soft: true
  objectives:
    - name: minimize-cost
      weight: 0.4
      direction: minimize
      expression: "candidate.hourlyCostUSD"
    - name: maximize-slo
      weight: 0.4
      direction: maximize
      expression: "slo.compliance"
    - name: minimize-carbon
      weight: 0.2
      direction: minimize
      expression: "candidate.carbonIntensity"
  dryRun: false
```

## Spec Fields

### `spec.selector`

Standard Kubernetes `LabelSelector`. Matches `ServiceObjective` resources by label. An empty selector matches all service objectives in the namespace.

### `spec.constraints[]`

CEL expressions evaluated against each candidate plan. All hard constraints must pass for a candidate to be considered viable.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | ✓ | Human-readable constraint name |
| `expression` | string | ✓ | CEL boolean expression |
| `message` | string | | Displayed when constraint fails |
| `soft` | bool | | If `true`, failure penalizes score but doesn't discard candidate (default `false`) |

### `spec.objectives[]`

Weighted objectives used to break ties among Pareto-optimal candidates.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | ✓ | Objective name |
| `weight` | float | ✓ | Relative weight (weights are normalised to sum to 1) |
| `direction` | string | ✓ | `minimize` or `maximize` |
| `expression` | string | | CEL expression returning a float (optional override) |

### `spec.dryRun`

When `true`, the policy is compiled and evaluated but actuation is skipped. Useful for testing new constraints without affecting the live system.

## CEL Expression Reference

See [cel-reference.md](../cel-reference.md) for all available variables, functions, and examples.

### Quick reference

| Variable | Type | Description |
|---|---|---|
| `candidate.replicas` | int | Proposed replica count |
| `candidate.instanceType` | string | Proposed instance type |
| `candidate.hourlyCostUSD` | double | Estimated hourly cost |
| `candidate.spotRatio` | double | Fraction of spot instances (0–1) |
| `candidate.carbonIntensity` | double | Estimated gCO₂/hour |
| `slo.compliance` | double | Current SLO compliance (0–1) |
| `slo.budgetRemaining` | double | Error budget remaining (0–1) |
| `slo.burnRate` | double | Current multi-window burn rate |
| `metrics["name"]` | double | Custom metric value by name |

### Built-in functions

| Function | Signature | Description |
|---|---|---|
| `spotRisk(instanceType, az)` | `(string, string) → double` | Spot interruption probability (0–1) |
| `carbonIntensity(region)` | `(string) → double` | Estimated gCO₂/kWh for region |
| `costRate(instanceType, count)` | `(string, int) → double` | Estimated hourly cost in USD |

## Webhook Validation

All `expression` fields are compiled at admission time. Invalid CEL expressions are rejected with a clear error message:

```
Error from server: OptimizationPolicy "my-policy" is invalid:
  spec.constraints[0].expression: CEL compilation failed:
    undeclared reference to 'candidat' (did you mean 'candidate'?)
```
