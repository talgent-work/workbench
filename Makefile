# Recipes pipe go test into scripts/test/case-budget.py and need pipefail.
SHELL := bash

.PHONY: generate check-generate check-breaking build image-binaries test test-scripts test-go test-compile test-integration test-integration-compile print-unit-packages print-integration-packages lint clean pnpm-install frontend-typecheck frontend-test frontend-build frontend-lint check-leaks install-hooks

UNIT_TEST_DIRS = $(shell find . -name '*_test.go' \
	-not -path './gen/*' \
	-not -path '*/node_modules/*' \
	-printf '%h\n' | sort -u)
UNIT_PACKAGES = $(shell go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' $(UNIT_TEST_DIRS) | awk 'NF')

INTEGRATION_GROUP ?= all
INTEGRATION_PACKAGES_agent := \
	./internal/business/agent \
	./internal/business/agent/infrastructure \
	./internal/business/agent/infrastructure/execution \
	./internal/business/agent/infrastructure/turnresults \
	./internal/business/dispatcher \
	./internal/pkg/taskqueue

INTEGRATION_PACKAGES_storage := \
	./internal/business/account \
	./internal/business/dispatcher/infrastructure/unifiedmessagelog \
	./internal/business/marketplace/infrastructure \
	./internal/business/marketplace/infrastructure/persistence \
	./internal/business/storage \
	./internal/business/trace/infrastructure/clickhouse

INTEGRATION_PACKAGES_all := $(INTEGRATION_PACKAGES_agent) $(INTEGRATION_PACKAGES_storage)
INTEGRATION_PACKAGES := $(INTEGRATION_PACKAGES_$(INTEGRATION_GROUP))
ifeq ($(strip $(INTEGRATION_PACKAGES)),)
$(error Unknown INTEGRATION_GROUP: $(INTEGRATION_GROUP))
endif

# The longest a test case may take, in seconds (docs/development.md);
# scripts/test/case-budget.py fails a Go run over it.
CASE_BUDGET_SECONDS := 3

# Flags that select compiled test code. The *-compile targets reuse them so the
# Go build cache they fill on master is the one pull request test runs restore.
UNIT_TEST_FLAGS := -race
INTEGRATION_TEST_FLAGS := -tags=integration -race

# Generate proto code
generate:
	buf generate

# Check whether generated code is in sync with proto definitions
check-generate:
	buf generate
	@if [ -n "$$(git status --porcelain -- gen)" ]; then \
		echo "ERROR: gen/ is out of sync with proto/. Run 'make generate' and commit the result."; \
		git status --short -- gen; \
		exit 1; \
	fi

# A task payload may stay queued across a release, so its contract only grows
# within its version: compared with BREAKING_AGAINST, a buf input such as the
# previous release tag (".git#tag=v0.1.0").
check-breaking:
	test -n "$(BREAKING_AGAINST)"
	buf breaking --against '$(BREAKING_AGAINST)'

# Build all services
build:
	go build ./cmd/dispatcher
	go build ./cmd/controlplane
	go build ./cmd/streamgateway
	go build ./cmd/computer-gateway

# The static binaries of the Go images, in bin/<service>; CI packages them
# into the images, which then compile nothing (BINARY=prebuilt).
IMAGE_SERVICES ?= controlplane dispatcher streamgateway computer-gateway

image-binaries:
	bash scripts/image-binaries.sh bin $(IMAGE_SERVICES)

test: test-scripts test-go

# CI, build, release and deployment script tests; CI runs them beside the Go tests.
test-scripts:
	bash test/test-target-selection-test.sh
	bash test/docker-build-workflow-test.sh
	bash test/prune-actions-caches-test.sh
	bash test/released-merge-test.sh
	bash test/next-version-test.sh
	bash test/sandbox-dockerfile-test.sh
	bash test/marketplace-sync-test.sh
	bash test/pre-push-leak-check-test.sh
	bash test/k3s-render-test.sh

test-go:
	set -o pipefail; TALGENT_TEST_SERVICES=nats bash scripts/test/with-middleware.sh go test -json -p=4 $(UNIT_TEST_FLAGS) $(UNIT_PACKAGES) \
		| python3 scripts/test/case-budget.py $(CASE_BUDGET_SECONDS)

# Compile, without running, the test binaries of test / test-integration to
# seed the Go build cache (.github/workflows/go-cache.yml). Packages compile
# one by one because several share a base name.
test-compile:
	printf '%s\n' $(UNIT_PACKAGES) | xargs -P4 -n1 go test -c -o /dev/null $(UNIT_TEST_FLAGS)

test-integration-compile:
	printf '%s\n' $(INTEGRATION_PACKAGES) | xargs -P4 -n1 go test -c -o /dev/null $(INTEGRATION_TEST_FLAGS)

print-unit-packages:
	@printf '%s\n' $(UNIT_PACKAGES)

print-integration-packages:
	@printf '%s\n' $(INTEGRATION_PACKAGES)

# One middleware set and initialized schema pool shared by up to four packages.
# The test binaries compile while the middleware starts; the tests wait for
# that compilation (the process $$0) instead of compiling the same packages
# beside it.
test-integration:
	set -o pipefail; $(MAKE) --no-print-directory test-integration-compile & \
	bash scripts/test/with-middleware.sh bash -c 'while kill -0 "$$0" 2>/dev/null; do sleep 1; done; exec "$$@"' "$$!" \
		go test -json $(INTEGRATION_TEST_FLAGS) -count=1 -p=4 -timeout=10m $(INTEGRATION_PACKAGES) \
		| python3 scripts/test/case-budget.py $(CASE_BUDGET_SECONDS)

pnpm-install:
	pnpm install --frozen-lockfile

frontend-typecheck:
	pnpm -r --parallel --stream run typecheck

frontend-test:
	pnpm -r --parallel --stream run test

frontend-build:
	pnpm -r --parallel --stream run build

frontend-lint:
	pnpm --filter @talgent/workbench-ui --filter @talgent/workbench-web --filter @talgent/computer run lint

# lint
lint:
	buf lint
	go vet ./...

# What the public Workbench tree must not carry (scripts/public-tree/manifest);
# gitleaks scans the public tree as the working tree holds it, when installed.
check-leaks:
	python3 scripts/public-tree/check-leaks.py

# Runs check-leaks before every push.
install-hooks:
	git config core.hooksPath .githooks

# Clean build artifacts
clean:
	rm -f controlplane dispatcher streamgateway computer-gateway
	rm -rf bin
