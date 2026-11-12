#!/usr/bin/env bash
set -euo pipefail

# scripts/deploy/apply-opensandbox.sh against a stubbed kubectl: it checks the
# configured sandbox RuntimeClass and applies it in the workload template, and
# it refuses nodes without Flannel WireGuard unless the operator vouches for
# their pod network's encryption.
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/bin"
printf 'kind: List\n' > "$fixture/manifest.yaml"

cat > "$fixture/bin/kubectl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
echo "kubectl $*" >> "$PROBE_ARGS"
case "$*" in
  "get nodes -o json") cat "$PROBE_NODES" ;;
  *"get secret opensandbox-api"*) printf '64' ;;
  "apply -f "*resources.yaml) cp "${@: -1}" "$PROBE_RESOURCES" ;;
  *) : ;;
esac
SH
chmod +x "$fixture/bin/kubectl"

flannel='{"items":[{"metadata":{"annotations":{"flannel.alpha.coreos.com/backend-type":"wireguard","flannel.alpha.coreos.com/backend-data":"{\"PublicKey\":\"k\",\"Port\":51820}"}}}]}'
plain='{"items":[{"metadata":{"annotations":{}}}]}'

run() { # nodes-json, then env assignments
  local nodes=$1; shift
  : > "$fixture/args"; rm -f "$fixture/resources"
  printf '%s' "$nodes" > "$fixture/nodes.json"
  env PATH="$fixture/bin:$PATH" PROBE_ARGS="$fixture/args" PROBE_NODES="$fixture/nodes.json" \
    PROBE_RESOURCES="$fixture/resources" "$@" \
    bash "$root_dir/scripts/deploy/apply-opensandbox.sh" "$fixture/manifest.yaml" > "$fixture/out" 2>&1
}
fail() { echo "FAIL: $*" >&2; cat "$fixture/out" >&2; exit 1; }

run "$flannel" || fail "default deploy on Flannel WireGuard nodes"
grep -q 'get runtimeclass kata-qemu-runtime-rs' "$fixture/args" || fail "default RuntimeClass not checked"
grep -q 'runtimeClassName: kata-qemu-runtime-rs' "$fixture/resources" || fail "default RuntimeClass not applied"

if run "$plain"; then fail "nodes without Flannel WireGuard were accepted"; fi
grep -q 'Flannel WireGuard' "$fixture/out" || fail "refusal does not name the missing backend"
! grep -q 'apply -f' "$fixture/args" || fail "applied something after refusing the network"

run "$plain" TALGENT_POD_NETWORK_CHECK=operator TALGENT_SANDBOX_RUNTIME_CLASS=gvisor \
  || fail "operator-vouched network with a custom RuntimeClass"
grep -q 'get runtimeclass gvisor' "$fixture/args" || fail "custom RuntimeClass not checked"
grep -q 'runtimeClassName: gvisor' "$fixture/resources" || fail "custom RuntimeClass not applied"
! grep -q 'get nodes' "$fixture/args" || fail "inspected nodes although the operator vouched"

if run "$flannel" TALGENT_POD_NETWORK_CHECK=none; then fail "accepted an unknown network check"; fi
if run "$flannel" TALGENT_SANDBOX_RUNTIME_CLASS='bad name'; then fail "accepted an invalid RuntimeClass"; fi

echo "apply-opensandbox: ok"
