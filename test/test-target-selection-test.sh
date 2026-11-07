#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local body="$1"
  local needle="$2"
  local label="$3"
  if ! grep -Fqx "${needle}" <<<"${body}"; then
    fail "${label}: expected ${needle}"
  fi
}

assert_not_contains() {
  local body="$1"
  local needle="$2"
  local label="$3"
  if grep -Fqx "${needle}" <<<"${body}"; then
    fail "${label}: unexpected ${needle}"
  fi
}

unit_packages="$(make -s --no-print-directory -C "${ROOT_DIR}" print-unit-packages)"
integration_packages="$(make -s --no-print-directory -C "${ROOT_DIR}" print-integration-packages)"
grouped_packages="$(for group in agent storage; do make -s --no-print-directory -C "${ROOT_DIR}" print-integration-packages INTEGRATION_GROUP="${group}"; done)"
[[ "$(sort <<<"${integration_packages}")" == "$(sort <<<"${grouped_packages}")" ]] || fail "Selectable groups must partition the full integration suite without omissions or duplicates"
if make -s --no-print-directory -C "${ROOT_DIR}" print-integration-packages INTEGRATION_GROUP=unknown >/dev/null 2>&1; then
  fail "unknown integration group must not silently run zero tests"
fi

# Intent: the default unit target runs every code-level test, the entrypoints'
# composition checks included (a service that cannot be assembled must fail
# before release), and leaves out generated contracts.
assert_contains "${unit_packages}" "github.com/talgent-work/workbench/cmd/controlplane" "entrypoint composition coverage"
assert_not_contains "${unit_packages}" "github.com/talgent-work/workbench/internal/business/foundry/infrastructure/buildkit" "unit package selection"
assert_not_contains "${unit_packages}" "github.com/talgent-work/workbench/internal/business/foundry/infrastructure/kubernetes" "unit package selection"
assert_not_contains "${unit_packages}" "github.com/talgent-work/workbench/internal/business/foundry/infrastructure/oci" "unit package selection"
assert_contains "${unit_packages}" "github.com/talgent-work/workbench/internal/business/agent/domain" "unit package selection"

# Intent: the default integration target should keep storage adapter integration
# coverage but leave deployment-scale Kubernetes and full pipeline checks out.
assert_contains "${integration_packages}" "./internal/business/agent" "Agent private module integration coverage"
assert_contains "${integration_packages}" "./internal/business/agent/infrastructure" "Agent persistent lifecycle integration coverage"
assert_contains "${integration_packages}" "./internal/business/trace/infrastructure/clickhouse" "integration package selection"
assert_contains "${integration_packages}" "./internal/business/marketplace/infrastructure" "personal plugin API and native catalog integration coverage"
assert_contains "${integration_packages}" "./internal/business/marketplace/infrastructure/persistence" "authorization cancellation and maintenance persistence coverage"
assert_contains "${integration_packages}" "./internal/business/dispatcher" "native plugin feedback and capability integration coverage"
assert_contains "${integration_packages}" "./internal/business/dispatcher/infrastructure/unifiedmessagelog" "native message history persistence integration coverage"
assert_contains "${integration_packages}" "./internal/pkg/taskqueue" "production taskqueue integration coverage"
assert_contains "${integration_packages}" "./internal/business/agent/infrastructure/turnresults" "Work turn result query integration coverage"
assert_not_contains "${integration_packages}" "./internal/pkg/kafka" "unit-only package"
assert_not_contains "${integration_packages}" "./internal/pkg/integrationmessage" "unit-only package"
assert_not_contains "${integration_packages}" "./cmd/controlplane" "entrypoints have no tests"
assert_not_contains "${integration_packages}" "./internal/business/foundry/application" "integration package selection"
assert_not_contains "${integration_packages}" "./internal/business/foundry/infrastructure/kubernetes" "integration package selection"

assert_not_contains "${integration_packages}" "./cmd/migrate-work-waiting" "obsolete migration tests"

echo "test target selection passed"
