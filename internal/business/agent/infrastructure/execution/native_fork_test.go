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

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// launchArgs runs a prepared launch against a native binary that records its
// arguments, in a HOME whose transcript of the Work's conversation exists
// when mode is "--resume". It returns the native arguments, or nil when the
// launch starts nothing.
func launchArgs(t *testing.T, input domain.LaunchSpecification, mode string, env ...string) ([]string, nativeLaunch) {
	t.Helper()
	root := t.TempDir()
	output := filepath.Join(root, "arguments.json")
	binary := filepath.Join(root, "claude")
	require.NoError(t, os.WriteFile(binary, []byte("#!/usr/bin/env python3\nimport os,sys,json\nopen(os.environ['ARGV_FILE'],'w').write(json.dumps(sys.argv[1:]))\n"), 0700))
	launch, err := NativeLaunchConfig{Binary: binary}.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.Remove("/tmp/talgent-native-" + storageKey(input.WorkID+":"+fixtureAllocationID) + ".stderr")
	})
	config := filepath.Join(root, ".claude")
	transcripts := filepath.Join(config, "projects", "namespace")
	require.NoError(t, os.MkdirAll(transcripts, 0700))
	if mode == "--resume" {
		require.NoError(t, os.WriteFile(filepath.Join(transcripts, launch.SessionID+".jsonl"), []byte("{}\n"), 0600))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", launch.Command)
	cmd.Env = append(os.Environ(), "ARGV_FILE="+output, "CLAUDE_CONFIG_DIR="+config)
	cmd.Env = append(cmd.Env, env...)
	if err := cmd.Run(); err != nil {
		return nil, launch
	}
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var args []string
	require.NoError(t, json.Unmarshal(data, &args))
	return args, launch
}

// contextArgs are the arguments naming the conversation a launch runs in.
func contextArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fork-session":
			out = append(out, args[i])
		case "--resume", "--session-id", "--resume-session-at":
			out = append(out, args[i], args[i+1])
			i++
		}
	}
	return out
}

// A Work that started from another Work's context forks that Work's native
// conversation at the named position into its own, leaving the source as it
// is; once its own conversation exists, every later start resumes it.
func TestWorkForksItsStartingContextFromTheSourceConversation(t *testing.T) {
	input := request()
	input.WorkID = "review-work"
	input.StartingContext = &domain.StartingContext{SourceWorkID: "project-work", SourceSessionReference: "project-context", Position: "5f1c2b0e-0000-4000-8000-000000000001"}
	source := nativeSessionID("project-work", "project-context")

	input.ContextMode = domain.ContextFresh
	args, launch := launchArgs(t, input, "")
	require.Equal(t, []string{"--resume", source, "--fork-session", "--session-id", launch.SessionID, "--resume-session-at", "5f1c2b0e-0000-4000-8000-000000000001"}, contextArgs(args))
	require.NotEqual(t, source, launch.SessionID)

	input.ContextMode = domain.ContextRecover
	args, _ = launchArgs(t, input, "--session-id")
	require.Equal(t, []string{"--resume", source, "--fork-session", "--session-id", launch.SessionID, "--resume-session-at", "5f1c2b0e-0000-4000-8000-000000000001"}, contextArgs(args), "a recovered start without its own conversation forks again")
	args, _ = launchArgs(t, input, "--resume")
	require.Equal(t, []string{"--resume", launch.SessionID}, contextArgs(args), "a recovered start resumes its own conversation once it exists")

	input.ContextMode = domain.ContextContinue
	args, _ = launchArgs(t, input, "")
	require.Equal(t, []string{"--resume", launch.SessionID}, contextArgs(args))

	input.StartingContext = nil
	input.ContextMode = domain.ContextRecover
	args, _ = launchArgs(t, input, "--session-id")
	require.Equal(t, []string{"--session-id", launch.SessionID}, contextArgs(args), "a Work that started afresh restarts afresh")

	input.StartingContext = &domain.StartingContext{SourceWorkID: "project-work", SourceSessionReference: "project-context", Position: "bad\x00position"}
	_, err := NativeLaunchConfig{Binary: "claude"}.prepare(sandboxLayout, input, owner(input), "")
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
}
