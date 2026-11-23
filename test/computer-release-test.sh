#!/usr/bin/env bash
# @talgent/computer is published only at releases that change it, so a platform
# release deploys the newest Talgent Computer npm has at or below it: in version
# order, never a later one, and never one of the 0.30.x releases published
# before the platform's own versions began.
set -euo pipefail

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
registry=$(mktemp -d)
trap 'rm -rf "$registry"' EXIT
# shellcheck source=../scripts/deploy/lib/computer-release.sh
source "$root_dir/scripts/deploy/lib/computer-release.sh"
mkdir -p "$registry/@talgent"
cat > "$registry/@talgent/computer" <<'JSON'
{"name":"@talgent/computer","dist-tags":{"latest":"0.4.12"},"modified":"2026-11-23T03:00:00.000Z",
 "versions":{"0.30.2":{"dist":{"tarball":"x"}},"0.4.8":{"dependencies":{"@anthropic-ai/claude-code":"2.1.323"}},"0.4.12":{"dist":{}},"0.4.2":{}}}
JSON
export TALGENT_NPM_REGISTRY="file://$registry"
expect() {
  local got
  got=$(computer_release "$1" 2>/dev/null)
  [ "$got" = "$2" ] || { echo "platform $1: expected Talgent Computer $2, got $got" >&2; exit 1; }
}

expect 0.4.8 0.4.8
expect 0.4.9 0.4.8
expect 0.4.11 0.4.8
expect 0.4.12 0.4.12
expect 0.5.0 0.4.12
if computer_release 0.4.1 >/dev/null 2>&1; then echo "a platform release below every Computer release must fail" >&2; exit 1; fi

TALGENT_COMPUTER_RELEASE=0.4.2 expect 0.4.12 0.4.2
if TALGENT_COMPUTER_RELEASE=v0.4.2 computer_release 0.4.12 >/dev/null 2>&1; then echo "TALGENT_COMPUTER_RELEASE takes X.Y.Z only" >&2; exit 1; fi
if TALGENT_NPM_REGISTRY="file://$registry/missing" computer_release 0.4.9 >/dev/null 2>&1; then echo "an unreadable registry must fail" >&2; exit 1; fi
echo "computer release: ok"
