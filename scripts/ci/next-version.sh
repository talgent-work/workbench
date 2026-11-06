#!/usr/bin/env bash
# Usage: next-version.sh <pull-request-head-branch>
#
# Prints the release tag a pull request merged into master receives: the
# highest vX.Y.Z tag bumped by the head branch's prefix.
#   release/, release-                          minor
#   breaking/, breaking-, major/, major-        major
#   anything else (hotfix/, dependabot/, ...)   patch
# Only the branch prefix decides. A bump read from the merge message matched
# "release" anywhere in it, so hotfix/release-only-own-merges released v0.19.0
# instead of v0.18.4.
set -euo pipefail

branch="$1"
latest=$(git tag --list 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)
IFS=. read -r major minor patch <<<"${latest#v}"
major=${major:-0} minor=${minor:-0} patch=${patch:-0}

case "$branch" in
release/* | release-*) echo "v${major}.$((minor + 1)).0" ;;
breaking/* | breaking-* | major/* | major-*) echo "v$((major + 1)).0.0" ;;
*) echo "v${major}.${minor}.$((patch + 1))" ;;
esac
