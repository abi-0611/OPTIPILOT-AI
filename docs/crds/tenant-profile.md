# TenantProfile CRD Reference

**API Group:** `tenant.optipilot.ai/v1alpha1`  
**Kind:** `TenantProfile`  
**Scope:** Namespaced

## Example

```yaml
apiVersion: tenant.optipilot.ai/v1alpha1
kind: TenantProfile
metadata:
  name: platform-team
  namespace: optipilot-system
spec:
  tenantName: platform
  tier: premium
  namespaces:
    - platform-prod
    - platform-staging
  resourceBudget:
    maxCores: 64
    maxMemoryGiB: 256
    maxMonthlyCostUSD: 5000
  fairSharePolicy:
    weight: 3
    guaranteedCoresPercent: 20
    burstable: true
    maxBurstPercent: 150
```

## Spec Fields

### `spec.tenantName`

Unique identifier for the tenant. Used in fairness metrics and decision journal records.

### `spec.tier`

Tenant service tier. Affects default scheduling priority.

| Value | Description |
|---|---|
| `premium` | Highest priority, guaranteed resources |
| `standard` | Standard allocation (default) |
| `economy` | Best-effort, first to be throttled |

### `spec.namespaces[]`

List of Kubernetes namespaces that belong to this tenant. Resource usage is aggregated across all listed namespaces.

### `spec.resourceBudget`

Hard limits for tenant resource consumption.

| Field | Type | Description |
|---|---|---|
| `maxCores` | float | Maximum CPU cores |
| `maxMemoryGiB` | float | Maximum memory in GiB |
| `maxMonthlyCostUSD` | float | Maximum monthly cost in USD |

A value of `0` means unlimited.

### `spec.fairSharePolicy`

Controls how the scheduler allocates cluster resources among tenants.

| Field | Type | Default | Description |
|---|---|---|---|
| `weight` | float | `1.0` | Relative weight in fair-share calculation |
| `guaranteedCoresPercent` | float | `0` | Minimum guaranteed CPU as % of cluster |
| `burstable` | bool | `false` | Allow bursting beyond guarantee |
| `maxBurstPercent` | float | `100` | Maximum burst as % of guaranteed allocation |

## Fair-Share Algorithm

OptiPilot uses a 3-phase fair-share algorithm:

1. **Guarantee phase** — each tenant receives `guaranteedCoresPercent` of cluster cores
2. **Burst phase** — remaining capacity distributed by `weight` among burstable tenants
3. **Cap phase** — allocations exceeding `maxBurstPercent` are capped; excess redistributed iteratively (up to 10 rounds)

## Status Fields

| Field | Type | Description |
|---|---|---|
| `status.currentCores` | float | Observed CPU consumption |
| `status.currentMemoryGiB` | float | Observed memory consumption |
| `status.allocationStatus` | string | `guaranteed`, `bursting`, `throttled`, or `under_allocated` |
| `status.fairnessScore` | float | Jain's fairness index contribution (0–1) |
| `status.lastRefreshed` | dateTime | Timestamp of last Prometheus refresh |

## Noisy-Neighbor Detection

OptiPilot continuously monitors tenants that consume disproportionately more than their fair share. When a tenant exceeds 3× its fair allocation for more than 5 minutes, an event is emitted:

```
Warning  NoisyNeighbor  serviceobjective/api-slo  Tenant "platform" 
  consuming 3.4× fair share (42 cores vs 12 allowed), 
  impacting 2 co-located tenants
```

## Quota Enforcement

`CheckQuota` is evaluated before every actuation:
- **Cores:** `currentCores + delta.additionalCores > maxCores` → denied
- **Memory:** same pattern
- **Cost:** `currentCostUSD + delta.additionalCostUSD > maxMonthlyCostUSD` → denied

Actions that would push a tenant over quota are blocked; the decision journal records the blocked actuation with reason.
