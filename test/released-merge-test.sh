#!/usr/bin/env bash
# Only a pull request whose own merge lands on master's first-parent history is
# released; one GitHub marks merged because another branch carried its commits
# into master is not (v0.18.2 was released from such an intermediate commit).
set -euo pipefail

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
# Thousands of commits would start a detached auto-gc that still writes into
# .git while the EXIT trap removes it.
git_() { git -C "$repo" -c user.name=t -c user.email=t@t -c commit.gpgsign=false -c gc.auto=0 -c maintenance.auto=false "$@"; }
commit() { echo "$1" > "$repo/$1"; git_ add "$1"; git_ commit -qm "$1"; git_ rev-parse HEAD; }

git_ init -q -b master
commit base >/dev/null
# Releases sit at the top of a long history, as on the real master: the
# lookup must not fail when it stops reading early.
for i in $(seq 1 3000); do git_ commit -q --allow-empty -m "history $i"; done
# A dependency branch, merged into a hotfix branch, which is then merged into master.
git_ checkout -qb dependency
commit dependency >/dev/null
git_ checkout -q master
git_ checkout -qb hotfix
commit fix >/dev/null
git_ merge -q --no-ff --no-edit dependency
carried=$(git_ rev-parse HEAD)
git_ checkout -q master
git_ merge -q --no-ff --no-edit hotfix
merged=$(git_ rev-parse HEAD)
# A squash merge lands a single commit on master.
squashed=$(commit squashed)

check() { (cd "$repo" && bash "$root_dir/scripts/ci/released-merge.sh" "$1" master); }
check "$merged" || { echo "a pull request merged into master must be released" >&2; exit 1; }
check "$squashed" || { echo "a squash merge must be released" >&2; exit 1; }
if check "$carried"; then echo "a pull request carried in by another branch must not be released" >&2; exit 1; fi
if check 0000000000000000000000000000000000000000; then echo "an unknown commit must not be released" >&2; exit 1; fi
echo "released-merge: ok"
