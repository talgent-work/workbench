#!/usr/bin/env bash
set -euo pipefail
# Runs the upstream node installer, which restarts the container runtime on selected nodes.
# Node labeling is intentionally separate: only label a node after checking /dev/kvm.
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
for tool in helm kubectl curl sha256sum; do command -v "$tool" >/dev/null; done
if [[ -z $(kubectl get nodes -l talgent.io/kata-install=true -o name) ]]; then
  echo 'No KVM-verified node selected: label eligible nodes talgent.io/kata-install=true first' >&2
  exit 1
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl --fail --location --retry 3 https://github.com/kata-containers/kata-containers/releases/download/4.1.0/kata-deploy-4.1.0.tgz -o "$work/chart.tgz"
printf '%s  %s\n' 285ff2ec8e13b6fad6c95ac2d3742d5d8a323c1781672c519546af9c1f07c772 "$work/chart.tgz" | sha256sum --check
helm upgrade --install kata-deploy "$work/chart.tgz" --namespace kube-system \
  --values "$root_dir/deploy/kata/values.yaml" --wait --timeout 20m
kubectl get runtimeclass kata-qemu-runtime-rs
