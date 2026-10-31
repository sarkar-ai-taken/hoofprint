#!/usr/bin/env bash
# Creates the dev cluster described in ROADMAP M0: kind + Argo CD + Argo
# Rollouts + kube-prometheus-stack, then builds and deploys the sample app.
#
#   hack/kind-up.sh            everything
#   SKIP_ARGOCD=1 hack/kind-up.sh   skip Argo CD (saves ~1 min and 500Mi)
#   WITH_FLAGGER=1 hack/kind-up.sh  also install Flagger
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER=hoofprint
need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1 ($2)"; exit 1; }; }
need kind "go install sigs.k8s.io/kind@latest"
need kubectl "https://kubernetes.io/docs/tasks/tools/"
need helm "https://helm.sh/docs/intro/install/"
need docker "https://docs.docker.com/get-docker/"

if ! kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "==> creating kind cluster $CLUSTER"
  kind create cluster --config hack/kind-config.yaml --wait 120s
else
  echo "==> kind cluster $CLUSTER already exists"
fi
kubectl config use-context "kind-$CLUSTER" >/dev/null

echo "==> installing Argo Rollouts"
kubectl create namespace argo-rollouts --dry-run=client -o yaml | kubectl apply -f -
# Server-side apply: the Rollouts CRDs exceed the client-side annotation limit.
kubectl apply --server-side --force-conflicts -n argo-rollouts -f https://github.com/argoproj/argo-rollouts/releases/latest/download/install.yaml

if [[ "${SKIP_ARGOCD:-}" != "1" ]]; then
  echo "==> installing Argo CD"
  kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
  kubectl apply --server-side --force-conflicts -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
fi

echo "==> installing kube-prometheus-stack"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
helm repo update >/dev/null
helm upgrade --install prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring --create-namespace \
  --set grafana.enabled=false \
  --set alertmanager.enabled=false \
  --set kubeStateMetrics.enabled=true \
  --set nodeExporter.enabled=false \
  --set prometheus.prometheusSpec.scrapeInterval=5s \
  --set prometheus.prometheusSpec.evaluationInterval=5s \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.retention=2d \
  --wait --timeout 10m

if [[ "${WITH_FLAGGER:-}" == "1" ]]; then
  echo "==> installing Flagger"
  helm repo add flagger https://flagger.app >/dev/null 2>&1 || true
  helm upgrade --install flagger flagger/flagger --namespace flagger-system --create-namespace \
    --set meshProvider=kubernetes --set metricsServer=http://prometheus-kube-prometheus-prometheus.monitoring:9090 --wait
fi

echo "==> building sample-app image"
docker build -q -f examples/sample-app/Dockerfile -t hoofprint/sample-app:dev --build-arg APP_VERSION=v1 .
kind load docker-image hoofprint/sample-app:dev --name "$CLUSTER"

echo "==> deploying sample-app (tier 1: plain Deployment)"
kubectl apply -f examples/sample-app/k8s/deployment.yaml
kubectl apply -f examples/sample-app/k8s/servicemonitor.yaml
kubectl -n sample rollout status deployment/sample-app --timeout=120s

cat <<MSG

Cluster ready. Next, in two terminals from the repo root:

  make run-api        # control plane on :8080 (HTTP) / :9090 (gRPC), in-memory store
  make run-agent      # agent against kind-$CLUSTER, connects to localhost:9090

Then trigger a deploy and watch it land:

  kubectl -n sample set env deployment/sample-app INJECT_LATENCY_MS=300 APP_VERSION=v2
  go run ./cmd/cli deploys

Prometheus: kubectl -n monitoring port-forward svc/prometheus-kube-prometheus-prometheus 9091:9090
MSG
