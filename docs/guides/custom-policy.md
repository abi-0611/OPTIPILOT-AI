# Guide: Writing Custom CEL Policies

This tutorial walks through writing increasingly sophisticated `OptimizationPolicy` CEL expressions to achieve real-world optimization goals.

**Prerequisites:** OptiPilot installed, an existing `ServiceObjective`, familiarity with [CEL Reference](../cel-reference.md).

---

## Policy Anatomy Recap

```yaml
apiVersion: policy.optipilot.ai/v1alpha1
kind: OptimizationPolicy
metadata:
  name: my-policy
  namespace: production
spec:
  targetObjective: my-slo
  constraints:          # Must ALL pass for any action to proceed
    - expression: "..."
      message: "human-readable failure message"
  objectives:           # Minimize: lower score is better
    - expression: "..."
      weight: 1.0
      description: "what this optimizes"
```

---

## Level 1: Basic Safety Guard

Always start with a constraint that protects the error budget:

```yaml
constraints:
  - expression: "slo.errorBudgetRemaining > 0.05"
    message: "Error budget critically low — optimizations frozen"
```

**What it does:** When the remaining 30-day error budget drops below 5%, all OptiPilot actions are blocked until it recovers.

---

## Level 2: Burn-Rate Speed Limits

Block actions when the service is actively degrading:

```yaml
constraints:
  - expression: "slo.errorBudgetRemaining > 0.1"
    message: "Insufficient error budget"
  - expression: "slo.burnRate1h < 5.0"
    message: "Hourly burn rate too high — service is degrading"
  - expression: "slo.burnRate6h < 2.0"
    message: "6-hour burn rate elevated — no scale-down allowed"
```

**What it does:** The 1-hour check catches sudden spikes. The 6-hour check catches slow rolling degradations.

---

## Level 3: Cost-Aware Scale-Down

Minimize cost while keeping sufficient replicas:

```yaml
constraints:
  - expression: "slo.errorBudgetRemaining > 0.15"
    message: "Budget below threshold"
  - expression: "candidate.replicas >= 2"
    message: "Minimum 2 replicas for availability"

objectives:
  - expression: "double(candidate.replicas) * costRate(candidate.instanceType, candidate.region, false)"
    weight: 1.0
    description: Hourly compute cost
```

**What it does:** Of all configurations that satisfy the constraints, the optimizer picks the one with the lowest hourly cost.

---

## Level 4: Spot Instance Safety

Allow Spot/Preemptible only when interruption risk is acceptable:

```yaml
constraints:
  - expression: >-
      !candidate.spotEnabled
      || spotRisk(candidate.instanceType, candidate.region) < 0.15
    message: "Spot interruption risk too high (>15%)"
  - expression: >-
      !candidate.spotEnabled
      || slo.errorBudgetRemaining > 0.5
    message: "Not enough error budget to risk Spot"

objectives:
  - expression: >-
      costRate(candidate.instanceType, candidate.region, candidate.spotEnabled)
      * double(candidate.replicas)
    weight: 1.0
    description: Minimize cost including Spot discount
```

**What it does:** Spot is only enabled when: (1) risk of interruption is under 15%, AND (2) you have more than 50% of your error budget remaining as a cushion.

---

## Level 5: Carbon-Aware Scheduling

Prefer low-carbon regions or time windows:

```yaml
constraints:
  - expression: "slo.errorBudgetRemaining > 0.2"
    message: "Low budget"
  - expression: "candidate.replicas >= 2"
    message: "Minimum replicas"

objectives:
  - expression: >-
      costRate(candidate.instanceType, candidate.region, candidate.spotEnabled)
      * double(candidate.replicas)
    weight: 0.6
    description: Minimize cost
  - expression: >-
      carbonIntensity(candidate.region)
      * candidate.cpuCores
    weight: 0.4
    description: Minimize carbon footprint (gCO2eq/h)
```

**What it does:** Scores configurations on a weighted combination of cost and carbon intensity. A config in `us-east-1` might score lower (better) than `us-west-2` when both are equally good on cost but differ in grid carbon.

---

## Level 6: Tenant-Aware Constraints

Different rules for different tenant tiers:

```yaml
constraints:
  - expression: >-
      tenant.tier == "premium"
      || slo.errorBudgetRemaining > 0.3
    message: "Standard/economy tenants require 30% budget before optimization"

  - expression: >-
      tenant.currentCostUSD
      + costRate(candidate.instanceType, candidate.region, false) * 720.0
      <= tenant.maxCostUSD
    message: "Would exceed tenant monthly cost budget"

  - expression: >-
      candidate.cpuCores <= tenant.maxCores
    message: "Would exceed tenant CPU quota"
```

**What it does:** Premium tenants can optimize even when budget is lower. All tenants are guarded by their resource and cost budgets.

---

## Level 7: Custom Prometheus Metrics

Use application-specific metrics in constraints:

```yaml
constraints:
  - expression: >-
      metrics["cache_hit_rate"] > 0.7
    message: "Cache hit rate below 70% — do not scale down"

  - expression: >-
      !has(metrics, "db_connection_pool_exhausted_total")
      || metrics["db_connection_pool_exhausted_total"] < 5.0
    message: "DB connection pool exhausted — resolve before optimizing"
```

**What it does:** Blocks scale-down when the cache layer is cold or the database connection pool is saturated.

---

## Testing Your Policy

### Dry Run Mode

Set `spec.dryRun: true` and observe decisions:

```bash
kubectl patch op my-policy -n production \
  --type=merge -p '{"spec":{"dryRun":true}}'
```

### What-If Simulation

Test a specific configuration without touching the cluster:

```bash
curl -X POST http://localhost:8090/api/v1/whatif/simulate \
  -d '{
    "policyName": "my-policy",
    "objectiveName": "my-slo",
    "namespace": "production",
    "scenario": {"replicas": 3, "spotEnabled": true, "instanceType": "m5.large"}
  }' | jq '.constraintResults'
```

### CEL Trace

Every decision stored in the journal includes a `celTrace` field showing each expression result and the variable values at evaluation time. Retrieve it with:

```bash
DECISION_ID=$(curl -s 'http://localhost:8090/api/v1/decisions?objectiveName=my-slo&limit=1' | jq -r '.decisions[0].id')
curl -s "http://localhost:8090/api/v1/decisions/$DECISION_ID" | jq '.celTrace'
```

---

## Common Policy Patterns Summary

| Goal | Key Expression |
|---|---|
| Protect error budget | `slo.errorBudgetRemaining > 0.1` |
| Guard burn rate | `slo.burnRate1h < 2.0` |
| Enforce minimum replicas | `candidate.replicas >= 2` |
| Minimize cost | objective: `costRate(...) * double(candidate.replicas)` |
| Spot safety | `!candidate.spotEnabled \|\| spotRisk(...) < 0.15` |
| Carbon awareness | objective: `carbonIntensity(candidate.region) * candidate.cpuCores` |
| Respect quota | `candidate.cpuCores <= tenant.maxCores` |
| Custom metric guard | `metrics["queue_depth"] < 1000` |
