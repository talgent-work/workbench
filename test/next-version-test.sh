#!/usr/bin/env bash
# The release bump follows only the pull request's head branch prefix; a
# hotfix branch whose name contains "release" is still a patch (v0.19.0 was
# released from hotfix/release-only-own-merges instead of v0.18.4).
set -euo pipefail

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
git_() { git -C "$repo" -c user.name=t -c user.email=t@t -c commit.gpgsign=false "$@"; }
next() { (cd "$repo" && bash "$root_dir/scripts/ci/next-version.sh" "$1"); }
expect() {
  local got
  got=$(next "$1")
  [ "$got" = "$2" ] || { echo "$1: expected $2, got $got" >&2; exit 1; }
}

git_ init -q -b master
git_ commit -q --allow-empty -m base
expect hotfix/fix v0.0.1

# Version order, not text order, picks the latest; other tags are ignored.
for tag in v0.9.0 v0.18.1 v0.18.3 v0.2.7 marketplace-v9.9.9 v1.0.0-rc1; do git_ tag -a "$tag" -m "$tag"; done

expect hotfix/release-only-own-merges v0.18.4
expect fix/released-flag v0.18.4
expect dependabot/npm_and_yarn/release-it-1.0 v0.18.4
expect release/mailbox-causal-chain v0.19.0
expect release-283 v0.19.0
expect breaking/api v1.0.0
expect major-rewrite v1.0.0
expect hotfix/major-cleanup v0.18.4
echo "next-version: ok"
