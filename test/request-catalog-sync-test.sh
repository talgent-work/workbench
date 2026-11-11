#!/usr/bin/env bash
set -euo pipefail

# scripts/deploy/request-catalog-sync.sh against stubbed kubectl and curl: it
# starts a synchronization with the agent control token, waits for the run to
# finish within its bound, and never puts the token on a command line.
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/bin"
token=fixture-control-token-0123456789abcdef

cat > "$fixture/bin/kubectl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
echo "kubectl $*" >> "$PROBE_ARGS"
case "$*" in
  *"port-forward svc/controlplane "*":8080") exec sleep 30 ;;
  *"get secret native-execution-control "*) printf '%s' "$PROBE_TOKEN" | base64 -w0 ;;
  *) echo "unexpected kubectl $*" >&2; exit 91 ;;
esac
SH
cat > "$fixture/bin/curl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
echo "curl $*" >> "$PROBE_ARGS"
url='' data=''
while [ $# -gt 0 ]; do
  case "$1" in
    -H) case "$2" in @*) cat "${2#@}" >> "$PROBE_HEADERS" ;; *) echo "$2" >> "$PROBE_HEADERS" ;; esac; shift ;;
    -d|--data) data=$2; shift ;;
    -X|-m|--max-time|-o) shift ;;
    http*) url=$1 ;;
  esac
  shift
done
case "$url" in
  */marketplace.private.v1.SynchronizationService/RequestSynchronization)
    echo "$data" > "$PROBE_REQUESTED"
    echo '{"run":{"id":"run-1","status":"queued"}}' ;;
  */marketplace.private.v1.SynchronizationService/GetSynchronization)
    polls=$(( $(cat "$PROBE_POLLS") + 1 )); echo "$polls" > "$PROBE_POLLS"
    # The first poll proves the port-forward answers; the run finishes on the third.
    if [ "$polls" -lt 3 ] || [ "$PROBE_OUTCOME" = running ]; then echo '{"run":{"id":"run-1","status":"running"}}'
    elif [ "$PROBE_OUTCOME" = succeeded ]; then echo '{"run":{"id":"run-1","status":"succeeded","finishedAtUnixMs":"1"}}'
    else echo '{"run":{"id":"run-1","status":"partial_failure","finishedAtUnixMs":"1","failures":[{"source":"public","slug":"talgent","reason":"unreachable"}]}}'
    fi ;;
  *) echo "unexpected curl $url" >&2; exit 92 ;;
esac
SH
chmod +x "$fixture/bin/kubectl" "$fixture/bin/curl"

run() {
  export PATH="$fixture/bin:$PATH" PROBE_TOKEN="$token" PROBE_OUTCOME="$1" \
    PROBE_ARGS="$fixture/args" PROBE_HEADERS="$fixture/headers" PROBE_REQUESTED="$fixture/requested" PROBE_POLLS="$fixture/polls" \
    CATALOG_SYNC_POLL_SECONDS=0.1 CATALOG_SYNC_TIMEOUT_SECONDS="${2:-5}"
  : > "$PROBE_ARGS"; : > "$PROBE_HEADERS"; : > "$PROBE_REQUESTED"; echo 0 > "$PROBE_POLLS"
  bash "$root_dir/scripts/deploy/request-catalog-sync.sh" > "$fixture/out" 2>&1
}

run succeeded
grep -Fxq "Authorization: Bearer $token" "$fixture/headers"
grep -Fxq "Content-Type: application/json" "$fixture/headers"
test "$(cat "$fixture/requested")" = '{}'
grep -Fq 'succeeded' "$fixture/out"
! grep -Fq "$token" "$fixture/args" || { echo "the token reached a command line" >&2; exit 1; }

# A run that finishes with failures names them and fails the script.
if run partial_failure; then echo "a partial failure passed" >&2; exit 1; fi
grep -Fq 'talgent: unreachable' "$fixture/out"

# A run that never finishes ends the wait at its bound.
if run running 1; then echo "an unfinished run passed" >&2; exit 1; fi
grep -Fq 'did not finish within 1s' "$fixture/out"

echo "request-catalog-sync: ok"
