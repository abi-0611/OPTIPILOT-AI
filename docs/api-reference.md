# API Reference

OptiPilot exposes a REST API on the cluster-agent pod at port `8090`. This API is also served behind the hub's gRPC gateway when multi-cluster mode is enabled.

## Base URL

In-cluster: `http://optipilot-cluster-agent.optipilot-system.svc.cluster.local:8090`

Port-forward for local development:
```bash
kubectl port-forward -n optipilot-system svc/optipilot-cluster-agent 8090:8090
```

## Authentication

All endpoints require a valid `ServiceAccount` token in the `Authorization: Bearer <token>` header when accessed from outside the cluster. Internal in-cluster calls from the dashboard pod skip authentication.

---

## Decisions

### `GET /api/v1/decisions`

Returns a paginated list of optimization decisions from the decision journal.

**Query Parameters**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `limit` | int | `20` | Number of results per page |
| `offset` | int | `0` | Pagination offset |
| `namespace` | string | all | Filter by namespace |
| `objectiveName` | string | all | Filter by `ServiceObjective` name |
| `since` | RFC3339 | 1 hour ago | Earliest decision timestamp |

**Response**

```json
{
  "total": 142,
  "offset": 0,
  "limit": 20,
  "decisions": [
    {
      "id": "dec-a1b2c3d4",
      "timestamp": "2025-01-15T10:30:00Z",
      "objectiveName": "api-slo",
      "namespace": "production",
      "action": {
        "type": "scale",
        "replicas": 7
      },
      "reason": "p99 latency 340ms approaching SLO target 400ms; error budget 62%",
      "outcome": "applied",
      "costDeltaUSD": 1.20
    }
  ]
}
```

---

### `GET /api/v1/decisions/{id}`

Returns a single decision by ID, including full CEL trace and Prometheus snapshot.

**Path Parameters**

| Parameter | Description |
|---|---|
| `id` | Decision ID (e.g., `dec-a1b2c3d4`) |

**Response**

```json
{
  "id": "dec-a1b2c3d4",
  "timestamp": "2025-01-15T10:30:00Z",
  "objectiveName": "api-slo",
  "namespace": "production",
  "action": { "type": "scale", "replicas": 7 },
  "reason": "p99 latency 340ms approaching SLO target 400ms",
  "outcome": "applied",
  "costDeltaUSD": 1.20,
  "celTrace": [
    { "expression": "slo.errorBudgetRemaining > 0.1", "result": true, "values": { "slo.errorBudgetRemaining": 0.62 } },
    { "expression": "slo.burnRate1h < 2.0", "result": true, "values": { "slo.burnRate1h": 0.8 } }
  ],
  "metricsSnapshot": {
    "http_requests_total": 12500,
    "http_error_rate": 0.0012,
    "latency_p99_ms": 340
  }
}
```

---

## What-If Simulation

### `POST /api/v1/whatif/simulate`

Simulates a proposed configuration change and returns projected SLO impact, cost delta, and CEL constraint evaluation.

**Request Body**

```json
{
  "policyName": "prod-policy",
  "objectiveName": "api-slo",
  "namespace": "production",
  "scenario": {
    "replicas": 5,
    "instanceType": "m5.xlarge",
    "spotEnabled": true,
    "cpuRequestMillicores": 500
  }
}
```

**Response**

```json
{
  "feasible": true,
  "projectedLatencyP99Ms": 285,
  "projectedErrorRate": 0.0008,
  "projectedMonthlyCostUSD": 320.0,
  "costDeltaUSD": -40.0,
  "carbonDeltaKgCO2": -12.3,
  "constraintResults": [
    { "expression": "slo.errorBudgetRemaining > 0.1", "passed": true },
    { "expression": "candidate.spotEnabled && spotRisk(...) < 0.2", "passed": false, "reason": "spotRisk=0.31 exceeds threshold" }
  ],
  "objectiveScore": 0.73,
  "warnings": ["Spot risk above threshold — constraint would block this configuration"]
}
```

---

### `GET /api/v1/whatif/slo-cost-curve`

Returns a cost-vs-SLO compliance curve by sweeping `replicas` and `instanceType` combinations.

**Query Parameters**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `objectiveName` | string | ✅ | Target `ServiceObjective` |
| `namespace` | string | ✅ | Namespace |
| `minReplicas` | int | | Sweep min (default: 1) |
| `maxReplicas` | int | | Sweep max (default: 20) |

**Response**

```json
{
  "objectiveName": "api-slo",
  "points": [
    { "replicas": 2, "monthlyCostUSD": 120, "estimatedAvailability": 0.9901, "feasible": false },
    { "replicas": 3, "monthlyCostUSD": 180, "estimatedAvailability": 0.9987, "feasible": true },
    { "replicas": 5, "monthlyCostUSD": 300, "estimatedAvailability": 0.9999, "feasible": true }
  ],
  "optimalPoint": { "replicas": 3, "monthlyCostUSD": 180, "estimatedAvailability": 0.9987 }
}
```

---

## Tenant API

### `GET /api/v1/tenants`

Lists all tenants with current allocation status.

**Response**

```json
{
  "tenants": [
    {
      "name": "platform-team",
      "tier": "premium",
      "currentCores": 24.5,
      "maxCores": 64,
      "currentCostUSD": 1240.0,
      "maxCostUSD": 5000.0,
      "allocationStatus": "guaranteed",
      "fairnessScore": 0.92
    }
  ]
}
```

---

### `GET /api/v1/tenants/{name}/quota`

Returns the current quota status for a specific tenant.

---

## Health Endpoints

### `GET /healthz`

Liveness probe. Returns `200 OK` when the manager process is alive.

### `GET /readyz`

Readiness probe. Returns `200 OK` when the manager has completed its initial cache sync and is ready to serve.

### `GET /metrics`

Prometheus metrics endpoint. Returns all `optipilot_*` metrics.

---

## Error Format

All non-2xx responses return:

```json
{
  "error": "not_found",
  "message": "decision dec-xxxx not found",
  "code": 404
}
```
