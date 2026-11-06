# Sourced by the deploy scripts. ConfigMap workbench-environment is the single
# source of a deployment's environment. The environment.env files of the k3s
# kustomizations hold placeholders only; a deploy reads the real values from
# TALGENT_ENVIRONMENT_FILE, a KEY=VALUE file kept outside the repository (for
# a first deploy, or to change values), or else from the cluster's ConfigMap.

K3S_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../deploy/k3s" && pwd -P)"
# The repository the deploy runs from, which may hold the Workbench in a
# subdirectory.
REPOSITORY_DIR="$(cd "$(git -C "${K3S_DIR}" rev-parse --show-toplevel 2>/dev/null || dirname "$(dirname "${K3S_DIR}")")" && pwd -P)"

# k3s_placeholder_files DIR prints the environment.env files kustomize merges
# for the kustomization DIR: the Workbench base's, then DIR's own.
k3s_placeholder_files() {
  local dir
  dir="$(cd "$1" && pwd -P)"
  printf '%s\n' "${K3S_DIR}/workbench/environment.env"
  if [[ "${dir}" != "${K3S_DIR}/workbench" && -f "${dir}/environment.env" ]]; then
    printf '%s\n' "${dir}/environment.env"
  fi
}

# env_file_keys FILE prints the keys of a KEY=VALUE file, failing on any other
# line that is neither blank nor a comment.
env_file_keys() {
  local line key
  while IFS= read -r line || [[ -n "${line}" ]]; do
    [[ -z "${line}" || "${line}" == \#* ]] && continue
    key="${line%%=*}"
    if [[ ! "${key}" =~ ^[A-Z_][A-Z0-9_]*$ || "${key}" == "${line}" ]]; then
      echo "$1: not a KEY=VALUE line: ${line}" >&2
      return 1
    fi
    printf '%s\n' "${key}"
  done < "$1"
}

# resolve_k3s_environment DIR OUT writes the deployment's environment for the
# kustomization DIR to OUT and fails unless it sets every key DIR's
# placeholders name.
resolve_k3s_environment() {
  local dir="$1" out="$2" namespace="${TALGENT_NAMESPACE:-talgent}" file key
  if [[ -n "${TALGENT_ENVIRONMENT_FILE:-}" ]]; then
    if [[ ! -r "${TALGENT_ENVIRONMENT_FILE}" ]]; then
      echo "TALGENT_ENVIRONMENT_FILE=${TALGENT_ENVIRONMENT_FILE} is not readable" >&2
      return 1
    fi
    # Real values never enter the repository, so neither does their file.
    file="$(cd "$(dirname "${TALGENT_ENVIRONMENT_FILE}")" && pwd -P)/$(basename "${TALGENT_ENVIRONMENT_FILE}")"
    if [[ "${file}" == "${REPOSITORY_DIR}/"* ]]; then
      echo "TALGENT_ENVIRONMENT_FILE must lie outside the repository" >&2
      return 1
    fi
    cp "${file}" "${out}"
  else
    local configmap
    if ! configmap="$(kubectl -n "${namespace}" get configmap workbench-environment -o json)"; then
      echo "cannot read ConfigMap ${namespace}/workbench-environment (above); for a first deploy set TALGENT_ENVIRONMENT_FILE to a KEY=VALUE file outside the repository" >&2
      return 1
    fi
    jq -r '.data // {} | to_entries[] | "\(.key)=\(.value)"' <<< "${configmap}" > "${out}"
  fi
  env_file_keys "${out}" > /dev/null || return 1
  local -A present=()
  while IFS= read -r key; do present["${key}"]=1; done < <(env_file_keys "${out}")
  while IFS= read -r file; do
    while IFS= read -r key; do
      if [[ -z "${present[${key}]:-}" ]]; then
        echo "the environment sets no ${key} (named in ${file#"${K3S_DIR}/"}); add it to TALGENT_ENVIRONMENT_FILE" >&2
        return 1
      fi
    done < <(env_file_keys "${file}")
  done < <(k3s_placeholder_files "${dir}")
}

# export_environment FILE exports every KEY=VALUE of FILE. The file wins over
# the caller's environment, so the bootstrap reaches the stores the services use.
export_environment() {
  local line
  while IFS= read -r line || [[ -n "${line}" ]]; do
    [[ -z "${line}" || "${line}" == \#* ]] && continue
    export "${line%%=*}=${line#*=}"
  done < "$1"
}
