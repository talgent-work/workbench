#!/usr/bin/env bash
set -euo pipefail

# Deploys OpenSandbox. Its argument is the manifest
# scripts/deploy/render.sh rendered; the server's permissions, in the
# sandbox namespace, are applied from it. The sandbox namespace and its
# NetworkPolicies come with that manifest.
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
if [[ $# -ne 1 || ! -r "$1" ]]; then
  echo "usage: $(basename "$0") <rendered manifest>" >&2
  exit 2
fi
manifest=$1
for tool in kubectl openssl jq; do
  command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 1; }
done
if [[ ${TALGENT_NAMESPACE:-talgent} != talgent ]]; then
  echo "OpenSandbox deployment is fixed to namespace talgent" >&2
  exit 1
fi
umask 077
deployment_dir=$(mktemp -d)
trap 'rm -rf "$deployment_dir"' EXIT
cp "$root_dir/deploy/opensandbox/server.yaml" "$deployment_dir/server.yaml"
cp "$root_dir/deploy/opensandbox/resources.yaml" "$deployment_dir/resources.yaml"
# The RuntimeClass sandboxes run with: a VM-isolating runtime the cluster
# provides. The bundled default is Kata's QEMU runtime-rs.
runtime_class=${TALGENT_SANDBOX_RUNTIME_CLASS:-kata-qemu-runtime-rs}
if [[ ! "$runtime_class" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]]; then
  echo "TALGENT_SANDBOX_RUNTIME_CLASS must be a RuntimeClass name" >&2
  exit 1
fi
sed -i "s|runtimeClassName: kata-qemu-runtime-rs|runtimeClassName: ${runtime_class}|" "$deployment_dir/resources.yaml"
# How the script confirms that pod traffic between nodes is encrypted, since
# credential traffic to sandboxes crosses nodes: flannel-wireguard reads the
# nodes' Flannel backend annotations; operator means the operator vouches for
# their own pod network's encryption and the script checks nothing.
network_check=${TALGENT_POD_NETWORK_CHECK:-flannel-wireguard}
if [[ "$network_check" != flannel-wireguard && "$network_check" != operator ]]; then
  echo "TALGENT_POD_NETWORK_CHECK must be flannel-wireguard or operator" >&2
  exit 1
fi
# Bootstrap releases are independent of product-only image updates. Accept only
# an explicit complete published reference when overriding the checked-in pin.
if [[ -n "${TALGENT_EXECD_IMAGE:-}" ]]; then
  if [[ ! "$TALGENT_EXECD_IMAGE" =~ ^[a-zA-Z0-9._/:-]+(:[a-zA-Z0-9_][a-zA-Z0-9_.-]*|@sha256:[a-f0-9]{64})$ ]]; then
    echo "TALGENT_EXECD_IMAGE must be a complete image tag or digest without whitespace" >&2
    exit 1
  fi
  sed -i -E "s|execd_image = \"[^\"]+\"|execd_image = \"${TALGENT_EXECD_IMAGE}\"|" "$deployment_dir/server.yaml"
fi
runtime_hash=$(cat "$deployment_dir/resources.yaml" "$deployment_dir/server.yaml" | openssl dgst -sha256 | awk '{print $NF}')
sed -i "s|TALGENT_RUNTIME_CONFIG_SHA256|$runtime_hash|" "$deployment_dir/server.yaml"
kubectl get runtimeclass "$runtime_class" >/dev/null
# Inspect real node annotations, not a user-set assertion that encryption exists.
if [[ "$network_check" == flannel-wireguard ]] && ! kubectl get nodes -o json | jq -e '
  (.items | length > 0) and all(.items[];
    .metadata.annotations["flannel.alpha.coreos.com/backend-type"] == "wireguard" and
    (.metadata.annotations["flannel.alpha.coreos.com/backend-data"] | fromjson | (.PublicKey | length > 0) and .Port > 0))
' >/dev/null; then
  echo "All cluster nodes must report Flannel WireGuard with a public key before credential management traffic is enabled" >&2
  exit 1
fi
kubectl create namespace talgent --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace opensandbox-system --dry-run=client -o yaml | kubectl apply -f -
key_length=$(kubectl -n talgent get secret opensandbox-api --ignore-not-found -o go-template='{{with .data}}{{len (index . "api-key")}}{{end}}')
if [[ -z "$key_length" ]]; then
  openssl rand -hex 32 | tr -d '\n' > "$deployment_dir/api-key"
  kubectl -n talgent create secret generic opensandbox-api --from-file="api-key=$deployment_dir/api-key"
elif [[ ! "$key_length" =~ ^[0-9]+$ ]] || (( key_length < 32 )); then
  echo "opensandbox-api has no valid api-key; existing credentials were not changed" >&2
  exit 1
fi
kubectl apply -f "$deployment_dir/resources.yaml"
kubectl apply --server-side -f "$root_dir/deploy/opensandbox/controller.yaml"
kubectl wait --for=condition=Established --timeout=60s crd/batchsandboxes.sandbox.opensandbox.io crd/pools.sandbox.opensandbox.io crd/sandboxsnapshots.sandbox.opensandbox.io
kubectl -n opensandbox-system rollout status deployment/opensandbox-controller-manager --timeout=300s
kubectl apply -f "$manifest" -l app.kubernetes.io/name=opensandbox-server
kubectl apply -f "$deployment_dir/server.yaml"
kubectl -n talgent rollout status deployment/opensandbox --timeout=300s
