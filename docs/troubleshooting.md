# Troubleshooting

This document covers the most common issues encountered when deploying and operating OptiPilot AI.

---

## Diagnostics Quick Reference

```bash
# Check component health
kubectl get pods -n optipilot-system
kubectl describe pod -n optipilot-system <pod-name>

# Recent events
kubectl get events -n optipilot-system --sort-by=.lastTimestamp | tail -20

# Manager logs (last 100 lines)
kubectl logs -n optipilot-system \
  -l app.kubernetes.io/name=optipilot-cluster-agent --tail=100

# Hub logs
kubectl logs -n optipilot-system \
  -l app.kubernetes.io/name=optipilot-hub --tail=100

# CRD status
kubectl get serviceobjectives,optimizationpolicies,tenantprofiles,applicationtunings -A
```

---

## Installation Issues

### CRD Not Found After `helm install`

**Symptom:** `error: resource mapping not found for "ServiceObjective"`

**Cause:** CRDs not yet applied.

**Fix:**
```bash
kubectl apply -f helm/optipilot/crds/
# Then retry: helm install optipilot helm/optipilot ...
```

**Alternative:** If using ArgoCD or Flux, use the `crds/` hook-based install or enable `installCRDs: true` in your chart values.

---

### ImagePullBackOff

**Symptom:** Pod stuck in `ImagePullBackOff`.

**Fix:**
```bash
# Check the actual image URL being used
kubectl describe pod -n optipilot-system <pod> | grep Image:

# If using private registry, create pull secret:
kubectl create secret docker-registry ghcr-secret \
  -n optipilot-system \
  --docker-server=ghcr.io \
  --docker-username=<user> \
  --docker-password=<token>

# Add to values:
helm upgrade optipilot helm/optipilot \
  --set global.image.pullSecrets[0]=ghcr-secret
```

---

### Leader Election Timeout

**Symptom:** Manager logs: `failed to acquire leader lease ... context deadline exceeded`

**Cause:** Another replica holds the lease and is unresponsive.

**Fix:**
```bash
kubectl delete lease -n optipilot-system optipilot-manager-leader
# Manager will acquire the lease on the next attempt
```

---

## Prometheus Integration

### `no datapoints returned` Warning

**Symptom:** `ServiceObjective` status shows `sloCompliant: unknown` and events contain `no datapoints`.

**Cause:** Prometheus query returns no data. Usually a label mismatch or missing scrape target.

**Fix:**
```bash
# Test your query directly in Prometheus:
kubectl port-forward -n monitoring svc/prometheus-operated 9090:9090
# Open http://localhost:9090 and paste your prometheusQuery

# Check if the metric exists at all:
curl http://localhost:9090/api/v1/query?query=http_requests_total | jq '.data.result | length'
```

---

### Prometheus Unreachable

**Symptom:** Manager logs: `error querying prometheus: connection refused` or `dial tcp: i/o timeout`

**Fix:**

1. Verify the configured URL:
```bash
kubectl get configmap -n optipilot-system optipilot-cluster-agent -o yaml | grep prometheus
```

2. Test connectivity from within the pod:
```bash
kubectl exec -n optipilot-system deploy/optipilot-cluster-agent -- \
  wget -qO- http://prometheus-operated.monitoring.svc:9090/-/healthy
```

3. Check NetworkPolicy if present in your cluster.

---

## SLO Issues

### ServiceObjective Stuck in `Initializing`

**Symptom:** `.status.phase: Initializing` persists for > 5 minutes.

**Cause:** Either missing RBAC permissions or Prometheus connectivity issue.

**Fix:**
```bash
kubectl describe serviceobjective -n <ns> <name>
# Look for Events: section for specific error messages
```

---

### Error Budget Shows 0% Immediately After Deploy

**Symptom:** `errorBudgetRemaining: 0.0` right after creating `ServiceObjective`.

**Cause:** Prometheus has insufficient historical data (< 5 minutes of data) or the query returns error rate = 1.0.

**Fix:** Wait 5–10 minutes for Prometheus to accumulate data. If it persists, check your query manually.

---

### SLO Burn Rate Alert Firing Constantly

**Symptom:** Continuous `BurnRateAlert` events even when the service appears healthy.

**Cause:** Prometheus query returns a value > 1.0 due to mismatched series.

**Fix:**
```bash
# Verify the ratio is bounded 0-1:
curl -s "http://localhost:9090/api/v1/query?query=<your-query>" | jq '.data.result[0].value[1]'
# Should be between 0 and 1
```

---

## Policy / CEL Issues

### CEL Expression Evaluation Error

**Symptom:** Events: `CEL evaluation failed: no such attribute 'candidate.replicas'`

**Cause:** Typo in variable name or accessing a field that doesn't exist in the current context.

**Fix:** Use the what-if API's `celTrace` to debug:
```bash
curl -X POST http://localhost:8090/api/v1/whatif/simulate \
  -d '{"policyName":"my-policy","objectiveName":"my-slo","namespace":"ns","scenario":{}}' | \
  jq '.constraintResults'
```

See [CEL Reference](cel-reference.md) for all valid variable names.

---

### All Constraints Failing (Optimizer Frozen)

**Symptom:** No new decisions appearing in journal; `outcome: blocked` on all recent entries.

**Cause:** One or more constraints are too strict for current cluster state.

**Fix:**
1. Check which constraint is failing in recent decision: `curl http://localhost:8090/api/v1/decisions/<id> | jq '.celTrace'`
2. Add `spec.dryRun: true` temporarily and lower constraint thresholds
3. Consider parameterizing with `tenant.tier` so only economy tenants have strict limits

---

## Actuator Issues

### HPA Not Being Updated

**Symptom:** OptiPilot journal shows `action.type: scale` but HPA unchanged.

**Cause:** Missing `patch hpa` RBAC or HPA is managed by another controller (e.g., KEDA).

**Fix:**
```bash
# Verify RBAC
kubectl auth can-i update horizontalpodautoscalers -n production \
  --as=system:serviceaccount:optipilot-system:optipilot-cluster-agent
# Should print: yes
```

---

### Karpenter NodePool Not Adjustable

**Symptom:** `actuator: karpenter` decisions not applied.

**Cause:** Karpenter NodePool not found or wrong version (OptiPilot supports `karpenter.sh/v1beta1`).

**Fix:**
```bash
kubectl get nodepools.karpenter.sh  # Verify CRD exists and version
kubectl auth can-i patch nodepools.karpenter.sh \
  --as=system:serviceaccount:optipilot-system:optipilot-cluster-agent
```

---

## Multi-Cluster / Hub Issues

### Spoke Cannot Connect to Hub

**Symptom:** Spoke agent logs: `failed to connect to hub: connection refused`

**Fix:**
1. Verify hub LoadBalancer has an external IP: `kubectl get svc -n optipilot-system optipilot-hub-grpc`
2. Test port 50051 is open from spoke node
3. Check mTLS certificates: `kubectl get certificate -n optipilot-system`

---

### `certificate verify failed` on mTLS Connection

**Symptom:** gRPC error: `transport: authentication handshake failed: x509: certificate signed by unknown authority`

**Fix:**
- Ensure the spoke has the hub CA certificate installed as a secret
- Verify cert-manager issued certificates in both hub and spoke namespaces
- Check certificate validity: `kubectl get certificate -n optipilot-system -o wide`

---

## Dashboard / API

### Dashboard Returns 403

**Fix:** Check that the ServiceAccount token is valid and has the `optipilot:viewer` ClusterRole:
```bash
kubectl get clusterrolebinding | grep optipilot
```

### What-If API Returns `503 ML service unavailable`

**Fix:** The ML service is disabled by default. Enable it:
```bash
helm upgrade optipilot helm/optipilot --set mlService.enabled=true
```

Or use the fallback rule-based projections (less accurate but no ML required) by setting `mlService.fallbackEnabled=true`.

---

## Known Limitations

| Limitation | Workaround |
|---|---|
| SQLite journal limited to ~2GB | Enable `persistence.size: 20Gi` or use PostgreSQL (future feature) |
| CEL cannot call external HTTP APIs | Use Prometheus recording rules to cache external data as metrics |
| Karpenter < v0.30 not supported | Upgrade Karpenter or use HPA-only mode |
| Multi-cluster requires LoadBalancer service for hub | Use NodePort + external load balancer or VPN tunnel |

---

## Getting Help

- Open an issue: `https://github.com/optipilot-ai/optipilot/issues`
- Include output of: `kubectl get all -n optipilot-system`, logs, and the specific error message
- Include OptiPilot version: `kubectl get deployment -n optipilot-system optipilot-cluster-agent -o jsonpath='{.spec.template.spec.containers[0].image}'`
