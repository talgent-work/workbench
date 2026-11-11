#!/usr/bin/env bash
# The pre-push hook checks the commits being pushed, not the working tree: a
# pushed leak is refused even when the working tree no longer has it, and a
# clean push passes even when the working tree has one.
set -euo pipefail

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
git_() { git -C "$repo" -c user.name=t -c user.email=t@t -c commit.gpgsign=false "$@"; }
zero=0000000000000000000000000000000000000000
# An extension's name, spelled apart so this file does not leak it.
leak="orch""estra"

# A repository that is a public tree: the checker, its manifest and one core file.
mkdir -p "$repo/scripts/public-tree" "$repo/.githooks"
cp "$root_dir/scripts/public-tree/check-leaks.py" "$repo/scripts/public-tree/"
cp "$root_dir/.githooks/pre-push" "$repo/.githooks/"
printf 'public scripts\npublic .githooks\ncore README.md\n' > "$repo/scripts/public-tree/manifest"
echo "A neutral readme." > "$repo/README.md"
git_ init -q -b master
git_ add -A
git_ commit -qm clean
clean=$(git_ rev-parse HEAD)
echo "Stop $leak first." > "$repo/README.md"
git_ commit -qam leak
leaked=$(git_ rev-parse HEAD)

output="$repo.out"
trap 'rm -rf "$repo" "$output"' EXIT
push() {
  (cd "$repo" && printf 'refs/heads/master %s refs/heads/master %s\n' "$1" "$zero" |
    env -u TALGENT_REQUIRE_SECRET_SCAN TALGENT_LEAK_PATTERNS_FILE=/nonexistent bash .githooks/pre-push) >"$output" 2>&1
}

# The working tree is clean, the pushed commit is not.
git_ checkout -q "$clean" -- README.md
if push "$leaked" || ! grep -q "README.md:1: extension term: $leak" "$output"; then
  echo "FAIL: a pushed commit with a leak was not refused for it while the working tree is clean" >&2
  cat "$output" >&2
  exit 1
fi

# The working tree leaks, the pushed commit does not.
echo "Ask $leak." > "$repo/untracked.md"
echo "Ask $leak." > "$repo/README.md"
if ! push "$clean"; then
  echo "FAIL: a clean pushed commit was refused because of the working tree" >&2
  cat "$output" >&2
  exit 1
fi

# A commit without the checker cannot be checked, so it is refused.
git_ rm -q -r scripts/public-tree
git_ commit -qm unchecked
git_ checkout -q HEAD~1 -- scripts/public-tree
if push "$(git_ rev-parse HEAD)" || ! grep -q "refused unchecked" "$output"; then
  echo "FAIL: a pushed commit without the checker was not refused" >&2
  cat "$output" >&2
  exit 1
fi

# Deleting a remote branch pushes no commit.
if ! (cd "$repo" && printf '(delete) %s refs/heads/old %s\n' "$zero" "$leaked" |
  env -u TALGENT_REQUIRE_SECRET_SCAN TALGENT_LEAK_PATTERNS_FILE=/nonexistent bash .githooks/pre-push) >/dev/null 2>&1; then
  echo "FAIL: deleting a branch was refused" >&2
  exit 1
fi

if [[ "$(git_ worktree list | wc -l)" -ne 1 ]]; then
  echo "FAIL: the hook left a worktree behind" >&2
  exit 1
fi
# The root README may name the extension's product, and nothing else of it.
product="Talgent ${leak^}"
check() { (cd "$repo" && env -u TALGENT_REQUIRE_SECRET_SCAN TALGENT_LEAK_PATTERNS_FILE=/nonexistent python3 scripts/public-tree/check-leaks.py --skip-secret-scan) >"$output" 2>&1; }
echo "Discover $product." > "$repo/README.md"
if ! check; then
  echo "FAIL: the root README naming the extension's product was refused" >&2
  cat "$output" >&2
  exit 1
fi
echo "Discover $product and its ${leak^}." > "$repo/README.md"
if check || ! grep -q "README.md:1: extension term: ${leak^}" "$output"; then
  echo "FAIL: the root README's bare extension term passed beside the product name" >&2
  cat "$output" >&2
  exit 1
fi
mkdir -p "$repo/docs"
printf 'public scripts\npublic .githooks\ncore README.md\ncore docs\n' > "$repo/scripts/public-tree/manifest"
echo "A neutral readme." > "$repo/README.md"
echo "Discover $product." > "$repo/docs/guide.md"
git_ add docs/guide.md
if check || ! grep -q "docs/guide.md:1: extension term: ${leak^}" "$output"; then
  echo "FAIL: the extension's product name passed outside the root README" >&2
  cat "$output" >&2
  exit 1
fi
git_ rm -q --cached docs/guide.md
rm -rf "$repo/docs"
git_ checkout -q -- scripts/public-tree/manifest README.md

echo "pre-push leak check: ok"
