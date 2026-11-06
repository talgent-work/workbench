#!/usr/bin/env bash
# Creates the Workbench's MySQL databases with its current schema,
# 001_workbench.sql. The database names default to the core's own; a
# deployment maps them in its environment (scripts/deploy/lib/environment.sh).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=lib/mysql.sh
source "${ROOT_DIR}/scripts/deploy/lib/mysql.sh"

apply_mysql_schema 001_workbench.sql \
  "${ACCOUNT_MYSQL_DATABASE:-talgent_account}" \
  "${AGENT_MYSQL_DATABASE:-talgent_agent}" \
  "${WORKBENCH_MYSQL_DATABASE:-talgent_workbench}"
