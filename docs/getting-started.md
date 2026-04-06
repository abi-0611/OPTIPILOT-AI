# Getting Started with OptiPilot AI

Get OptiPilot AI running on your local machine in under 5 minutes using [kind](https://kind.sigs.k8s.io/).

## Prerequisites

- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) v0.20+
- [kubectl](https://kubernetes.io/docs/tasks/tools/) v1.28+
- [Helm](https://helm.sh/docs/intro/install/) v3.14+
- Docker (for building images locally) or internet access to `ghcr.io`

## Step 1: Create a kind Cluster

```bash
kind create cluster --name optipilot-dev
kubectl cluster-info --context kind-optipilot-dev
```

## Step 2: Install Prometheus (required for SLO evaluation)

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
helm install prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring --create-namespace \
  --set grafana.enabled=false \
  --wait --timeout 5m
```

## Step 3: Install OptiPilot AI

```bash
helm install optipilot ./helm/optipilot \
  --namespace optipilot-system --create-namespace \
  --set global.prometheusURL=http://prometheus-operated.monitoring.svc.cluster.local:9090 \
  --wait --timeout 3m
```

Verify the controller is running:

```bash
kubectl get pods -n optipilot-system
# NAME                                          READY   STATUS    RESTARTS   AGE
# optipilot-cluster-agent-6d8b9f4c7-xk9q2      1/1     Running   0          45s
```

## Step 4: Create Your First SLO

```yaml
# my-slo.yaml
apiVersion: slo.optipilot.ai/v1alpha1
kind: ServiceObjective
metadata:
  name: api-server-slo
  namespace: default
spec:
  targetRef:
    name: api-server
    kind: Deployment
  objectives:
    - metricName: error_rate
      target: "0.1%"
      window: 1h
    - metricName: latency_p99
      target: "200ms"
      window: 5m
  costConstraint:
    maxHourlyCostUSD: "5.00"
```

```bash
kubectl apply -f my-slo.yaml
kubectl get serviceobjectives
```

## Step 5: Watch Optimization Decisions

```bash
# Stream the controller logs
kubectl logs -n optipilot-system -l app.kubernetes.io/component=cluster-agent -f

# Access the decision journal API
kubectl port-forward -n optipilot-system svc/optipilot-cluster-agent 8090:8090 &
curl http://localhost:8090/api/v1/decisions | jq .
```

## Step 6: Open the Dashboard

With the port-forward from Step 5 still running:

```
open http://localhost:8090
```

## Using OptiPilot With Your Own Project

OptiPilot does not control code in a folder directly. It watches Kubernetes
workloads, reads Prometheus metrics, and then recommends or applies changes to
the workload objects running in the cluster.

For your CodePro project in `E:\codepro`, the easiest first target is the
backend API service because it already exposes `/health` and `/metrics`.

What you need first:

- A Kubernetes deployment for CodePro running in the same cluster as OptiPilot
- Prometheus scraping the backend metrics endpoint
- A `ServiceObjective` that points at the CodePro deployment you want to tune
- An `OptimizationPolicy` that selects that workload by label

Use the backend service as the first example. The project already has these
runtime settings in `admin-backend/.env.example`:

- `DATABASE_URL`
- `SECRET_KEY`
- `ADMIN_USERNAME`
- `ADMIN_PASSWORD`
- `REDIS_URL`

Once the CodePro backend is deployed to Kubernetes, apply an SLO like this:

```yaml
apiVersion: slo.optipilot.ai/v1alpha1
kind: ServiceObjective
metadata:
  name: codepro-api-slo
  namespace: codepro
  labels:
    app: codepro-api
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: api
  objectives:
    - metric: availability
      target: "99.9%"
      window: "5m"
    - metric: latency_p99
      target: "500ms"
      window: "5m"
  evaluationInterval: "30s"
  errorBudget:
    total: "0.1%"
```

Then apply a policy that OptiPilot can use for decisions:

The `OptimizationPolicy.selector` matches labels on the ServiceObjective, so
the SLO above includes `app: codepro-api`.

```yaml
apiVersion: policy.optipilot.ai/v1alpha1
kind: OptimizationPolicy
metadata:
  name: codepro-api-policy
  namespace: codepro
spec:
  selector:
    matchLabels:
      app: codepro-api
  objectives:
    - name: slo_compliance
      direction: maximize
      weight: 0.7
    - name: cost
      direction: minimize
      weight: 0.3
  dryRun: true
```

If you have not deployed CodePro to Kubernetes yet, that is the next step.
I can help scaffold the Kubernetes manifests for `E:\codepro` next if you want
the exact deployment files.

## Cleanup

```bash
kind delete cluster --name optipilot-dev
```

## Next Steps

- [Installation guide](./installation.md) — production deployment with HA and security
- [Architecture overview](./architecture.md) — understand how the system works
- [Your first SLO tutorial](./guides/first-slo.md) — deeper SLO configuration
- [Configuration reference](./configuration.md) — all Helm values documented
