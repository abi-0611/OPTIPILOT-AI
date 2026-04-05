# ApplicationTuning CRD Reference

**API Group:** `tuning.optipilot.ai/v1alpha1`  
**Kind:** `ApplicationTuning`  
**Scope:** Namespaced

## Example

```yaml
apiVersion: tuning.optipilot.ai/v1alpha1
kind: ApplicationTuning
metadata:
  name: payments-tuning
  namespace: payments
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: payments-api
  tunableParameters:
    - name: replicas
      valueType: int
      minValue: "1"
      maxValue: "20"
      currentValue: "3"
      description: Number of pod replicas
    - name: cpu_request_millicores
      valueType: int
      minValue: "100"
      maxValue: "2000"
      currentValue: "500"
      description: CPU request per pod
    - name: memory_limit_mib
      valueType: int
      minValue: "128"
      maxValue: "2048"
      currentValue: "512"
      description: Memory limit per pod
    - name: gc_heap_goal_percent
      valueType: float
      minValue: "50"
      maxValue: "200"
      currentValue: "100"
      description: Go GOGC percentage
  safetyPolicy:
    maxChangePercentPerStep: 30
    cooldownSeconds: 120
    rollbackOnSLOBreach: true
  optimizerPhase: active
```

## Spec Fields

### `spec.targetRef`

Reference to the Kubernetes workload to tune.

| Field | Type | Description |
|---|---|---|
| `apiVersion` | string | Workload API version (e.g., `apps/v1`) |
| `kind` | string | `Deployment`, `StatefulSet`, or `Rollout` |
| `name` | string | Workload name |

### `spec.tunableParameters[]`

List of parameters the optimizer may adjust.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | ✅ | Parameter identifier (used in optimizer and actuator) |
| `valueType` | string | ✅ | `int`, `float`, or `string` |
| `minValue` | string | ✅ | Lower bound (as string) |
| `maxValue` | string | ✅ | Upper bound (as string) |
| `currentValue` | string | ✅ | Current observed value |
| `step` | string | | Minimum step size (defaults to 1 for int, 0.1 for float) |
| `description` | string | | Human-readable description shown in dashboard |

### `spec.safetyPolicy`

Guards against unsafe or disruptive changes.

| Field | Type | Default | Description |
|---|---|---|---|
| `maxChangePercentPerStep` | float | `25` | Maximum relative change per tuning step |
| `cooldownSeconds` | int | `60` | Minimum seconds between consecutive changes |
| `rollbackOnSLOBreach` | bool | `false` | Auto-rollback if SLO burns down after change |
| `requireApproval` | bool | `false` | Gate changes behind manual approval webhook |

### `spec.optimizerPhase`

Lifecycle phase of the optimizer for this workload.

| Value | Description |
|---|---|
| `discovery` | Observing the workload, no changes applied |
| `exploration` | Small random changes to build model |
| `active` | Optimizer issuing tuning recommendations |
| `paused` | Optimizer paused (no new recommendations) |
| `terminated` | Optimizer stopped, `ApplicationTuning` object retained for history |

## Status Fields

| Field | Type | Description |
|---|---|---|
| `status.phase` | string | Current optimizer phase |
| `status.bestParameters` | map | Best-known parameter values by optimizer |
| `status.lastAppliedAt` | dateTime | Timestamp of last applied tuning |
| `status.appliedCount` | int | Total tuning steps applied |
| `status.rollbackCount` | int | Number of rollbacks triggered |
| `status.modelConfidence` | float | Optimizer model confidence (0–1) |

## Parameter Actuator Mapping

When the optimizer issues a recommendation, OptiPilot maps each parameter to an actuator:

| Parameter Name Pattern | Actuator | Action |
|---|---|---|
| `replicas` | HPA / direct patch | Adjusts `spec.replicas` |
| `*_request_*` | Resource patch | Updates `resources.requests` |
| `*_limit_*` | Resource patch | Updates `resources.limits` |
| `GOGC`, `gc_heap_*` | Env-var patch | Sets env var on pod spec |
| `karpenter_*` | Karpenter NodePool | Updates node provisioner |

Custom parameter names not matching these patterns are surfaced as events for user-defined actuators via webhooks.

## Rollback Mechanism

When `rollbackOnSLOBreach: true`:

1. Snapshot of parameter values taken before every change
2. Post-change, SLO error budget burn rate monitored for `cooldownSeconds`
3. If burn rate > 3× pre-change rate → automatic rollback applied
4. Rollback event emitted:

```
Warning  RollbackTriggered  applicationtuning/payments-tuning  
  Rolled back 'replicas' 7→3 due to SLO burn rate spike (1.2→4.1 err/s)
```
