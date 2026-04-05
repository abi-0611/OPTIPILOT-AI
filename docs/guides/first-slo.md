# Guide: Defining Your First SLO

This tutorial walks through creating an end-to-end SLO for a sample HTTP service, observing OptiPilot's optimization decisions, and reading the decision journal.

**Time required:** ~20 minutes  
**Prerequisites:** OptiPilot installed (see [Getting Started](../getting-started.md)), Prometheus collecting `http_requests_total` metrics from your workloads.

---

## Step 1: Deploy the Sample Application

We'll use a lightweight echo server to simulate a real service.

```bash
kubectl create namespace demo

kubectl apply -n demo -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo-api
spec:
  replicas: 2
  selector:
    matchLabels:
      app: echo-api
  template:
    metadata:
      labels:
        app: echo-api
    spec:
      containers:
        - name: echo
          image: hashicorp/http-echo:1.0
          args:
            - "-text=hello"
            - "-listen=:8080"
          ports:
            - containerPort: 8080
          resources:
            requests:
              cpu: 100m
              memory: 64Mi
---
apiVersion: v1
kind: Service
metadata:
  name: echo-api
spec:
  selector:
    app: echo-api
  ports:
    - port: 8080
      targetPort: 8080
EOF
```

---

## Step 2: Configure Prometheus Scraping

Ensure Prometheus scrapes the sample app. With kube-prometheus-stack, a `ServiceMonitor` works:

```bash
kubectl apply -n demo -f - <<EOF
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: echo-api
  labels:
    release: prometheus
spec:
  selector:
    matchLabels:
      app: echo-api
  endpoints:
    - port: "8080"
      path: /metrics
      interval: 15s
EOF
```

---

## Step 3: Define the ServiceObjective

Create a `ServiceObjective` with a 99.5% availability target:

```bash
kubectl apply -f - <<EOF
apiVersion: slo.optipilot.ai/v1alpha1
kind: ServiceObjective
metadata:
  name: echo-api-slo
  namespace: demo
spec:
  serviceName: echo-api
  sloType: availability
  target: 0.995
  window: 30d
  errorBudgetBurnAlerts:
    - name: fast-burn
      threshold: 14.4
      window: 1h
      severity: page
    - name: slow-burn
      threshold: 6.0
      window: 6h
      severity: ticket
  prometheusQuery: |
    rate(http_requests_total{
      service="echo-api",
      code!~"5.."
    }[5m])
    /
    rate(http_requests_total{service="echo-api"}[5m])
EOF
```

Within 30 seconds, verify OptiPilot reconciles it:

```bash
kubectl get serviceobjective -n demo echo-api-slo -o yaml
```

You should see `.status.sloCompliant: true` and a freshly set `.status.errorBudgetRemaining`.

---

## Step 4: Create an Optimization Policy

Define an `OptimizationPolicy` that minimizes cost while maintaining SLO compliance:

```bash
kubectl apply -f - <<EOF
apiVersion: policy.optipilot.ai/v1alpha1
kind: OptimizationPolicy
metadata:
  name: echo-api-policy
  namespace: demo
spec:
  targetObjective: echo-api-slo
  constraints:
    - expression: "slo.errorBudgetRemaining > 0.2"
      message: "Error budget must stay above 20%"
    - expression: "candidate.replicas >= 1"
      message: "Must keep at least one replica"
    - expression: "slo.burnRate1h < 2.0"
      message: "Burn rate too high for optimization"
  objectives:
    - expression: "double(candidate.replicas) * 0.05"
      weight: 1.0
      description: Minimize replica cost
  scaleDown:
    enabled: true
    minReplicas: 1
    maxReplicas: 10
  dryRun: false
EOF
```

---

## Step 5: Observe Optimization Decisions

Generate some load so OptiPilot has metrics to evaluate:

```bash
kubectl run load-gen -n demo --image=busybox --rm -it --restart=Never -- \
  sh -c 'while true; do wget -qO- http://echo-api:8080; done'
```

Watch decisions being made:

```bash
kubectl port-forward -n optipilot-system svc/optipilot-cluster-agent 8090:8090 &

# List decisions for our SLO
curl -s 'http://localhost:8090/api/v1/decisions?namespace=demo&objectiveName=echo-api-slo' | jq .
```

You should see entries with `action.type: scale` and the reasoning explanation.

---

## Step 6: Read the Decision Journal

```bash
curl -s 'http://localhost:8090/api/v1/decisions?namespace=demo&limit=5' | \
  jq '.decisions[] | {id, timestamp, action, reason, outcome}'
```

Each entry shows:
- What action was taken (e.g., `scale replicas: 2→3`)
- The reason (latency, burn rate, or cost driver)
- The outcome (`applied`, `deferred`, or `blocked`)

---

## Step 7: View Burn Alerts

If you deliberately cause errors:

```bash
kubectl run error-gen -n demo --image=busybox --rm -it --restart=Never -- \
  sh -c 'while true; do wget -qO- http://echo-api:8080/this-404s; done'
```

After ~2 minutes, check for burn-rate alerts:

```bash
kubectl get events -n demo --field-selector reason=BurnRateAlert
```

---

## Step 8: Enable Dry Run Mode

To preview decisions without applying them, set `dryRun: true`:

```bash
kubectl patch optimizationpolicy -n demo echo-api-policy \
  --type=merge -p '{"spec":{"dryRun":true}}'
```

Decisions will still appear in the journal with `outcome: dry-run`.

---

## Clean Up

```bash
kubectl delete namespace demo
kubectl delete serviceobjective echo-api-slo -n demo  # if not deleted by namespace
```

---

## Next Steps

- [Write a custom CEL policy](custom-policy.md)
- [Set up multi-cluster](multi-cluster.md)
- [Use the what-if simulator](what-if.md)
