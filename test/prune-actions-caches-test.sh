#!/usr/bin/env bash
set -euo pipefail

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/bin"
old_sha=1111111111111111111111111111111111111111
new_sha=2222222222222222222222222222222222222222
# Newest first, as the script requests for master Go caches.
cat > "$fixture/caches.json" <<JSON
[
  {"id": 1, "ref": "refs/heads/master", "key": "go-Linux-X64-1.26.6-test-abc-$new_sha"},
  {"id": 2, "ref": "refs/heads/master", "key": "go-Linux-X64-1.26.6-test-abc-$old_sha"},
  {"id": 3, "ref": "refs/heads/master", "key": "go-Linux-X64-1.26.6-integration-test-abc-$old_sha"},
  {"id": 4, "ref": "refs/heads/master", "key": "buildkit-blob-1-sha256:feed"},
  {"id": 5, "ref": "refs/pull/7/merge", "key": "go-Linux-X64-1.26.6-test-abc-$old_sha"},
  {"id": 6, "ref": "refs/pull/7/merge", "key": "buildkit-blob-1-sha256:beef"},
  {"id": 7, "ref": "refs/pull/8/merge", "key": "go-Linux-X64-1.26.6-test-abc-$new_sha"}
]
JSON
cat > "$fixture/bin/gh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  "cache list")
    shift 2
    ref='' key=''
    while [ $# -gt 0 ]; do
      case "$1" in
        --ref) ref=$2; shift ;;
        --key) key=$2; shift ;;
      esac
      shift
    done
    jq --arg ref "$ref" --arg key "$key" '[.[] | select(($ref == "" or .ref == $ref) and (.key | startswith($key)))]' "$PROBE_CACHES" ;;
  "cache delete") echo "$3" >> "$PROBE_DELETED" ;;
  "pr view") [ "$3" = 8 ] && echo OPEN || echo MERGED ;;
  *) echo "unexpected gh $*" >&2; exit 91 ;;
esac
SH
chmod +x "$fixture/bin/gh"
export PATH="$fixture/bin:$PATH" PROBE_CACHES="$fixture/caches.json" PROBE_DELETED="$fixture/deleted" GITHUB_REPOSITORY=owner/repo
: > "$PROBE_DELETED"
bash "$root_dir/scripts/ci/prune-actions-caches.sh" >/dev/null
# Superseded master Go cache and every cache of the closed PR #7 are deleted;
# the newest master entry per job, non-Go master caches and open PR #8 stay.
test "$(sort -n "$PROBE_DELETED" | tr '\n' ' ')" = "2 5 6 "
# The workflow token must allow what the script reads: without pull-requests
# read, `gh pr view` fails and the prune job fails on every master push.
grep -Fq '  pull-requests: read' "$root_dir/.github/workflows/go-cache.yml"
echo "prune-actions-caches: ok"
