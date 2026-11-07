#!/usr/bin/env python3
"""Fails a Go test run in which a case took longer than its budget.

Reads `go test -json` on stdin, prints what go test prints without -v (each
package's result and the output of failed cases), and exits 1
when go test failed or any case, top-level or subtest, passed or failed after
more than BUDGET seconds (docs/development.md: a case takes at most 3s).
Go has no per-case timeout; the run's -timeout still bounds a case that hangs.

Usage: go test -json ... | case-budget.py BUDGET
"""
import json
import sys


def main() -> int:
    budget = float(sys.argv[1])
    slow, failed, cases = [], False, {}
    for line in sys.stdin:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            # Build errors and the middleware wrapper's messages are not JSON.
            sys.stdout.write(line)
            continue
        action, test = event.get("Action"), event.get("Test")
        case = (event.get("Package"), test)
        if "Output" in event:
            if test:
                cases.setdefault(case, []).append(event["Output"])
            else:
                sys.stdout.write(event["Output"])
        if action in ("pass", "fail", "skip") and test:
            output = cases.pop(case, [])
            if action == "fail":
                sys.stdout.writelines(output)
        if action == "fail":
            failed = True
        if action in ("pass", "fail") and test and event.get("Elapsed", 0) > budget:
            slow.append((event["Elapsed"], event["Package"], test))
    sys.stdout.flush()
    for elapsed, package, test in sorted(slow, reverse=True):
        print(f"SLOW: {package} {test} took {elapsed:.2f}s, over the {budget:g}s budget", file=sys.stderr)
    return 1 if failed or slow else 0


if __name__ == "__main__":
    sys.exit(main())
