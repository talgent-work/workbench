# Sourced by the deploy scripts. ConfigMap workbench-environment is the single
# source of a deployment's environment. The environment.env files of the Kubernetes
# kustomizations hold placeholders only; a deploy reads the real values from
# TALGENT_ENVIRONMENT_FILE, a KEY=VALUE file kept outside the repository (for
# a first deploy, or to change values), or else from the cluster's ConfigMap.

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../deploy/kubernetes" && pwd -P)"
# The repository the deploy runs from, which may hold the Workbench in a
# subdirectory.
REPOSITORY_DIR="$(cd "$(git -C "${DEPLOY_DIR}" rev-parse --show-toplevel 2>/dev/null || dirname "$(dirname "${DEPLOY_DIR}")")" && pwd -P)"

# deploy_placeholder_files DIR prints the environment.env files kustomize merges
# for the kustomization DIR: the Workbench base's, then DIR's own.
deploy_placeholder_files() {
  local dir
  dir="$(cd "$1" && pwd -P)"
  printf '%s\n' "${DEPLOY_DIR}/workbench/environment.env"
  if [[ "${dir}" != "${DEPLOY_DIR}/workbench" && -f "${dir}/environment.env" ]]; then
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

# resolve_deploy_environment DIR OUT writes the deployment's environment for the
# kustomization DIR to OUT and fails unless it sets every key DIR's
# placeholders name.
resolve_deploy_environment() {
  local dir="$1" out="$2" namespace="${TALGENT_NAMESPACE:-talgent}" file key origin
  # A deploy resolves its environment once (use_deploy_environment); the scripts
  # it runs reuse that file, still checked against DIR's placeholders.
  if [[ -n "${TALGENT_RESOLVED_ENVIRONMENT:-}" ]]; then
    cp "${TALGENT_RESOLVED_ENVIRONMENT}" "${out}"
    origin=""
  elif [[ -n "${TALGENT_ENVIRONMENT_FILE:-}" ]]; then
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
    origin="TALGENT_ENVIRONMENT_FILE"
  else
    local configmap
    if ! configmap="$(kubectl -n "${namespace}" get configmap workbench-environment -o json)"; then
      echo "cannot read ConfigMap ${namespace}/workbench-environment (above); for a first deploy set TALGENT_ENVIRONMENT_FILE to a KEY=VALUE file outside the repository" >&2
      return 1
    fi
    jq -r '.data // {} | to_entries[] | "\(.key)=\(.value)"' <<< "${configmap}" > "${out}"
    origin="ConfigMap ${namespace}/workbench-environment"
  fi
  env_file_keys "${out}" > /dev/null || return 1
  local -A present=()
  while IFS= read -r key; do present["${key}"]=1; done < <(env_file_keys "${out}")
  while IFS= read -r file; do
    while IFS= read -r key; do
      if [[ -z "${present[${key}]:-}" ]]; then
        echo "the environment sets no ${key} (named in ${file#"${DEPLOY_DIR}/"}); add it to TALGENT_ENVIRONMENT_FILE" >&2
        return 1
      fi
    done < <(env_file_keys "${file}")
  done < <(deploy_placeholder_files "${dir}")
  if [[ -n "${origin}" ]]; then
    echo "resolved the environment from ${origin}" >&2
  fi
}

# use_deploy_environment DIR WORK resolves the deployment's environment for the
# kustomization DIR into the directory WORK, unless the deploy running this
# script already has, and exports it to this script and the ones it runs.
use_deploy_environment() {
  if [[ -z "${TALGENT_RESOLVED_ENVIRONMENT:-}" ]]; then
    resolve_deploy_environment "$1" "$2/environment.env"
    export TALGENT_RESOLVED_ENVIRONMENT="$2/environment.env"
  fi
  export_environment "${TALGENT_RESOLVED_ENVIRONMENT}"
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

# environment_value KEY FILE... prints KEY's value in the last FILE that sets
# it, as kustomize merges them.
environment_value() {
  local key="$1" file line value="" found=0
  shift
  for file in "$@"; do
    while IFS= read -r line || [[ -n "${line}" ]]; do
      if [[ "${line}" == "${key}="* ]]; then
        value="${line#*=}"
        found=1
      fi
    done < "${file}"
  done
  if [[ "${found}" == "0" ]]; then
    echo "the environment sets no ${key}" >&2
    return 1
  fi
  printf '%s\n' "${value}"
}

# deploy_switch_components FILE... prints, one per line, the components of the
# base's components/ that the deployment switches of the environment FILE...
# turn on, failing on a value a switch does not take.
deploy_switch_components() {
  access_switch_component AGENT_CORE_API_ACCESS core-api-without-agents "$@" || return 1
  value_switch_component ADDITIONAL_ENTRY_HOST additional-entry-host "$@" || return 1
  value_switch_component CONTROLPLANE_PROFILE controlplane-profile "$@" || return 1
}

# value_switch_component KEY COMPONENT FILE... prints COMPONENT when the
# switch KEY has a value; empty, its default, prints nothing.
value_switch_component() {
  local key="$1" component="$2" value
  shift 2
  value="$(environment_value "${key}" "$@")" || return 1
  if [[ -n "${value}" ]]; then
    printf '%s\n' "${component}"
  fi
}

# access_switch_component KEY COMPONENT FILE... prints COMPONENT when the
# switch KEY is disabled; enabled, its default, prints nothing.
access_switch_component() {
  local key="$1" component="$2" value
  shift 2
  value="$(environment_value "${key}" "$@")" || return 1
  case "${value}" in
    enabled) ;;
    disabled) printf '%s\n' "${component}" ;;
    *)
      echo "${key} is enabled or disabled, not '${value}'" >&2
      return 1
      ;;
  esac
}
