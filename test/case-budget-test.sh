#!/usr/bin/env bash
# A case that hangs until go test's -timeout never ends with its own result;
# the goroutines the timeout reports must still reach the log.
set -euo pipefail

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
budget() { python3 "$root_dir/scripts/test/case-budget.py" 3; }

out=$(budget <<'JSON' || true
{"Action":"run","Package":"p","Test":"TestFine"}
{"Action":"output","Package":"p","Test":"TestFine","Output":"fine output\n"}
{"Action":"pass","Package":"p","Test":"TestFine","Elapsed":0}
{"Action":"run","Package":"p","Test":"TestHangs"}
{"Action":"output","Package":"p","Test":"TestHangs","Output":"panic: test timed out after 10m0s\n"}
{"Action":"output","Package":"p","Test":"TestHangs","Output":"hang_test.go:9 +0x57\n"}
{"Action":"output","Package":"p","Output":"FAIL\tp\t600.000s\n"}
{"Action":"fail","Package":"p","Elapsed":600}
JSON
)
grep -q 'panic: test timed out' <<<"$out" || { echo "case-budget: the hung case's timeout report is missing: $out" >&2; exit 1; }
grep -q 'hang_test.go:9' <<<"$out" || { echo "case-budget: the hung case's goroutines are missing: $out" >&2; exit 1; }
if grep -q 'fine output' <<<"$out"; then echo "case-budget: a passing case's output was printed: $out" >&2; exit 1; fi
if budget >/dev/null <<<'{"Action":"fail","Package":"p","Elapsed":1}'; then echo "case-budget: a failed package exited 0" >&2; exit 1; fi
echo "case-budget: ok"
