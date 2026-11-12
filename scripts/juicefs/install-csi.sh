#!/usr/bin/env bash
set -euo pipefail

CSI_VERSION="${JUICEFS_CSI_VERSION:-v0.31.6}"
CONTROLLER_REPLICAS="${JUICEFS_CSI_CONTROLLER_REPLICAS:-1}"
DASHBOARD_REPLICAS="${JUICEFS_CSI_DASHBOARD_REPLICAS:-0}"
MANIFEST_URL="https://raw.githubusercontent.com/juicedata/juicefs-csi-driver/${CSI_VERSION}/deploy/k8s.yaml"
CONFIRM="${JUICEFS_CSI_INSTALL_CONFIRM:-}"

kubectl cluster-info >/dev/null

if [ "$CONFIRM" != "cluster-wide" ]; then
  current_context="$(kubectl config current-context 2>/dev/null || true)"
  cluster_server="$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || true)"
  cat >&2 <<EOF
Refusing to install or modify cluster-wide JuiceFS CSI resources without confirmation.
Current context: ${current_context:-unknown}
Cluster server: ${cluster_server:-unknown}

Set JUICEFS_CSI_INSTALL_CONFIRM=cluster-wide to confirm that this cluster may receive the cluster-wide JuiceFS CSI driver.
EOF
  exit 2
fi

kubectl apply -f "$MANIFEST_URL"

kubectl -n kube-system scale statefulset/juicefs-csi-controller --replicas="$CONTROLLER_REPLICAS"
kubectl -n kube-system scale deployment/juicefs-csi-dashboard --replicas="$DASHBOARD_REPLICAS"

kubectl -n kube-system rollout status statefulset/juicefs-csi-controller --timeout=300s
kubectl -n kube-system rollout status daemonset/juicefs-csi-node --timeout=300s
kubectl -n kube-system wait --for=condition=Ready pod -l app=juicefs-csi-controller --timeout=300s
kubectl -n kube-system wait --for=condition=Ready pod -l app=juicefs-csi-node --timeout=300s

kubectl -n kube-system get pods -l app.kubernetes.io/name=juicefs-csi-driver
echo "JuiceFS CSI Driver ${CSI_VERSION} is ready."
