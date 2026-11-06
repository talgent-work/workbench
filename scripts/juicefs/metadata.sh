#!/usr/bin/env bash
set -euo pipefail
umask 077

if [[ $# != 2 || ( "$1" != backup && "$1" != restore ) ]]; then
  echo 'usage: JUICEFS_META_URL=redis://host:6379/0 REDIS_PASSWORD=... metadata.sh backup|restore FILE' >&2
  exit 2
fi

: "${JUICEFS_META_URL:?set the dedicated Redis metadata URL}"
python3 - <<'PY'
import os
from urllib.parse import urlsplit
url = urlsplit(os.environ['JUICEFS_META_URL'])
if url.scheme not in ('redis', 'rediss') or not url.hostname or url.username or url.password:
    raise SystemExit('use a Redis URL without credentials; supply REDIS_PASSWORD through the environment')
PY

mode="$1"
file="$2"
directory="$(dirname -- "$file")"
name="$(basename -- "$file")"

if [[ "$mode" == backup ]]; then
  # The operator has stopped all file writers before invoking this command.
  # The matching object-store copy is part of the same maintenance window.
  mkdir -p -- "$directory"
  temporary="$(mktemp "${directory}/.juicefs-metadata.XXXXXX")"
  trap 'rm -f -- "$temporary"' EXIT
  juicefs dump --binary "$JUICEFS_META_URL" "$temporary"
  ln -- "$temporary" "$file" # Never overwrite an existing recovery point.
  (cd -- "$directory" && sha256sum -- "$name" > "${name}.sha256")
else
  (cd -- "$directory" && sha256sum -c -s -- "${name}.sha256")
  # JuiceFS load refuses a nonempty metadata database. Do not FLUSHDB or format
  # over an existing filesystem to make restoration succeed.
  juicefs load --binary "$JUICEFS_META_URL" "$file"
fi
