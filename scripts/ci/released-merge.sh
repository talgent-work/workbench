#!/usr/bin/env bash
# Usage: released-merge.sh <merge-commit-sha> [branch-ref]
#
# Succeeds only when the pull request's merge commit lies on the branch's
# first-parent history, i.e. the pull request itself was merged there (merge,
# squash or rebase). GitHub also marks a pull request merged when another
# branch carried its commits in; its merge_commit_sha is then an intermediate
# commit of that branch, which must not be released.
set -euo pipefail

sha="$1"
branch="${2:-origin/master}"
# grep reads all input: with -q it would stop early and, under pipefail, the
# interrupted rev-list would fail the lookup.
git rev-list --first-parent "$branch" | grep -x "$sha" >/dev/null
