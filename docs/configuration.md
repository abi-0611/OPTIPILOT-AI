# Configuration Reference

All configuration is provided via Helm `values.yaml`. This document covers every configurable field.

## Global Settings

| Key | Type | Default | Description |
|---|---|---|---|
| `global.namespace` | string | `optipilot-system` | Namespace where all components are deployed |
| `global.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `global.image.pullSecrets` | list | `[]` | Names of `imagePullSecret` objects |
| `global.podLabels` | map | `{}` | Extra labels applied to all pods |
| `global.podAnnotations` | map | `{}` | Extra annotations applied to all pods |
| `global.prometheusURL` | string | `http://prometheus-operated.monitoring.svc.cluster.local:9090` | Prometheus base URL for the manager (passed as `--prometheus-url`; overrides `clusterAgent.args.prometheusURL` when set) |

## Cluster Agent

| Key | Type | Default | Description |
|---|---|---|---|
| `clusterAgent.enabled` | bool | `true` | Deploy the cluster agent |
| `clusterAgent.image.repository` | string | `ghcr.io/optipilot-ai/optipilot/manager` | Image repository |
| `clusterAgent.image.tag` | string | `""` | Image tag (defaults to Chart `appVersion`) |
| `clusterAgent.replicas` | int | `1` | Number of cluster agent replicas |
| `clusterAgent.leaderElect` | bool | `true` | Enable leader election (required for replicas > 1) |
| `clusterAgent.logLevel` | string | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `clusterAgent.metricsPort` | int | `8080` | Prometheus metrics port |
| `clusterAgent.dashboardPort` | int | `8090` | REST API and dashboard port |
| `clusterAgent.reconcileIntervalSeconds` | int | `30` | How often the controller reconciles |
| `clusterAgent.resources.requests.cpu` | string | `100m` | CPU request |
| `clusterAgent.resources.requests.memory` | string | `128Mi` | Memory request |
| `clusterAgent.resources.limits.memory` | string | `512Mi` | Memory limit |
| `clusterAgent.persistence.enabled` | bool | `true` | Persist SQLite journal to PVC |
| `clusterAgent.persistence.size` | string | `2Gi` | PVC size |
| `clusterAgent.persistence.storageClass` | string | `""` | Storage class (uses default if empty) |

## Prometheus Integration

| Key | Type | Default | Description |
|---|---|---|---|
| `global.prometheusURL` | string | see Global Settings | Preferred way to set Prometheus for the cluster agent |
| `clusterAgent.args.prometheusURL` | string | `""` | Per-release override; empty uses chart default unless `global.prometheusURL` is set |
| `clusterAgent.args.requirePrometheus` | bool | `true` | **Production default:** manager exits on startup if Prometheus is unreachable or range queries fail (`--require-prometheus`). Set `false` only for offline/dev. |
| `clusterAgent.args.requireMLForecast` | bool | `true` | **Production default:** when `--ml-service-url` is set, manager exits if ML `GET /v1/health` is not OK. Ignored when no ML URL. Set `false` to debug without a healthy ML pod. |

The manager also accepts `--ml-service-url` for ML forecasts; set `clusterAgent.args.mlServiceURL` or `autoDiscoverMLService` when `mlService.enabled` is true.

## ML Service

| Key | Type | Default | Description |
|---|---|---|---|
| `mlService.enabled` | bool | `false` | Deploy the predictive ML service |
| `mlService.image.repository` | string | `ghcr.io/optipilot-ai/optipilot/ml` | Image repository |
| `mlService.image.tag` | string | `""` | Image tag |
| `mlService.replicas` | int | `1` | Number of ML service replicas |
| `mlService.port` | int | `8000` | HTTP port for ML service |
| `mlService.model.type` | string | `prophet` | Forecasting model: `prophet`, `lightgbm`, or `ensemble` |
| `mlService.model.trainingIntervalHours` | int | `6` | How often to retrain models |
| `mlService.model.lookbackDays` | int | `14` | Training data window |
| `mlService.persistence.enabled` | bool | `true` | Persist trained models to PVC |
| `mlService.persistence.size` | string | `5Gi` | PVC size |
| `mlService.resources.requests.cpu` | string | `500m` | CPU request |
| `mlService.resources.requests.memory` | string | `1Gi` | Memory request |
| `mlService.resources.limits.memory` | string | `4Gi` | Memory limit |

## Hub (Multi-Cluster)

| Key | Type | Default | Description |
|---|---|---|---|
| `hub.enabled` | bool | `false` | Deploy the multi-cluster hub |
| `hub.image.repository` | string | `ghcr.io/optipilot-ai/optipilot/hub` | Image repository |
| `hub.image.tag` | string | `""` | Image tag |
| `hub.replicas` | int | `1` | Number of hub replicas |
| `hub.grpcPort` | int | `50051` | gRPC listener port |
| `hub.resources.requests.cpu` | string | `100m` | CPU request |
| `hub.resources.requests.memory` | string | `128Mi` | Memory request |

## mTLS

| Key | Type | Default | Description |
|---|---|---|---|
| `mtls.enabled` | bool | `false` | Enable mTLS between hub and agents |
| `mtls.certManagerIssuer` | string | `""` | cert-manager `Issuer` or `ClusterIssuer` name |
| `mtls.certDurationHours` | int | `720` | Certificate lifetime in hours |
| `mtls.certRenewBeforeHours` | int | `168` | Renew certificate this many hours before expiry |

## Ingress / Dashboard

| Key | Type | Default | Description |
|---|---|---|---|
| `ingress.enabled` | bool | `false` | Create an `Ingress` for the dashboard |
| `ingress.className` | string | `nginx` | Ingress class name |
| `ingress.host` | string | `optipilot.example.com` | Dashboard hostname |
| `ingress.tls.enabled` | bool | `false` | Enable TLS on ingress |
| `ingress.tls.secretName` | string | `optipilot-tls` | TLS secret name |

## Service Monitor (Prometheus Operator)

| Key | Type | Default | Description |
|---|---|---|---|
| `serviceMonitor.enabled` | bool | `false` | Create a `ServiceMonitor` for prometheus-operator |
| `serviceMonitor.interval` | string | `30s` | Scrape interval |
| `serviceMonitor.namespace` | string | `monitoring` | Namespace where ServiceMonitor is created |
| `serviceMonitor.labels` | map | `{}` | Labels to match prometheus-operator's `serviceMonitorSelector` |

## Full Production Example

```yaml
global:
  namespace: optipilot-system

clusterAgent:
  enabled: true
  replicas: 2
  leaderElect: true
  logLevel: info
  prometheus:
    url: http://prometheus-operated.monitoring.svc:9090
  persistence:
    enabled: true
    size: 10Gi
    storageClass: gp3
  resources:
    requests:
      cpu: 200m
      memory: 256Mi
    limits:
      memory: 1Gi

mlService:
  enabled: true
  replicas: 1
  model:
    type: ensemble
    trainingIntervalHours: 6
  persistence:
    enabled: true
    size: 20Gi

hub:
  enabled: true
  replicas: 2

mtls:
  enabled: true
  certManagerIssuer: cluster-issuer

serviceMonitor:
  enabled: true
  interval: 15s
  namespace: monitoring
  labels:
    release: prometheus
```
