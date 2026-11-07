#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
WORKFLOW="${ROOT_DIR}/.github/workflows/image-build.yml"
PUBLISH="${ROOT_DIR}/.github/workflows/image-publish.yml"
CI="${ROOT_DIR}/.github/workflows/ci.yml"
BUILD_SCRIPT="${ROOT_DIR}/scripts/deploy/build-and-push-images.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local needle="$1"
  local label="$2"
  if ! grep -Fq "${needle}" "${WORKFLOW}"; then
    fail "${label}: expected ${needle}"
  fi
}

assert_publishes() {
  local needle="$1"
  local label="$2"
  if ! grep -Fq "${needle}" "${PUBLISH}"; then
    fail "${label}: expected ${needle}"
  fi
}

assert_file_not_contains() {
  local path="$1"
  local needle="$2"
  local label="$3"
  if grep -Fq "${needle}" "${path}"; then
    fail "${label}: unexpected ${needle}"
  fi
}

# Merge publication must not be suppressed by a PR head's [skip ci] marker.
if ! grep -Fq '  pull_request_target:' "${ROOT_DIR}/.github/workflows/auto-tag.yml"; then
  fail "merged PR release must use pull_request_target"
fi
# Only a PR merged into master itself is released, not one another branch carried in.
for gate in 'bash scripts/ci/released-merge.sh "$MERGE_SHA" origin/master' "    if: steps.merged.outputs.release == 'true'"; do
  if ! grep -Fq "$gate" "${ROOT_DIR}/.github/workflows/auto-tag.yml"; then
    fail "merged PR release must check its own merge: $gate"
  fi
done
for trigger in '    branches-ignore: [master]' '  workflow_dispatch:'; do
  if ! grep -Fq "$trigger" "${ROOT_DIR}/.github/workflows/ci.yml"; then
    fail "CI must retain branch pushes and manual dispatch: $trigger"
  fi
done
assert_file_not_contains "${ROOT_DIR}/.github/workflows/ci.yml" '  pull_request:' "PR creation must not trigger CI"
if grep -Eq '^  (push|pull_request|workflow_dispatch):' "${WORKFLOW}" "${PUBLISH}"; then
  fail "image workflows must only run through their callers"
fi
# Selection runs beside the checks and publishes nothing; publication waits for
# every check, so a failing check publishes no image and advances no baseline.
if ! grep -Fq '    needs: [generate, image-baseline]' "${CI}" ||
   ! grep -Fq '    needs: [images, test, frontend, integration-test, breaking]' "${CI}" ||
   ! grep -Fq '    needs: publish' "${CI}"; then
  fail "images publish only after all checks, and the baseline only after they publish"
fi
assert_file_not_contains "${WORKFLOW}" 'packages: write' "selection cannot publish"
# The Go and web images compile nothing: CI's test and frontend jobs build what
# they take, a release builds it in Image Build, both with the same make targets.
for build in 'run: make image-binaries' 'run: make frontend-build'; do
  grep -Fq "$build" "${CI}" || fail "CI's checks build the images' inputs: $build"
  grep -Fq "$build" "${WORKFLOW}" || fail "a release builds the images' inputs: $build"
done
grep -Fq "if: inputs.compile && needs.detect-changes.outputs.go == 'true'" "${WORKFLOW}" || fail "a release compiles the selected Go images"
grep -Fq "if: inputs.compile && needs.detect-changes.outputs.web == 'true'" "${WORKFLOW}" || fail "a release builds the selected web app"
grep -Fq '      compile: false' "${CI}" || fail "CI takes what its checks built"
assert_publishes 'args=(--build-arg BINARY=prebuilt --build-arg DIST=prebuilt' "publication packages prebuilt inputs"
assert_publishes 'docker push "$image"' "publication pushes each image"
for dockerfile in Dockerfile deploy/controlplane/Dockerfile; do
  grep -Fq 'FROM ${BINARY} AS binary' "${ROOT_DIR}/${dockerfile}" || fail "${dockerfile} takes the CI binary"
  grep -Fq 'bash scripts/image-binaries.sh /out' "${ROOT_DIR}/${dockerfile}" || fail "${dockerfile} compiles from source as CI does"
done
grep -Fq 'FROM ${DIST} AS dist' "${ROOT_DIR}/apps/workbench-web/Dockerfile" || fail "the web image takes the CI build"
assert_publishes "if: contains(inputs.matrix, '\"name\":\"execd\"')" "execd's shared HOME bootstrap is verified before it is published"

# Native Sandbox is a released workload image. No Executor base-image chain remains.
assert_publishes 'build-args: SANDBOX_BASE_IMAGE=${{ needs.sandbox-base.outputs.image }}' "sandbox environment digest"
assert_file_not_contains "${BUILD_SCRIPT}" "talgent-executor" "retired Executor image build"
assert_contains 'sandbox_required: ${{ steps.changed.outputs.sandbox_required }}' "sandbox output"
assert_publishes 'build-sandbox:' "independent sandbox job"
assert_publishes "if: inputs.sandbox_required == 'true'" "sandbox job condition"
assert_publishes 'file: deploy/sandbox/Dockerfile' "sandbox Dockerfile build"
assert_publishes 'tags: ghcr.io/talgent-work/sandbox:${{ inputs.tag }}' "sandbox image tag"
assert_file_not_contains "${PUBLISH}" '    needs: [sandbox-base, build-sandbox]' "service images must not wait for sandbox"
assert_file_not_contains "${PUBLISH}" 'needs.build-sandbox.result' "service images must not depend on sandbox results"


# Intent: the caller accepts sandbox as a selectable service, so service
# validation must accept it even though it is not under cmd/ or apps/.
assert_contains '[ "$svc" = "sandbox" ]' "sandbox service validation"
assert_contains 'add_sandbox' "sandbox service selection"

# The owned Go deployment topology is exactly four services: controlplane and
# its runtime services, the Computer Gateway among them. Modules merged into a
# process must not receive independently selectable images.
assert_contains 'GO_SERVICES=(controlplane dispatcher streamgateway computer-gateway)' "Go release topology"
if ! grep -Fq 'services=(controlplane dispatcher streamgateway computer-gateway)' "${BUILD_SCRIPT}"; then
  fail "local image builds must cover the same four services"
fi
# The local build also builds the web app.
if ! grep -Fxq 'build_web_app workbench-web' "${BUILD_SCRIPT}"; then
  fail "the local image build must build the web app"
fi
# Every image is named after its service.
assert_publishes 'image="ghcr.io/talgent-work/${name}:${TAG}"' "service image tag"

# Intent: web image builds no longer receive SPA OIDC provider build arguments;
# browser sign-in is served by Account at runtime.
assert_file_not_contains "${BUILD_SCRIPT}" "VITE_ZITADEL_GITHUB_IDP_ID" "web build oidc idp arg"
assert_file_not_contains "${BUILD_SCRIPT}" "VITE_OIDC_GITHUB_PROVIDER_HINT" "web build oidc provider hint arg"

# Execute the actual matrix script. Only the change list is injected; service
# discovery, dependency expansion and emitted matrix values run as in CI.
export WORKFLOW ROOT_DIR
python3 - <<'PYTHON'
import json, os, shlex, subprocess, tempfile
from pathlib import Path
body = Path(os.environ["WORKFLOW"]).read_text()
a = body.index("        run: |", body.index("      - name: Detect changed services"))
b = body.rindex("\n\n", a, body.index("\n  go-binaries:", a))  # the job after selection and its comment
script = "\n".join(line[10:] for line in body[a:b].splitlines()[1:])
script = script.replace("${{ github.event.repository.default_branch || 'master' }}", "master")
subprocess.run(["bash", "-n"], input=script, text=True, check=True)
for name, build_all, selected, changed, event, expected, sandbox_required in [
    ("full release", "true", "", "", "workflow_call", None, "true"),
    ("sandbox selection", "false", "sandbox", "", "workflow_dispatch", set(), "true"),
    ("dashboard selection", "false", "controlplane,workbench-web", "", "workflow_dispatch", {"controlplane", "workbench-web"}, "false"),
    ("execd selection", "false", "execd", "", "workflow_dispatch", {"execd"}, "false"),
    ("retired Executor", "false", "executor", "", "push", set(), "false"),
    ("plugin authoring only", "false", "", "plugins/talgent-runtime/skills/triaging-inbox/SKILL.md", "push", set(), "false"),
    ("catalog dependency", "false", "", "internal/business/marketplace/infrastructure/catalog_sync.go", "push", {"controlplane"}, "false"),
    ("Trace runs in controlplane", "false", "", "internal/business/trace/infrastructure/clickhouse/repository.go", "push", {"controlplane"}, "false"),
    ("catalog test only", "false", "", "internal/business/marketplace/infrastructure/catalog_sync_integration_test.go", "push", set(), "false"),
    ("Go dependency lock", "false", "", "go.sum", "push", {"controlplane", "dispatcher", "streamgateway", "computer-gateway"}, "false"),
    ("runtime plugin hook", "false", "", "plugins/talgent-runtime/hooks/open-actions.mjs", "push", set(), "false"),
    ("execd bootstrap change", "false", "", "deploy/execd/trust-mitm-ca-nss.sh", "push", {"execd"}, "false"),
    ("execd verification change", "false", "", "deploy/execd/verify-nss-bootstrap.py", "push", {"execd"}, "false"),
    ("Workbench web change", "false", "", "apps/workbench-web/src/app.tsx", "push", {"workbench-web"}, "false"),
    ("workbench-ui feeds the web app", "false", "", "packages/workbench-ui/lib/client.ts", "push", {"workbench-web"}, "false"),
    ("branch already built", "false", "", "", "push", set(), "false"),
    ("Go test only", "false", "", "internal/business/agent/application/command/create_work_test.go", "push", set(), "false"),
    ("move out of an app", "false", "", "apps/workbench-web/index.html\ndocs/logo.svg", "push", {"workbench-web"}, "false"),
    ("generated TS contract", "false", "", "gen/ts/file/v1/file_pb.ts", "push", {"workbench-web"}, "false"),
    ("raw proto is not a TS build input", "false", "", "proto/file/v1/file.proto", "push", set(), "false"),
    ("shared workspace dependencies", "false", "", "pnpm-lock.yaml", "push", {"workbench-web"}, "false"),
    ("Talgent Computer ships through npm, in no image", "false", "", "apps/computer/src/cli.ts", "push", set(), "false"),
    ("a Go service's own deploy directory", "false", "", "deploy/computer-gateway/Dockerfile", "push", {"computer-gateway"}, "false"),
    ("sandbox environment", "false", "", "deploy/sandbox/Dockerfile.base", "push", set(), "true"),
    ("sandbox assembly", "false", "", "deploy/sandbox/Dockerfile", "push", set(), "true"),
    ("build context rules", "false", "", ".dockerignore", "push", {"controlplane", "dispatcher", "streamgateway", "computer-gateway", "workbench-web", "execd"}, "true"),
    ("branch docs only", "false", "", "docs/guide.md", "push", set(), "false"),
    # These run the real substantive filter: the listed files are identical at
    # both revisions, as a comment-only change compiles to the same code.
    ("REAL Go comments only", "false", "", "internal/pkg/taskqueue/runtime.go", "push", set(), "false"),
    ("REAL generated TS comments only", "false", "", "gen/ts/talgent/trace/public/v1/trace_pb.ts", "push", set(), "false"),
    ("REAL hand-written TS is always a change", "false", "", "apps/workbench-web/src/app.tsx", "push", {"workbench-web"}, "false"),
]:
    with tempfile.NamedTemporaryFile() as output:
        env = os.environ | dict(CURRENT_REF="HEAD", IMAGE_TAG="validation", BUILD_ALL=build_all, RELEASE="",
            SELECTED_SERVICES=selected, EVENT_NAME=event, BEFORE_SHA="HEAD~1", PREVIOUS_BUILD_REF="HEAD", GITHUB_OUTPUT=output.name)
        # The path rules are tested on their own; REAL cases run the filter too.
        selection = script if name.startswith("REAL ") else script.replace('go run ./scripts/ci/substantive "$1" "$2"', 'cat')
        assert selection != script or name.startswith("REAL "), "filter call not found"
        prefix = ""
        if changed:
            prefix += 'git() { if [ "$1" = "diff" ]; then test "$2" = "--no-renames" || return 1; printf "%s\\n" '+shlex.quote(changed)+'; elif [ "$1" = "tag" ]; then :; else command git "$@"; fi; }\n'
        result = subprocess.run(["bash", "-e"], input=prefix+selection, text=True,
            cwd=os.environ["ROOT_DIR"], env=env, capture_output=True)
        if selected == "executor":
            assert result.returncode != 0 and "Unknown service 'executor'" in result.stdout, result.stdout+result.stderr
        else:
            assert result.returncode == 0, result.stdout+result.stderr
            values = dict(line.split("=", 1) for line in Path(output.name).read_text().splitlines())
            assert values["sandbox_required"] == sandbox_required, values
            if changed in ("deploy/sandbox/Dockerfile.base", ".dockerignore"):
                assert values["sandbox_base_required"] == "true", values
            elif changed:
                assert values["sandbox_base_required"] == "false", values
            if event == "push":
                assert "Comparing HEAD..HEAD" in result.stdout, result.stdout
            names = {entry["name"] for entry in json.loads(values["matrix"])}
            assert "executor" not in names, names
            # What a release compiles before packaging follows the selection.
            assert values["go"] == str(bool(names & {"controlplane", "dispatcher", "streamgateway", "computer-gateway"})).lower(), (name, values)
            assert values["web"] == str("workbench-web" in names).lower(), (name, values)
            if build_all == "true":
                assert names == {"controlplane", "dispatcher", "streamgateway", "computer-gateway", "workbench-web", "execd"}, names
            if expected is not None:
                assert names == expected, names
        print(name+": PASS")
PYTHON
# A release builds what changed since the previous release and gives every
# other image the new tag from the previous release's image. An image the
# previous release lacks is built instead.
export WORKFLOW ROOT_DIR
python3 - <<'PYTHON'
import json, os, shlex, subprocess, tempfile
from pathlib import Path
body = Path(os.environ["WORKFLOW"]).read_text()
a = body.index("        run: |", body.index("      - name: Detect changed services"))
b = body.rindex("\n\n", a, body.index("\n  go-binaries:", a))  # the job after selection and its comment
script = "\n".join(line[10:] for line in body[a:b].splitlines()[1:])
script = script.replace("${{ github.event.repository.default_branch || 'master' }}", "master")
script = script.replace('go run ./scripts/ci/substantive "$1" "$2"', 'cat')
everything = {"controlplane", "dispatcher", "streamgateway", "computer-gateway", "workbench-web", "execd", "sandbox"}
for name, changed, missing, built, sandbox in [
    ("release of a Workbench web change", "apps/workbench-web/src/app.tsx", "", {"workbench-web"}, "false"),
    ("release of a shared Go change", "internal/pkg/taskqueue/runtime.go", "", {"controlplane", "dispatcher", "streamgateway", "computer-gateway"}, "false"),
    ("release of a Talgent Computer change", "apps/computer/src/cli.ts", "", set(), "false"),
    ("release with nothing to build", "docs/guide.md", "", set(), "false"),
    ("release missing a previous image", "docs/guide.md", "streamgateway:v0.0.1", {"streamgateway"}, "false"),
    ("release missing the previous sandbox", "docs/guide.md", "sandbox:v0.0.1", set(), "true"),
]:
    with tempfile.NamedTemporaryFile() as output:
        env = os.environ | dict(CURRENT_REF="HEAD", IMAGE_TAG="v0.0.2", RELEASE="true", BUILD_ALL="false",
            SELECTED_SERVICES="", EVENT_NAME="pull_request_target",  PREVIOUS_BUILD_REF="", GITHUB_OUTPUT=output.name)
        prefix = ('git() { case "$1" in diff) printf "%s\\n" '+shlex.quote(changed)+';; tag) echo v0.0.1;; cat-file) return 0;; *) command git "$@";; esac; }\n'
            'docker() { case "$*" in *'+(missing or "none")+'*) return 1;; *) return 0;; esac; }\n')
        result = subprocess.run(["bash", "-e"], input=prefix+script, text=True, cwd=os.environ["ROOT_DIR"], env=env, capture_output=True)
        assert result.returncode == 0, result.stdout+result.stderr
        values = dict(line.split("=", 1) for line in Path(output.name).read_text().splitlines())
        names = {entry["name"] for entry in json.loads(values["matrix"])}
        assert names == built, (name, names)
        assert values["sandbox_required"] == sandbox, (name, values)
        assert values["previous_tag"] == "v0.0.1", values
        reused = set(json.loads(values["reuse"]))
        expected_reuse = {name for name in everything - built - ({"sandbox"} if sandbox == "true" else set())}
        assert reused == expected_reuse, (name, reused)
        assert "Comparing v0.0.1..HEAD" in result.stdout, result.stdout
    print(name+": PASS")
PYTHON
python3 "${ROOT_DIR}/test/previous-image-build-test.py"
echo "docker build workflow test passed"
