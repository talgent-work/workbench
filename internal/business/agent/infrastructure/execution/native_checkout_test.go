package execution

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// The preparation process checks the planned repositories out inside the
// Work's environment; each ends checked out or not checked out with
// a safe reason, and the start continues.
func TestPreparationChecksOutPlannedRepositoriesInsideTheWorkspace(t *testing.T) {
	requireNodeAndGit(t)
	remotes := t.TempDir()
	populated := gitRemote(t, remotes, "populated", "main")
	empty := gitRemote(t, remotes, "empty", "")
	plan := []repositoryCheckout{
		{RepositoryID: "github:1", CloneURL: populated, BaseBranch: "main", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/repos/populated"},
		{RepositoryID: "github:2", CloneURL: empty, BaseBranch: "main", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/repos/empty"},
		{RepositoryID: "github:3", CloneURL: "file://" + filepath.Join(remotes, "missing.git"), BaseBranch: "main", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/repos/missing"},
		{RepositoryID: "github:4", CloneURL: populated, BaseBranch: "release", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/repos/no-branch"},
		{RepositoryID: "github:5", CloneURL: populated, BaseBranch: "main", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/elsewhere"},
	}
	workspace := t.TempDir()
	report := runPreparation(t, workspace, fixtureAllocationID, plan, "600")
	require.Equal(t, []domain.RepositoryCheckout{
		{RepositoryID: "github:1", CheckedOut: true},
		{RepositoryID: "github:2", CheckedOut: true},
		{RepositoryID: "github:3", Reason: domain.CheckoutUnavailable},
		{RepositoryID: "github:4", Reason: "branch_unavailable"},
		{RepositoryID: "github:5", Reason: "target_conflict"},
	}, report.checkouts(plan, domain.CheckoutIncomplete))
	require.Equal(t, "talgent/TAL-1", gitIn(t, filepath.Join(workspace, "repos", "populated"), "rev-parse", "--abbrev-ref", "HEAD"))
	require.Equal(t, "base\n", readFile(t, filepath.Join(workspace, "repos", "populated", "README.md")))
	require.Equal(t, "refs/heads/talgent/TAL-1", gitIn(t, filepath.Join(workspace, "repos", "empty"), "symbolic-ref", "HEAD"), "an empty repository starts its work branch unborn")
	require.NoDirExists(t, filepath.Join(workspace, "repos", "missing"))
	staged, err := os.ReadDir(filepath.Join(workspace, ".talgent", "checkouts"))
	require.NoError(t, err)
	require.Empty(t, staged, "a failed clone never leaves a partial repository")

	// Resume and Restart keep the Work's checkout: its branch, index and
	// working tree are never reset.
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "repos", "populated", "README.md"), []byte("edited by the Agent\n"), 0o600))
	again := runPreparation(t, workspace, "01900000-0000-7000-8000-000000000002", plan[:1], "600")
	require.Equal(t, []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}}, again.checkouts(plan[:1], domain.CheckoutIncomplete))
	require.Equal(t, "edited by the Agent\n", readFile(t, filepath.Join(workspace, "repos", "populated", "README.md")))
}

func TestCheckoutStillPendingAtThePreparationBoundIsTimedOut(t *testing.T) {
	requireNodeAndGit(t)
	plan := []repositoryCheckout{{RepositoryID: "github:1", CloneURL: gitRemote(t, t.TempDir(), "large", "main"), BaseBranch: "main", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/repos/large"}}
	report := runPreparation(t, t.TempDir(), fixtureAllocationID, plan, "0")
	require.Equal(t, []domain.RepositoryCheckout{{RepositoryID: "github:1", Reason: domain.CheckoutTimedOut}}, report.checkouts(plan, domain.CheckoutTimedOut))
	// Ended by timeout before its report, a repository still checking out
	// timed out; another early end leaves it incomplete.
	require.Equal(t, domain.CheckoutTimedOut, unfinishedCheckout(timeoutExitCode))
	require.Equal(t, domain.CheckoutTimedOut, unfinishedCheckout(timeoutKilledExitCode))
	require.Equal(t, domain.CheckoutIncomplete, unfinishedCheckout(1))
	foreign := preparationReport{Repositories: []checkoutReport{{RepositoryID: "github:1", Reason: "fatal: could not read Username"}}}
	require.Equal(t, domain.CheckoutUnavailable, foreign.checkouts(plan, domain.CheckoutTimedOut)[0].Reason, "an unknown reason is never shown and never blocks the start")
}

// On shared storage every file write waits for its upload, so a repository
// large enough for git's parallel checkout is written by several workers.
func TestPreparationWritesLargeCheckoutsInParallel(t *testing.T) {
	requireNodeAndGit(t)
	remotes := t.TempDir()
	bare, work := filepath.Join(remotes, "large.git"), filepath.Join(remotes, "large-work")
	gitIn(t, remotes, "init", "--quiet", "--bare", bare)
	gitIn(t, remotes, "init", "--quiet", "-b", "main", work)
	// git parallelizes a checkout of at least 100 files by default.
	for i := range 120 {
		require.NoError(t, os.WriteFile(filepath.Join(work, fmt.Sprintf("file-%d.txt", i)), []byte("content\n"), 0o600))
	}
	gitIn(t, work, "add", ".")
	gitIn(t, work, "-c", "user.name=fixture", "-c", "user.email=fixture@example.com", "commit", "--quiet", "-m", "base")
	gitIn(t, work, "push", "--quiet", bare, "main")
	trace := filepath.Join(t.TempDir(), "trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)

	plan := []repositoryCheckout{{RepositoryID: "github:1", CloneURL: "file://" + bare, BaseBranch: "main", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/repos/large"}}
	workspace := t.TempDir()
	report := runPreparation(t, workspace, fixtureAllocationID, plan, "600")
	require.Equal(t, []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}}, report.checkouts(plan, domain.CheckoutIncomplete))
	require.Equal(t, "content\n", readFile(t, filepath.Join(workspace, "repos", "large", "file-119.txt")))
	require.True(t, startedCheckoutWorker(t, trace), "checkout starts parallel workers")
	require.Equal(t, "8", gitIn(t, filepath.Join(workspace, "repos", "large"), "config", "--local", "--get", "checkout.workers"), "later checkouts in the Work stay parallel")
}

// startedCheckoutWorker reports whether a git trace2 event log records a
// parallel checkout worker process.
func startedCheckoutWorker(t *testing.T, trace string) bool {
	t.Helper()
	for line := range strings.Lines(readFile(t, trace)) {
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		if event.Event == "child_start" && slices.Contains(event.Argv, "checkout--worker") {
			return true
		}
	}
	return false
}

func runPreparation(t *testing.T, workspace, allocation string, plan []repositoryCheckout, budget string) preparationReport {
	t.Helper()
	input := request()
	encodedPlan, err := json.Marshal(plan)
	require.NoError(t, err)
	catalog := `{"name":"talgent-work-fixture","owner":{"name":"Talgent"},"plugins":[]}`
	receipt := storageKey(input.WorkID + ":" + allocation)[:32]
	process := exec.Command("node", "-e", nativePreparation, "claude", catalog, receipt, string(encodedPlan), budget)
	process.Dir, process.Env = workspace, append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_GLOBAL=/dev/null")
	output, err := process.Output()
	require.NoError(t, err)
	report := latestReport(output, 0, domain.CapabilitySnapshot{}, plan)
	require.NotNil(t, report.Repositories, "the run reports every planned repository: %s", output)
	return report
}

// gitRemote creates a bare repository; a non-empty branch gets one commit.
func gitRemote(t *testing.T, root, name, branch string) string {
	t.Helper()
	bare := filepath.Join(root, name+".git")
	gitIn(t, root, "init", "--quiet", "--bare", bare)
	if branch != "" {
		work := filepath.Join(root, name+"-work")
		gitIn(t, root, "init", "--quiet", "-b", branch, work)
		require.NoError(t, os.WriteFile(filepath.Join(work, "README.md"), []byte("base\n"), 0o600))
		gitIn(t, work, "add", "README.md")
		gitIn(t, work, "-c", "user.name=fixture", "-c", "user.email=fixture@example.com", "commit", "--quiet", "-m", "base")
		gitIn(t, work, "push", "--quiet", bare, branch)
	}
	return "file://" + bare
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	process := exec.Command("git", args...)
	process.Dir, process.Env = dir, append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	output, err := process.CombinedOutput()
	require.NoError(t, err, string(output))
	return strings.TrimSpace(string(output))
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	require.NoError(t, err)
	return string(content)
}

func requireNodeAndGit(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"node", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required to run the native preparation adapter", tool)
		}
	}
}
