# Sourced by the MySQL bootstrap scripts. The connection defaults are the local
# middleware's; a deployment's come from its environment
# (scripts/deploy/lib/environment.sh) and the operator's MYSQL_PASSWORD.

MYSQL_ADDR="${MYSQL_ADDR:-127.0.0.1:3306}"
MYSQL_USER="${MYSQL_USER:-talgent}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-}"
MYSQL_SCHEMA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../deploy/middleware/init/mysql" && pwd -P)"

mysql_exec() {
  MYSQL_PWD="${MYSQL_PASSWORD}" mysql \
    --host="${MYSQL_ADDR%:*}" \
    --port="${MYSQL_ADDR##*:}" \
    --user="${MYSQL_USER}" \
    "$@"
}

# apply_mysql_schema FILE DATABASE...: creates each distinct database and
# applies the current schema FILE to it (CREATE ... IF NOT EXISTS, repeatable).
# FILE is an absolute path or a file of the Workbench's schema directory.
apply_mysql_schema() {
  local sql_file="$1" db
  [[ "${sql_file}" == /* ]] || sql_file="${MYSQL_SCHEMA_DIR}/${sql_file}"
  shift
  if [[ ! -r "${sql_file}" ]]; then
    echo "missing readable SQL file: ${sql_file}" >&2
    exit 1
  fi
  declare -A seen=()
  for db in "$@"; do
    [[ -n "${seen[${db}]:-}" ]] && continue
    seen["${db}"]=1
    mysql_exec --execute="CREATE DATABASE IF NOT EXISTS \`${db}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
    echo "apply ${db}/$(basename "${sql_file}")"
    mysql_exec --database="${db}" < "${sql_file}"
    mysql_exec --database="${db}" --execute="SHOW TABLES;"
  done
}
