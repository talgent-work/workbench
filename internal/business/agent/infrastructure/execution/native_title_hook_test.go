package execution

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Every Harness process runs the title hook at each Stop. Its systemMessage
// reaches the output stream by itself; other hooks' events stay out of it.
func TestEveryLaunchReportsTheConversationTitleThroughAStopHook(t *testing.T) {
	input := request()
	input.ContextMode = domain.ContextFresh
	args, _ := launchArgs(t, input, "")
	require.NotContains(t, args, "--include-hook-events")
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string }
		}
	}
	for i, arg := range args {
		if arg == "--settings" {
			require.NoError(t, json.Unmarshal([]byte(args[i+1]), &settings))
		}
	}
	require.Len(t, settings.Hooks["Stop"], 1)
	require.Equal(t, []struct{ Type, Command string }{{Type: "command", Command: titleHook}}, settings.Hooks["Stop"][0].Hooks)
}

// The hook answers the latest title the Harness recorded in its transcript as
// its systemMessage, and nothing before there is one or when the transcript
// cannot be read; it never fails the Stop.
func TestTheTitleHookReportsTheLatestTranscriptTitle(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	root := t.TempDir()
	run := func(transcript string) string {
		t.Helper()
		path := filepath.Join(root, "session.jsonl")
		require.NoError(t, os.WriteFile(path, []byte(transcript), 0600))
		input, err := json.Marshal(map[string]string{"hook_event_name": "Stop", "transcript_path": path})
		require.NoError(t, err)
		cmd := exec.Command("sh", "-c", titleHook)
		cmd.Stdin = strings.NewReader(string(input))
		output, err := cmd.Output()
		require.NoError(t, err)
		return string(output)
	}
	require.JSONEq(t, `{"systemMessage":"talgent-title:\"Kubernetes \\\"PDB\\\" 作用 it's\"","suppressOutput":true}`, run(strings.Join([]string{
		`{"type":"user","message":{"content":"mentions \"ai-title\" in passing"}}`,
		`{"type":"ai-title","aiTitle":"First","sessionId":"s"}`,
		`{"type":"ai-title","aiTitle":"Kubernetes \"PDB\" 作用 it's","sessionId":"s"}`,
		`{"type":"assistant"}`, `{"type":"ai-title", broken`, ``,
	}, "\n")))
	require.Empty(t, run(`{"type":"user"}`+"\n"))
	cmd := exec.Command("sh", "-c", titleHook)
	cmd.Stdin = strings.NewReader(`{"transcript_path":"` + filepath.Join(root, "missing.jsonl") + `"}`)
	output, err := cmd.Output()
	require.NoError(t, err)
	require.Empty(t, output)
}
