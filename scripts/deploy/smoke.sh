#!/usr/bin/env bash
set -euo pipefail

NS="${TALGENT_NAMESPACE:-talgent}"

if [ "${NS}" != "talgent" ]; then
  echo "TALGENT_NAMESPACE=${NS} is not supported; current MVP manifests are fixed to namespace talgent" >&2
  exit 1
fi

status=0

check_rollout() {
  local deployment="$1"

  if ! kubectl -n "${NS}" rollout status "deployment/${deployment}" --timeout=180s; then
    status=1
  fi
}

# The Workbench's services, then any further deployments given as
# arguments, such as an overlay's.
for deployment in dispatcher streamgateway controlplane workbench-web "$@"; do
  check_rollout "${deployment}"
done

# Account publishes the keys the gateway validates Identity Tokens with.
if ! kubectl -n "${NS}" exec deploy/controlplane -- bash -c 'exec 3<>/dev/tcp/127.0.0.1/8080 && printf "GET /auth/jwks.json HTTP/1.0\r\n\r\n" >&3 && cat <&3' | grep -q '"keys":\[{'; then
  echo "Account published no signing keys at /auth/jwks.json" >&2
  status=1
fi

kubectl -n "${NS}" get pods -o wide
kubectl -n "${NS}" get svc

echo "Port-forward the Workbench's web app with:"
echo "kubectl -n ${NS} port-forward svc/workbench-web 3000:3000"

exit "${status}"
