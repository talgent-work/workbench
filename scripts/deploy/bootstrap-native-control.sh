#!/usr/bin/env bash
set -euo pipefail

NS="${TALGENT_NAMESPACE:-talgent}"
SECRET_NAME="native-execution-control"
keys=(AGENT_CONTROL_TOKEN AGENT_FEEDBACK_TOKEN DISPATCHER_EXECUTION_CONTROL_TOKEN STORAGE_DIRECTORY_TOKEN STORAGE_AGENT_TOKEN STORAGE_CONTENT_TOKEN STORAGE_LINK_SECRET GATEWAY_TOKEN COMPUTER_CONTROL_TOKEN)

# Inspect lengths only; credential values never enter logs or command arguments.
existing="$(kubectl -n "${NS}" get secret "${SECRET_NAME}" --ignore-not-found -o go-template='{{range $key, $value := .data}}{{printf "%s %d\n" $key (len $value)}}{{end}}')"
umask 077
credential_dir="$(mktemp -d)"
trap 'rm -rf "${credential_dir}"' EXIT
if [[ -n "${existing}" ]]; then
  for key in "${keys[@]}"; do
    if awk -v key="${key}" '$1 == key && $2 >= 32 { found=1 } END { exit !found }' <<< "${existing}"; then
      continue
    fi
    if awk -v key="${key}" '$1 == key { found=1 } END { exit !found }' <<< "${existing}"; then
      echo "${SECRET_NAME} holds an invalid ${key}; existing credentials were not changed" >&2
      exit 1
    fi
    # A key a release introduces is added; present keys are never rotated.
    printf '[{"op":"add","path":"/data/%s","value":"%s"}]' "${key}" "$(openssl rand -hex 32 | tr -d '\n' | base64 -w0)" > "${credential_dir}/patch.json"
    kubectl -n "${NS}" patch secret "${SECRET_NAME}" --type=json --patch-file "${credential_dir}/patch.json" >/dev/null
    echo "${SECRET_NAME} gained ${key}"
  done
  echo "${SECRET_NAME} contains the required credentials"
  exit 0
fi

args=()
for key in "${keys[@]}"; do
  openssl rand -hex 32 | tr -d '\n' > "${credential_dir}/${key}"
  args+=("--from-file=${key}=${credential_dir}/${key}")
done
# Create only: a concurrent creation fails instead of replacing active keys.
kubectl -n "${NS}" create secret generic "${SECRET_NAME}" "${args[@]}"
