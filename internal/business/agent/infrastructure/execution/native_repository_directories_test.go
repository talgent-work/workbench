package execution

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// The Harness opens every selected repository at its planned target, relative
// to the workspace it runs in, and loads each one's instruction files.
func TestNativeLaunchOpensEverySelectedRepositoryWithItsInstructions(t *testing.T) {
	input := request()
	input.ContextMode = domain.ContextContinue
	input.RepositorySelection = domain.WorkRepositorySelection{OwnerID: "owner", WorkBranch: "talgent/work", Repositories: []domain.WorkRepository{
		{Provider: "github", RepoID: "1", RepoName: "acme/api", CloneURL: "https://example.invalid/acme/api.git", DefaultBranch: "main", BaseBranch: "main"},
		{Provider: "github", RepoID: "2", RepoName: "other/api", CloneURL: "https://example.invalid/other/api.git", DefaultBranch: "main", BaseBranch: "main"},
		{Provider: "github", RepoID: "3", RepoName: "acme/web ' $(x)", CloneURL: "https://example.invalid/acme/web.git", DefaultBranch: "main", BaseBranch: "main"},
	}}
	var want []string
	for _, checkout := range command.PlanCheckouts(input.WorkID, input.RepositorySelection) {
		relative, err := filepath.Rel(domain.WorkspacePath, checkout.TargetPath)
		require.NoError(t, err)
		want = append(want, relative)
	}
	require.Len(t, want, 3)

	args, instructions := harnessRun(t, input)
	var opened []string
	for i, arg := range args {
		if arg == "--add-dir" {
			require.Less(t, i+1, len(args))
			opened = append(opened, args[i+1])
		}
	}
	require.Equal(t, want, opened, "a Resume opens the same repositories a first start checked out")
	require.Equal(t, "1", instructions, "the opened repositories' instruction files load")
}

func TestNativeLaunchOpensNoDirectoryWithoutRepositories(t *testing.T) {
	input := request()
	input.ContextMode = domain.ContextFresh
	input.RepositorySelection = domain.WorkRepositorySelection{}
	args, _ := harnessRun(t, input)
	require.NotContains(t, args, "--add-dir")
}

// harnessRun runs a start's launch command against a recorder and returns the
// Harness's arguments and whether it was told to load opened directories'
// instruction files.
func harnessRun(t *testing.T, input domain.LaunchSpecification) ([]string, string) {
	t.Helper()
	root := t.TempDir()
	output := filepath.Join(root, "run.json")
	binary := filepath.Join(root, "claude")
	require.NoError(t, os.WriteFile(binary, []byte("#!/usr/bin/env python3\nimport os,sys,json\nopen(os.environ['RUN_FILE'],'w').write(json.dumps({'args':sys.argv[1:],'instructions':os.environ.get('CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD','')}))\n"), 0700))
	launch, err := NativeLaunchConfig{Binary: binary}.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.Remove("/tmp/talgent-native-" + storageKey(input.WorkID+":"+fixtureAllocationID) + ".stderr")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", launch.Command)
	cmd.Env = append(os.Environ(), "RUN_FILE="+output, "CLAUDE_CONFIG_DIR="+filepath.Join(root, ".claude"))
	require.NoError(t, cmd.Run())
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var run struct {
		Args         []string
		Instructions string
	}
	require.NoError(t, json.Unmarshal(data, &run))
	return run.Args, run.Instructions
}
