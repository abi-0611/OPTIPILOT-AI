# Installation Guide

Production-grade installation of OptiPilot AI on Kubernetes.

## Prerequisites

| Requirement | Version | Notes |
|---|---|---|
| Kubernetes | v1.28+ | EKS, GKE, AKS, or on-prem |
| Helm | v3.14+ | |
| Prometheus | v2.40+ | kube-prometheus-stack recommended |
| cert-manager | v1.13+ | Required only for hub mTLS |
| Storage class | — | For SQLite PVC (disable with `persistence.enabled=false`) |

## Namespace Setup

```bash
kubectl create namespace optipilot-system
```

## Add the Helm Repository

```bash
helm repo add optipilot https://optipilot-ai.github.io/helm-charts
helm repo update
```

## Single-Cluster Install (recommended starting point)

```bash
helm install optipilot optipilot/optipilot \
  --namespace optipilot-system \
  --set global.prometheusURL=http://prometheus-operated.monitoring.svc.cluster.local:9090 \
  --set clusterAgent.args.leaderElect=true \
  --wait
```

## Production Values File

Create a `values-prod.yaml`:

```yaml
global:
  prometheusURL: "http://prometheus-operated.monitoring.svc.cluster.local:9090"
  journalBackend: postgres
  postgresDSN: "postgres://optipilot:secret@pg.db.svc.cluster.local:5432/optipilot?sslmode=require"

clusterAgent:
  replicaCount: 2
  args:
    leaderElect: true
    optimizerInterval: "30s"
  resources:
    requests:
      cpu: 500m
      memory: 512Mi
    limits:
      cpu: 2000m
      memory: 1Gi
  persistence:
    enabled: false   # using postgres, no SQLite needed

ingress:
  enabled: true
  className: nginx
  host: optipilot.internal.example.com
  tls:
    - secretName: optipilot-tls
      hosts:
        - optipilot.internal.example.com

serviceMonitor:
  enabled: true
```

```bash
helm install optipilot optipilot/optipilot \
  --namespace optipilot-system \
  -f values-prod.yaml
```

## Enable ML Service (optional)

```bash
helm upgrade optipilot optipilot/optipilot \
  --namespace optipilot-system \
  --reuse-values \
  --set mlService.enabled=true
```

## Enable Multi-Cluster Hub (optional)

See the [multi-cluster guide](./guides/multi-cluster.md). Requires cert-manager.

```bash
helm upgrade optipilot optipilot/optipilot \
  --namespace optipilot-system \
  --reuse-values \
  --set hub.enabled=true \
  --set hub.mtls.enabled=true
```

## Security Considerations

### RBAC
The cluster-agent ClusterRole requests permissions to:
- Read/write all 4 OptiPilot CRD groups
- Patch HPAs and Deployments (for actuation)
- Manage Karpenter NodePools (if using node actuator)
- Use leader election leases

Review and restrict as needed with a custom role binding.

### Network Policies
OptiPilot-system pods require egress to:
- Prometheus endpoint (configurable)
- Kubernetes API server
- Hub gRPC endpoint (if spoke agent enabled)

### Secret Management
- `global.postgresDSN` — use a Kubernetes `Secret` and reference via `valueFrom.secretKeyRef`
- Hub mTLS certificates — managed by cert-manager automatically

## Upgrading

```bash
helm repo update
helm upgrade optipilot optipilot/optipilot \
  --namespace optipilot-system \
  --reuse-values
```

**Note:** CRDs in `helm/optipilot/crds/` are installed by Helm automatically on first install. On upgrade, CRDs must be applied manually if schema changed:

```bash
kubectl apply -f helm/optipilot/crds/
```

## Uninstalling

```bash
helm uninstall optipilot --namespace optipilot-system

# Remove CRDs (CAUTION: deletes all OptiPilot custom resources)
kubectl delete crd \
  serviceobjectives.slo.optipilot.ai \
  optimizationpolicies.policy.optipilot.ai \
  tenantprofiles.tenant.optipilot.ai \
  applicationtunings.tuning.optipilot.ai
```
