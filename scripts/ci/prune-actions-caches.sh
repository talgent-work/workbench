#!/usr/bin/env bash
# Keep the repository's Actions caches under GitHub's size limit, where
# eviction would otherwise drop the master Go cache that pull requests restore.
#
# - master Go caches: keep the newest entry per key prefix (the key ends in the
#   commit SHA, so every master run saves a new entry).
# - pull request caches: delete every cache of a PR that is no longer open;
#   no later run can restore them.
#
# Requires gh authenticated with actions:write and pull-requests:read, and
# GITHUB_REPOSITORY.
set -euo pipefail

repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"

delete_cache() {
  echo "Deleting cache $1 ($2)"
  gh cache delete "$1" --repo "$repo"
}

# Cache keys look like go-<os>-<arch>-<go>-<job>-<go.sum hash>-<sha>.
gh cache list --repo "$repo" --ref refs/heads/master --key go- --sort created_at --order desc --limit 1000 --json id,key \
  | jq -r '.[] | [.id, .key, (.key | sub("-[0-9a-f]{40}$"; ""))] | @tsv' \
  | awk -F'\t' 'seen[$3]++ { print $1 "\t" $2 }' \
  | while IFS=$'\t' read -r id key; do delete_cache "$id" "$key"; done

gh cache list --repo "$repo" --limit 1000 --json ref \
  | jq -r '[.[].ref | capture("^refs/pull/(?<n>[0-9]+)/merge$").n] | unique | .[]' \
  | while read -r number; do
      state="$(gh pr view "$number" --repo "$repo" --json state --jq .state)"
      [ "$state" = "OPEN" ] && continue
      echo "PR #$number is $state; deleting its caches"
      gh cache list --repo "$repo" --ref "refs/pull/$number/merge" --limit 1000 --json id,key \
        | jq -r '.[] | [.id, .key] | @tsv' \
        | while IFS=$'\t' read -r id key; do delete_cache "$id" "$key"; done
    done
