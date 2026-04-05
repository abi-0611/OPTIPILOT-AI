#!/usr/bin/env bash
# hack/e2e-setup.sh — Bootstrap a local e2e environment for OptiPilot.
#
# Requirements:
#   - kind    (https://kind.sigs.k8s.io/)
#   - kubectl (https://kubernetes.io/docs/tasks/tools/)
#   - helm    (https://helm.sh/docs/intro/install/)
#   - docker  (running daemon)
#
# Usage:
#   ./hack/e2e-setup.sh [--cluster-name <name>] [--image-tag <tag>]
#
# The script:
#   1. Creates a kind cluster named "optipilot-e2e"
#   2. Installs kube-prometheus-stack via Helm
#   3. Deploys a sample nginx workload with Prometheus annotations
#   4. Builds and loads the OptiPilot manager image
#   5. Installs OptiPilot CRDs and the manager Deployment
#   6. Creates a sample ServiceObjective

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-optipilot-e2e}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
IMAGE_NAME="optipilot-ai/optipilot-manager:${IMAGE_TAG}"
NAMESPACE_OPTIPILOT="optipilot-system"
NAMESPACE_MONITORING="monitoring"
NAMESPACE_SAMPLE="sample-app"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "${SCRIPT_DIR}")"

# ── 1. Create kind cluster ───────────────────────────────────────────────────
echo "==> Creating kind cluster '${CLUSTER_NAME}'…"
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "    Cluster already exists, reusing."
else
  kind create cluster --name "${CLUSTER_NAME}" --wait 60s
fi
kubectl cluster-info --context "kind-${CLUSTER_NAME}"

# ── 2. Install kube-prometheus-stack ────────────────────────────────────────
echo "==> Installing kube-prometheus-stack…"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update

kubectl create namespace "${NAMESPACE_MONITORING}" --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
  --namespace "${NAMESPACE_MONITORING}" \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --set grafana.enabled=false \
  --wait --timeout 5m

echo "    Prometheus ready."

# ── 3. Deploy sample workload ────────────────────────────────────────────────
echo "==> Deploying sample nginx workload…"
kubectl create namespace "${NAMESPACE_SAMPLE}" --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -n "${NAMESPACE_SAMPLE}" -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-nginx
  labels:
    app: sample-nginx
  annotations:
    optipilot.ai/metrics-prefix: "nginx"
    optipilot.ai/metrics-labels: 'app="sample-nginx"'
spec:
  replicas: 1
  selector:
    matchLabels:
      app: sample-nginx
  template:
    metadata:
      labels:
        app: sample-nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.25
        ports:
        - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: sample-nginx
spec:
  selector:
    app: sample-nginx
  ports:
  - port: 80
    targetPort: 80
EOF

kubectl rollout status deployment/sample-nginx -n "${NAMESPACE_SAMPLE}" --timeout 60s
echo "    Sample workload ready."

# ── 4. Build and load OptiPilot manager image ─────────────────────────────
echo "==> Building OptiPilot manager image '${IMAGE_NAME}'…"
docker build -t "${IMAGE_NAME}" "${REPO_ROOT}"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"
echo "    Image loaded."

# ── 5. Install OptiPilot CRDs and manager ────────────────────────────────────
echo "==> Installing OptiPilot CRDs…"
kubectl apply -f "${REPO_ROOT}/config/crd/bases/"

echo "==> Creating namespace ${NAMESPACE_OPTIPILOT}…"
kubectl create namespace "${NAMESPACE_OPTIPILOT}" --dry-run=client -o yaml | kubectl apply -f -

echo "==> Deploying OptiPilot manager…"
kubectl apply -n "${NAMESPACE_OPTIPILOT}" -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: optipilot-manager
spec:
  replicas: 1
  selector:
    matchLabels:
      control-plane: controller-manager
  template:
    metadata:
      labels:
        control-plane: controller-manager
    spec:
      containers:
      - name: manager
        image: ${IMAGE_NAME}
        imagePullPolicy: IfNotPresent
        args:
        - --prometheus-url=http://kube-prometheus-stack-prometheus.${NAMESPACE_MONITORING}.svc.cluster.local:9090
        - --leader-elect=false
EOF

kubectl rollout status deployment/optipilot-manager -n "${NAMESPACE_OPTIPILOT}" --timeout 120s
echo "    OptiPilot manager ready."

# ── 6. Create sample ServiceObjective ────────────────────────────────────────
echo "==> Creating sample ServiceObjective…"
kubectl apply -n "${NAMESPACE_SAMPLE}" -f - <<'EOF'
apiVersion: slo.optipilot.ai/v1alpha1
kind: ServiceObjective
metadata:
  name: nginx-slo
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: sample-nginx
  objectives:
  - metric: latency_p99
    target: "200ms"
    window: "5m"
  - metric: error_rate
    target: "0.1%"
    window: "5m"
  errorBudget:
    total: "0.05%"
    burnRateAlerts:
    - severity: warning
      shortWindow: "5m"
      longWindow: "1h"
      factor: 14.4
    - severity: critical
      shortWindow: "2m"
      longWindow: "5m"
      factor: 36.0
  evaluationInterval: "30s"
EOF

echo ""
echo "✅ e2e environment is ready!"
echo ""
echo "Quick checks:"
echo "  kubectl get serviceobjective -n ${NAMESPACE_SAMPLE}"
echo "  kubectl get serviceobjective nginx-slo -n ${NAMESPACE_SAMPLE} -o yaml"
echo "  kubectl logs -n ${NAMESPACE_OPTIPILOT} deploy/optipilot-manager"
echo ""
echo "To tear down: kind delete cluster --name ${CLUSTER_NAME}"
