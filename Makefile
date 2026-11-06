.PHONY: generate check-generate check-breaking build test test-compile test-integration test-native test-native-computer test-integration-compile print-unit-packages print-integration-packages lint clean pnpm-install gen-ts-rebuild gen-ts-test-runtime frontend-typecheck frontend-test frontend-build frontend-lint check-leaks install-hooks

UNIT_TEST_DIRS = $(shell find . -name '*_test.go' \
	-not -path './cmd/*' \
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
	./pkg/taskqueue

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
	@if [ -n "$$(git diff gen/)" ]; then \
		echo "ERROR: gen/ is out of sync with proto/. Run 'make generate' and commit the result."; \
		git diff --stat gen/; \
		exit 1; \
	fi

# Every contract package except the task ones only grows within its version:
# compared with BREAKING_AGAINST, a buf input such as the previous release tag
# (".git#tag=v0.1.0").
check-breaking:
	test -n "$(BREAKING_AGAINST)"
	buf breaking --against '$(BREAKING_AGAINST)' $(foreach dir,$(shell find proto -type d -name task),--exclude-path $(dir))

# Build all services
build:
	go build ./cmd/dispatcher
	go build ./cmd/controlplane
	go build ./cmd/streamgateway
	go build ./cmd/computer-gateway

# Run tests
test:
	bash test/test-target-selection-test.sh
	bash test/docker-build-workflow-test.sh
	bash test/prune-actions-caches-test.sh
	bash test/released-merge-test.sh
	bash test/next-version-test.sh
	bash test/sandbox-dockerfile-test.sh
	bash test/marketplace-sync-test.sh
	TALGENT_TEST_SERVICES=nats bash scripts/test/with-middleware.sh go test -p=4 $(UNIT_TEST_FLAGS) $(UNIT_PACKAGES)

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
# Optional native runtime cases also require their binaries.
test-integration:
	bash scripts/test/with-middleware.sh go test $(INTEGRATION_TEST_FLAGS) -count=1 -p=4 -timeout=10m $(INTEGRATION_PACKAGES)

# The packages whose cases drive the real Claude Code and execd. CI runs those
# cases, named TestNative (pkg/nativetest), as the unprivileged user the
# sandbox runs them as, after the root run above skipped them; the packages'
# other cases ran there already.
NATIVE_INTEGRATION_PACKAGES := ./internal/business/agent/infrastructure/execution ./internal/business/dispatcher \
	./internal/business/trace/infrastructure/clickhouse ./internal/pkg/execd

test-native:
	test -n "$$CLAUDE_BINARY" && test -n "$$EXECD_BINARY"
	bash scripts/test/with-middleware.sh go test $(INTEGRATION_TEST_FLAGS) -count=1 -p=4 -timeout=10m -run '^TestNative' $(NATIVE_INTEGRATION_PACKAGES)

# The Dispatcher's native cases again, through the Computer Gateway against a
# real Talgent Computer (apps/computer, built), as a Managed Computer runs them.
test-native-computer:
	test -n "$$CLAUDE_BINARY" && test -n "$$TALGENT_COMPUTER_PROGRAM"
	bash scripts/test/with-middleware.sh go test $(INTEGRATION_TEST_FLAGS) -count=1 -timeout=10m -run '^TestNative' ./internal/business/dispatcher

pnpm-install:
	pnpm install --frozen-lockfile

gen-ts-rebuild:
	pnpm --filter @talgent-work/workbench-contracts run rebuild

gen-ts-test-runtime:
	pnpm --filter @talgent-work/workbench-contracts run test:runtime

frontend-typecheck:
	pnpm --filter @talgent-work/workbench-contracts run build
	pnpm -r --parallel --stream --filter '!@talgent-work/workbench-contracts' run typecheck

frontend-test:
	pnpm -r --parallel --stream run test

frontend-build:
	pnpm --filter @talgent-work/workbench-contracts run build
	pnpm -r --parallel --stream --filter '!@talgent-work/workbench-contracts' run build

frontend-lint:
	pnpm --filter @talgent-work/workbench-ui --filter @talgent-work/workbench-web --filter @talgent-work/computer run lint

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
