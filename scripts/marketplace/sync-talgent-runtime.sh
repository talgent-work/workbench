#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
export TALGENT_PLUGIN_ID="${TALGENT_PLUGIN_ID:-${TALGENT_RUNTIME_PLUGIN_ID:-talgent-runtime}}"
exec bash "${SCRIPT_DIR}/sync-plugin.sh" "$@"
