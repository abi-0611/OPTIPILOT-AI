# Migration Guide: CloudPilot AI → OptiPilot AI

This guide assists teams migrating from CloudPilot AI to OptiPilot AI. Both products share similar goals but differ in architecture, CRD schema, and configuration.

---

## Concept Mapping

| CloudPilot AI | OptiPilot AI | Notes |
|---|---|---|
| `ScalingPolicy` | `OptimizationPolicy` | Similar, but OptiPilot uses CEL instead of YAML rule DSL |
| `ServiceLevel` | `ServiceObjective` | Renamed; window now explicit |
| `TenantGroup` | `TenantProfile` | New fields: `fairSharePolicy`, `resourceBudget` |
| `ParameterSet` | `ApplicationTuning` | Much broader — supports any workload parameter |
| CloudPilot Console | OptiPilot Dashboard | Now embedded in cluster-agent |
| `cloudpilot-agent` DaemonSet | `cluster-agent` Deployment | No longer a DaemonSet |
| CloudPilot SaaS backend | Hub (optional) | Self-hosted, not SaaS |

---

## Pre-Migration Checklist

- [ ] Export existing `ScalingPolicy` objects: `kubectl get scalingpolicies -A -o yaml > scaling-policies-backup.yaml`
- [ ] Export existing `ServiceLevel` objects: `kubectl get servicelevels -A -o yaml > service-levels-backup.yaml`
- [ ] Note all Prometheus query strings in your `ServiceLevel` objects
- [ ] List all `TenantGroup` objects and their budget configurations
- [ ] Check CloudPilot dashboard for current drift annotations — these may contain useful baseline values

---

## Step 1: Install OptiPilot in Dry-Run Mode

Install OptiPilot alongside CloudPilot without conflicting:

```bash
helm install optipilot helm/optipilot \
  --namespace optipilot-system \
  --create-namespace \
  --set clusterAgent.dryRun=true
```

`dryRun=true` means OptiPilot observes and journals decisions but never applies actuations. This lets both systems run in parallel.

---

## Step 2: Migrate ServiceLevel → ServiceObjective

CloudPilot YAML:

```yaml
apiVersion: cloudpilot.io/v1
kind: ServiceLevel
metadata:
  name: checkout-slo
  namespace: checkout
spec:
  target: 0.999
  type: availability
  windowDays: 30
  query: "rate(http_requests_total{status!~'5..'}[5m]) / rate(http_requests_total[5m])"
```

Equivalent OptiPilot YAML:

```yaml
apiVersion: slo.optipilot.ai/v1alpha1
kind: ServiceObjective
metadata:
  name: checkout-slo
  namespace: checkout
spec:
  serviceName: checkout
  sloType: availability
  target: 0.999
  window: 30d                # Explicit duration string
  prometheusQuery: >
    rate(http_requests_total{status!~"5.."}[5m])
    /
    rate(http_requests_total[5m])
  errorBudgetBurnAlerts:    # New in OptiPilot
    - name: fast-burn
      threshold: 14.4
      window: 1h
      severity: page
```

Key differences:
- `windowDays: 30` → `window: 30d`
- `query` → `prometheusQuery`
- `errorBudgetBurnAlerts` is new and recommended

---

## Step 3: Migrate ScalingPolicy → OptimizationPolicy

CloudPilot YAML:

```yaml
apiVersion: cloudpilot.io/v1
kind: ScalingPolicy
metadata:
  name: checkout-policy
spec:
  targetSLO: checkout-slo
  rules:
    - if: errorBudgetBelow(10)
      action: noChange
    - if: burnRateAbove(3.0)
      action: scaleUp
    - if: burnRateBelow(0.5)
      action: scaleDown
```

Equivalent OptiPilot YAML:

```yaml
apiVersion: policy.optipilot.ai/v1alpha1
kind: OptimizationPolicy
metadata:
  name: checkout-policy
  namespace: checkout
spec:
  targetObjective: checkout-slo
  constraints:
    - expression: "slo.errorBudgetRemaining > 0.1"
      message: "Error budget below 10% — changes blocked"
    - expression: "slo.burnRate1h < 3.0"
      message: "Active degradation — scale-up only"
  objectives:
    - expression: "double(candidate.replicas) * costRate(candidate.instanceType, candidate.region, false)"
      weight: 1.0
      description: Minimize cost
  scaleDown:
    enabled: true
    minReplicas: 2
```

Key differences:
- No `if/action` DSL — replaced by CEL constraints (which actions are allowed) + objectives (what to optimize for)
- All constraints must pass for any action; objectives determine the best valid action
- See [CEL Reference](../cel-reference.md) for the full variable list

---

## Step 4: Migrate TenantGroup → TenantProfile

CloudPilot YAML:

```yaml
apiVersion: cloudpilot.io/v1
kind: TenantGroup
metadata:
  name: platform
spec:
  namespaces: [platform-prod, platform-staging]
  cpuQuota: 64
  memoryQuotaGiB: 256
  costBudgetUSD: 5000
  priority: high
```

Equivalent OptiPilot YAML:

```yaml
apiVersion: tenant.optipilot.ai/v1alpha1
kind: TenantProfile
metadata:
  name: platform-team
  namespace: optipilot-system
spec:
  tenantName: platform
  tier: premium              # high → premium
  namespaces:
    - platform-prod
    - platform-staging
  resourceBudget:
    maxCores: 64
    maxMemoryGiB: 256
    maxMonthlyCostUSD: 5000
  fairSharePolicy:           # New: fair-share weight
    weight: 3
    guaranteedCoresPercent: 20
    burstable: true
```

---

## Step 5: Remove CloudPilot Agent

Once OptiPilot is running in observation mode and journals look correct, disable CloudPilot:

```bash
# Scale down CloudPilot agent
kubectl scale deployment cloudpilot-agent -n cloudpilot-system --replicas=0
```

Leave it scaled to 0 for at least 2–3 days to monitor OptiPilot in shadow mode.

---

## Step 6: Enable OptiPilot Actuations

When you're satisfied with the decision journal, remove dry-run mode:

```bash
helm upgrade optipilot helm/optipilot \
  --namespace optipilot-system \
  --set clusterAgent.dryRun=false
```

---

## Step 7: Uninstall CloudPilot

```bash
helm uninstall cloudpilot -n cloudpilot-system

# Remove CloudPilot CRDs (after verifying nothing depends on them)
kubectl delete crd scalingpolicies.cloudpilot.io servicelevels.cloudpilot.io tenantgroups.cloudpilot.io
```

---

## Frequently Asked Questions

**Q: Can I run both systems simultaneously?**  
A: Yes. During the dry-run phase, OptiPilot observes without acting. CloudPilot continues managing the cluster normally.

**Q: Will OptiPilot lose my decision history when I uninstall CloudPilot?**  
A: CloudPilot's decision log is not imported. OptiPilot starts a fresh journal from installation day.

**Q: My CloudPilot rules used `if burnRateAbove(X) then scaleUp`. How do I express "scale up only" in OptiPilot?**  
A: OptiPilot doesn't have directional rules. Instead, express minimum replicas as a constraint that increases when burn-rate is high. See [Level 2 in the CEL guide](custom-policy.md#level-2-burn-rate-speed-limits).

**Q: Does OptiPilot support the CloudPilot Annotations API?**  
A: No. CloudPilot's `cloudpilot.io/drift-annotation` is not supported. Use `ServiceObjective` status fields instead.

---

## Rollback Plan

If you need to revert:

```bash
# Scale CloudPilot back up
kubectl scale deployment cloudpilot-agent -n cloudpilot-system --replicas=1

# Scale down OptiPilot (don't uninstall yet — keep CRDs and journal)
helm upgrade optipilot helm/optipilot \
  --namespace optipilot-system \
  --set clusterAgent.enabled=false
```
