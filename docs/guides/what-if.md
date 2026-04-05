# Guide: Using the What-If Simulator

The what-if simulator lets you explore "what would happen if I changed X?" without applying changes to your cluster. It evaluates your CEL policies, projects SLO impact, and estimates cost and carbon changes.

**Prerequisites:** OptiPilot installed, dashboard port-forwarded.

```bash
kubectl port-forward -n optipilot-system svc/optipilot-cluster-agent 8090:8090
```

---

## Use Case 1: Should I Enable Spot Instances?

You want to know if switching `payments-api` to Spot instances would break your SLO.

```bash
curl -X POST http://localhost:8090/api/v1/whatif/simulate \
  -H 'Content-Type: application/json' \
  -d '{
    "policyName": "payments-policy",
    "objectiveName": "payments-slo",
    "namespace": "production",
    "scenario": {
      "replicas": 4,
      "instanceType": "m5.large",
      "spotEnabled": true
    }
  }' | jq '{
    feasible,
    projectedAvailability: .projectedErrorRate,
    costDeltaUSD,
    spotRisk: (.constraintResults[] | select(.expression | test("spotRisk")) | .passed),
    warnings
  }'
```

**Example Output:**

```json
{
  "feasible": false,
  "projectedAvailability": 0.0028,
  "costDeltaUSD": -80.0,
  "spotRisk": false,
  "warnings": [
    "Spot interruption risk is 0.31 — exceeds policy constraint threshold 0.15",
    "Enabling Spot with current burn rate 1.8 would risk SLO breach"
  ]
}
```

The simulator blocked the configuration because `spotRisk > 0.15`. You save $80/month but would likely breach your SLO.

---

## Use Case 2: Find the Optimal Replica Count

Sweep replica counts to find the cheapest configuration that stays compliant:

```bash
curl -s 'http://localhost:8090/api/v1/whatif/slo-cost-curve?objectiveName=payments-slo&namespace=production&minReplicas=1&maxReplicas=12' | \
  jq '.points[] | select(.feasible == true) | {replicas, monthlyCostUSD, estimatedAvailability}'
```

**Example Output:**

```json
{"replicas": 3, "monthlyCostUSD": 210, "estimatedAvailability": 0.9986}
{"replicas": 4, "monthlyCostUSD": 280, "estimatedAvailability": 0.9994}
{"replicas": 5, "monthlyCostUSD": 350, "estimatedAvailability": 0.9999}
```

The `optimalPoint` in the response identifies replica count `3` as the cheapest feasible option (availability 99.86%, within the 99.5% SLO).

---

## Use Case 3: Carbon-Aware Region Selection

Evaluate the same workload across multiple regions:

```bash
for REGION in us-east-1 us-west-2 eu-west-1 eu-central-1; do
  echo "=== $REGION ==="
  curl -s -X POST http://localhost:8090/api/v1/whatif/simulate \
    -H 'Content-Type: application/json' \
    -d "{
      \"policyName\": \"payments-policy\",
      \"objectiveName\": \"payments-slo\",
      \"namespace\": \"production\",
      \"scenario\": {\"replicas\": 3, \"region\": \"$REGION\"}
    }" | jq '{feasible, costDeltaUSD, carbonDeltaKgCO2}'
done
```

This helps you identify that `eu-west-1` might produce 30% less carbon than `us-east-1` for the same cost.

---

## Use Case 4: Impact of Tuning Parameters

Simulate a `ApplicationTuning` parameter change:

```bash
curl -X POST http://localhost:8090/api/v1/whatif/simulate \
  -H 'Content-Type: application/json' \
  -d '{
    "tuningName": "payments-tuning",
    "namespace": "production",
    "scenario": {
      "replicas": 4,
      "cpu_request_millicores": 250,
      "memory_limit_mib": 256
    }
  }' | jq '{feasible, projectedLatencyP99Ms, costDeltaUSD, warnings}'
```

---

## Use Case 5: Pre-Validate a Policy Change

Before applying a stricter `OptimizationPolicy`, validate it won't lock out all configurations:

```bash
# Check if any configuration in the standard sweep passes the new policy
curl -s 'http://localhost:8090/api/v1/whatif/slo-cost-curve?objectiveName=payments-slo&namespace=production' | \
  jq '.points | map(select(.feasible)) | length'
```

If this returns `0`, your new policy is too restrictive and would freeze all optimizations.

---

## Using the Dashboard

The dashboard at `http://localhost:8090` provides a graphical what-if interface:

1. Open **"What-If Simulator"** from the left nav
2. Select your `ServiceObjective`
3. Adjust sliders for replicas, CPU, memory, and Spot toggle
4. See projected SLO compliance, cost, and carbon in real-time
5. Click **"Compare Scenarios"** to view multiple configurations side-by-side

The dashboard uses the same REST endpoints documented in the [API Reference](../api-reference.md).

---

## Interpreting Results

| Field | Meaning |
|---|---|
| `feasible: true` | All CEL constraints pass for this scenario |
| `feasible: false` | One or more constraints failed (see `constraintResults`) |
| `costDeltaUSD` | Negative = cost savings; positive = cost increase |
| `carbonDeltaKgCO2` | Negative = lower emissions; positive = higher emissions |
| `projectedLatencyP99Ms` | Estimated p99 latency under this configuration (from ML model) |
| `objectiveScore` | Weighted multi-objective score; lower = better |
| `warnings` | Non-blocking notices (Spot risk near threshold, budget below 30%, etc.) |

---

## Limitations

- Latency and availability projections use the ML forecasting model. Accuracy increases with model training history.
- Projections are best-effort estimates under steady-state load. Traffic spikes or external dependencies are not modeled.
- The `region` field in simulations reflects cost and carbon data, not actual cluster scheduling.

---

## Next Steps

- [API Reference](../api-reference.md) — full what-if API schemas
- [CEL Reference](../cel-reference.md) — write custom constraint expressions
- [Multi-Cluster](multi-cluster.md) — cross-cluster what-if simulations
