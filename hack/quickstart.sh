#!/usr/bin/env bash
# hack/quickstart.sh — OptiPilot AI one-command demo
#
# Creates a kind cluster, installs Prometheus + OptiPilot, deploys a sample
# application, applies a ServiceObjective + OptimizationPolicy, and
# port-forwards the OptiPilot API and Prometheus UI. Re-running is fully idempotent.
#
# Usage:
#   ./hack/quickstart.sh                # spin up the demo
#   ./hack/quickstart.sh --build-local  # build manager image from source
#   ./hack/quickstart.sh --destroy      # tear down everything
#   ./hack/quickstart.sh --help         # show usage
#
# Requirements: kind, kubectl, helm, docker (running daemon)

set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────
CLUSTER_NAME="${CLUSTER_NAME:-optipilot-quickstart}"
REGISTRY="${REGISTRY:-ghcr.io/optipilot-ai/optipilot}"
VERSION="${VERSION:-latest}"
RELEASE_NAME="optipilot"
NS_SYSTEM="optipilot-system"
NS_MONITORING="monitoring"
NS_DEMO="demo"
DASHBOARD_LOCAL_PORT="8090"
PROMETHEUS_LOCAL_PORT="9090"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "${SCRIPT_DIR}")"

# ── Parse flags ────────────────────────────────────────────────────────────
DESTROY=false
BUILD_LOCAL=false

for arg in "$@"; do
  case "${arg}" in
    --destroy)      DESTROY=true ;;
    --build-local)  BUILD_LOCAL=true ;;
    --help|-h)
      cat <<HELP
OptiPilot AI Quickstart

Usage:
  $(basename "$0") [OPTIONS]

Options:
  (none)           Spin up full demo environment (idempotent)
  --build-local    Build the manager container image locally with Docker
                   instead of pulling from ghcr.io (requires Go toolchain)
  --destroy        Delete the kind cluster and all associated resources
  --help, -h       Show this help message

Environment Variables:
  CLUSTER_NAME     Kind cluster name          (default: optipilot-quickstart)
  REGISTRY         Container registry prefix  (default: ghcr.io/optipilot-ai/optipilot)
  VERSION          Image tag to use           (default: latest)

Examples:
  ./hack/quickstart.sh
  ./hack/quickstart.sh --build-local
  ./hack/quickstart.sh --destroy
  CLUSTER_NAME=my-demo ./hack/quickstart.sh
HELP
      exit 0
      ;;
    *)
      echo "Unknown flag: ${arg}. Run with --help for usage."
      exit 1
      ;;
  esac
done

# ── Helpers ────────────────────────────────────────────────────────────────
print_header() {
  echo
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  printf "  %s\n" "$*"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

ok()   { echo "  ✓ $*"; }
info() { echo "    $*"; }
warn() { echo "  ⚠  $*"; }

# ── Destroy path ──────────────────────────────────────────────────────────
if ${DESTROY}; then
  print_header "Destroying OptiPilot quickstart"

  if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    info "Deleting kind cluster '${CLUSTER_NAME}'…"
    kind delete cluster --name "${CLUSTER_NAME}"
    ok "Cluster '${CLUSTER_NAME}' deleted."
  else
    warn "Cluster '${CLUSTER_NAME}' not found — nothing to delete."
  fi

  # Kill any lingering port-forwards from this session
  if command -v lsof &>/dev/null; then
    for port in "${DASHBOARD_LOCAL_PORT}" "${PROMETHEUS_LOCAL_PORT}"; do
      pids=$(lsof -ti :"${port}" 2>/dev/null || true)
      if [ -n "${pids}" ]; then
        echo "${pids}" | xargs kill -9 2>/dev/null || true
        info "Released port ${port}."
      fi
    done
  fi

  echo
  echo "  All quickstart resources removed."
  exit 0
fi

# ── Prerequisites ──────────────────────────────────────────────────────────
print_header "OptiPilot AI — Quickstart Demo"

MISSING=()
for cmd in kind kubectl helm docker; do
  if ! command -v "${cmd}" &>/dev/null; then
    MISSING+=("${cmd}")
  fi
done

if [ ${#MISSING[@]} -gt 0 ]; then
  echo
  echo "  ✗ Missing required tools: ${MISSING[*]}"
  echo
  echo "  Install instructions:"
  echo "    kind:    https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
  echo "    kubectl: https://kubernetes.io/docs/tasks/tools/"
  echo "    helm:    https://helm.sh/docs/intro/install/"
  echo "    docker:  https://docs.docker.com/get-docker/"
  exit 1
fi
ok "Prerequisites: kind, kubectl, helm, docker — all found."

# ── Step 1: kind cluster ───────────────────────────────────────────────────
print_header "Step 1/7 — Creating kind cluster"

if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  info "Cluster '${CLUSTER_NAME}' already exists — reusing."
else
  info "Creating cluster '${CLUSTER_NAME}' (this takes ~30 seconds)…"
  kind create cluster \
    --name "${CLUSTER_NAME}" \
    --config "${SCRIPT_DIR}/kind-config.yaml" \
    --wait 90s
  ok "Cluster '${CLUSTER_NAME}' created."
fi

kubectl config use-context "kind-${CLUSTER_NAME}" &>/dev/null
ok "kubectl context set to kind-${CLUSTER_NAME}."

# ── Step 2: kube-prometheus-stack ─────────────────────────────────────────
print_header "Step 2/7 — Installing Prometheus"

helm repo add prometheus-community \
  https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update 2>/dev/null || true

kubectl create namespace "${NS_MONITORING}" --dry-run=client -o yaml | kubectl apply -f -

if helm status kube-prometheus-stack -n "${NS_MONITORING}" &>/dev/null 2>&1; then
  info "kube-prometheus-stack already installed — skipping."
else
  info "Installing kube-prometheus-stack (grafana + alertmanager disabled for speed)…"
  helm upgrade --install kube-prometheus-stack \
    prometheus-community/kube-prometheus-stack \
    --namespace "${NS_MONITORING}" \
    --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
    --set grafana.enabled=false \
    --set alertmanager.enabled=false \
    --timeout 5m \
    --wait
  ok "Prometheus ready."
fi

PROM_URL="http://kube-prometheus-stack-prometheus.${NS_MONITORING}.svc:9090"

# ── Step 3: Manager image ─────────────────────────────────────────────────
print_header "Step 3/7 — Preparing container image"

if ${BUILD_LOCAL}; then
  LOCAL_IMAGE="optipilot-manager:quickstart"
  info "Building manager image from source (Dockerfile)…"
  docker build \
    --tag "${LOCAL_IMAGE}" \
    --file "${REPO_ROOT}/Dockerfile" \
    --build-arg VERSION="quickstart" \
    "${REPO_ROOT}"
  info "Loading image into kind cluster…"
  kind load docker-image "${LOCAL_IMAGE}" --name "${CLUSTER_NAME}"
  CHART_IMAGE_REPO="optipilot-manager"
  CHART_IMAGE_TAG="quickstart"
  ok "Manager image built and loaded (${LOCAL_IMAGE})."
else
  CHART_IMAGE_REPO="${REGISTRY}/manager"
  CHART_IMAGE_TAG="${VERSION}"
  ok "Using published image ${CHART_IMAGE_REPO}:${CHART_IMAGE_TAG}"
  info "Tip: pass --build-local to build from source instead."
fi

# ── Step 4: Install OptiPilot ─────────────────────────────────────────────
print_header "Step 4/7 — Installing OptiPilot"

kubectl create namespace "${NS_SYSTEM}" --dry-run=client -o yaml | kubectl apply -f -

if helm status "${RELEASE_NAME}" -n "${NS_SYSTEM}" &>/dev/null 2>&1; then
  info "Release '${RELEASE_NAME}' already installed — upgrading…"
  HELM_CMD="upgrade"
else
  info "Installing OptiPilot via Helm…"
  HELM_CMD="install"
fi

helm "${HELM_CMD}" "${RELEASE_NAME}" "${REPO_ROOT}/helm/optipilot" \
  --namespace "${NS_SYSTEM}" \
  --create-namespace \
  --set "clusterAgent.image.repository=${CHART_IMAGE_REPO}" \
  --set "clusterAgent.image.tag=${CHART_IMAGE_TAG}" \
  --set "global.prometheusURL=${PROM_URL}" \
  --set "mlService.enabled=false" \
  --set "hub.enabled=false" \
  --timeout 3m \
  --wait

ok "OptiPilot installed in namespace '${NS_SYSTEM}'."

# ── Step 5: Sample application ────────────────────────────────────────────
print_header "Step 5/7 — Deploying sample application"

kubectl create namespace "${NS_DEMO}" --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -n "${NS_DEMO}" -f - <<'SAMPLE_EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo-api
  labels:
    app: demo-api
    tier: demo
spec:
  replicas: 2
  selector:
    matchLabels:
      app: demo-api
  template:
    metadata:
      labels:
        app: demo-api
        tier: demo
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "80"
        prometheus.io/path: "/"
    spec:
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          ports:
            - name: http
              containerPort: 80
          resources:
            requests:
              cpu: "50m"
              memory: "64Mi"
            limits:
              cpu: "200m"
              memory: "128Mi"
          readinessProbe:
            httpGet:
              path: /
              port: 80
            initialDelaySeconds: 3
            periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: demo-api
spec:
  selector:
    app: demo-api
  ports:
    - port: 80
      targetPort: 80
SAMPLE_EOF

ok "Sample application 'demo-api' deployed (2 replicas, nginx:1.27-alpine)."

# ── Step 6: Custom resources ──────────────────────────────────────────────
print_header "Step 6/7 — Creating ServiceObjective + OptimizationPolicy"

kubectl apply -f - <<'CRS_EOF'
apiVersion: slo.optipilot.ai/v1alpha1
kind: ServiceObjective
metadata:
  name: demo-api-slo
  namespace: demo
  labels:
    tier: demo
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: demo-api
  objectives:
    - metric: availability
      target: "99.9%"
      window: "5m"
    - metric: latency_p99
      target: "500ms"
      window: "5m"
  errorBudget:
    total: "0.1%"
    burnRateAlerts:
      - severity: warning
        shortWindow: "5m"
        longWindow: "1h"
        factor: 14.4
  evaluationInterval: "30s"
---
apiVersion: policy.optipilot.ai/v1alpha1
kind: OptimizationPolicy
metadata:
  name: demo-policy
  namespace: demo
spec:
  selector:
    matchLabels:
      tier: demo
  objectives:
    - name: slo_compliance
      weight: 0.6
      direction: maximize
    - name: cost
      weight: 0.3
      direction: minimize
    - name: carbon
      weight: 0.1
      direction: minimize
  constraints:
    - expr: "candidate.replicas >= 1"
      reason: "Always keep at least 1 replica"
      hard: true
    - expr: "candidate.replicas <= 10"
      reason: "Cap at 10 replicas for demo"
      hard: true
  scalingBehavior:
    scaleUp:
      maxPercent: 100
      cooldownSeconds: 60
    scaleDown:
      maxPercent: 20
      cooldownSeconds: 120
  dryRun: true
  priority: 100
CRS_EOF

ok "ServiceObjective 'demo-api-slo' created."
ok "OptimizationPolicy 'demo-policy' created (dryRun=true — no actual actuation)."

# ── Step 7: Port-forward dashboard ────────────────────────────────────────
print_header "Step 7/7 — Starting dashboard port-forward"

# Release any existing port-forwards on the local ports
if command -v lsof &>/dev/null; then
  for port in "${DASHBOARD_LOCAL_PORT}" "${PROMETHEUS_LOCAL_PORT}"; do
    existing_pids=$(lsof -ti :"${port}" 2>/dev/null || true)
    if [ -n "${existing_pids}" ]; then
      echo "${existing_pids}" | xargs kill -9 2>/dev/null || true
      info "Released existing port-forward on :${port}."
    fi
  done
fi

# Wait for the manager deployment to be ready
info "Waiting for OptiPilot manager deployment to be Ready (up to 2 min)…"
kubectl rollout status \
  deployment/"${RELEASE_NAME}-cluster-agent" \
  -n "${NS_SYSTEM}" \
  --timeout=120s

# Start port-forward in background
kubectl port-forward \
  -n "${NS_SYSTEM}" \
  "svc/${RELEASE_NAME}-cluster-agent" \
  "${DASHBOARD_LOCAL_PORT}:8090" \
  >/tmp/optipilot-dashboard-pf.log 2>&1 &
DASHBOARD_PF_PID=$!

# Start Prometheus port-forward in background so the host can reach it.
kubectl port-forward \
  -n "${NS_MONITORING}" \
  "svc/kube-prometheus-stack-prometheus" \
  "${PROMETHEUS_LOCAL_PORT}:9090" \
  >/tmp/optipilot-prometheus-pf.log 2>&1 &
PROMETHEUS_PF_PID=$!

sleep 2
if kill -0 "${DASHBOARD_PF_PID}" 2>/dev/null; then
  ok "Dashboard port-forward started (PID ${DASHBOARD_PF_PID})."
else
  warn "Port-forward may have failed. Check /tmp/optipilot-dashboard-pf.log"
  warn "Manual: kubectl port-forward -n ${NS_SYSTEM} svc/${RELEASE_NAME}-cluster-agent ${DASHBOARD_LOCAL_PORT}:8090"
fi

sleep 2
if kill -0 "${PROMETHEUS_PF_PID}" 2>/dev/null; then
  ok "Prometheus port-forward started (PID ${PROMETHEUS_PF_PID})."
else
  warn "Prometheus port-forward may have failed. Check /tmp/optipilot-prometheus-pf.log"
  warn "Manual: kubectl port-forward -n ${NS_MONITORING} svc/kube-prometheus-stack-prometheus ${PROMETHEUS_LOCAL_PORT}:9090"
fi

# ── Done ──────────────────────────────────────────────────────────────────
print_header "OptiPilot AI Demo Ready! 🚀"

cat <<WELCOME

  ┌─────────────────────────────────────────────────────────┐
  │  OptiPilot API    http://localhost:${DASHBOARD_LOCAL_PORT}/api/v1/decisions │
  │  Prometheus      http://localhost:${PROMETHEUS_LOCAL_PORT}                  │
  └─────────────────────────────────────────────────────────┘

  Quick API exploration:

    # List optimization decisions
    curl -s http://localhost:${DASHBOARD_LOCAL_PORT}/api/v1/decisions | python3 -m json.tool

    # View decision summary (last 1 hour)
    curl -s "http://localhost:${DASHBOARD_LOCAL_PORT}/api/v1/decisions/summary?window=1h" | python3 -m json.tool

    # Run a what-if simulation
    curl -s -X POST http://localhost:${DASHBOARD_LOCAL_PORT}/api/v1/simulate \\
      -H 'Content-Type: application/json' \\
      -d '{"services":["demo-api"],"description":"quickstart what-if simulation"}' \\
      | python3 -m json.tool

    # View ServiceObjective
    kubectl get serviceobjective demo-api-slo -n ${NS_DEMO} -o yaml

    # Watch manager logs
    kubectl logs -n ${NS_SYSTEM} -l app.kubernetes.io/name=cluster-agent,app.kubernetes.io/instance=${RELEASE_NAME} -f

  Manager logs:
    kubectl logs -n ${NS_SYSTEM} -l app.kubernetes.io/name=cluster-agent,app.kubernetes.io/instance=${RELEASE_NAME}

  Tear down everything:
    ./hack/quickstart.sh --destroy

WELCOME
