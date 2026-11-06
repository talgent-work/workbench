//go:build integration

package execution

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/pkg/nativetest"
)

// Our preparation command installs the selected releases into the pinned
// Harness, our launch arguments select the startup Agent and pass the frozen
// Plugin configuration, and our parser reads the Harness's actual inventory.
// Only model inference is controlled. How the Harness then applies an Agent,
// Skill, hook or resumed context is its own behaviour and is not asserted.
func TestNativeLaunchLoadsSelectedAgentAndPluginConfiguration(t *testing.T) {
	binary := nativetest.Require(t, "CLAUDE_BINARY")[0]
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0700))
	plugin := filepath.Join(root, "plugin")
	write := func(name, contents string) {
		t.Helper()
		filename := filepath.Join(plugin, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0700))
		require.NoError(t, os.WriteFile(filename, []byte(contents), 0600))
	}
	write(".claude-plugin/plugin.json", `{"name":"native-fixture","version":"1.0.0","userConfig":{"greeting":{"type":"string","title":"Greeting","description":"Snapshot option","sensitive":true}}}`)
	write("agents/selected.md", "---\nname: selected\ndescription: Selected native role\n---\nNATIVE_SELECTED_ROLE\n")
	write("hooks/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"node \"${CLAUDE_PLUGIN_ROOT}/record.cjs\""}]}]}}`)
	write("record.cjs", `require('node:fs').writeFileSync(process.env.HOME+'/plugin-option', process.env.CLAUDE_PLUGIN_OPTION_GREETING || 'missing')`)
	inference := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "count_tokens") {
			_, _ = io.WriteString(w, `{"input_tokens":10}`)
			return
		}
		if !strings.Contains(r.URL.Path, "/messages") {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "content": []any{}, "model": "claude-sonnet-4-6", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "done"}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}},
			{"type": "message_stop"},
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		}
	}))
	defer inference.Close()
	input := request()
	input.WorkID = uuid.NewString()
	input.ContextMode = domain.ContextFresh
	input.WorkModel = domain.WorkModelSelection{ConfigurationID: "claude-sonnet-4-6"}
	input.Capabilities.StartupAgent = "native-fixture:selected"
	input.Capabilities.Plugins = []domain.SelectedPlugin{{Release: domain.PluginRelease{Slug: "native-fixture", Source: "fixture", Version: "1.0.0", RetrievalRef: "fixture:1.0.0", NativeEntry: []byte(`{"name":"native-fixture","version":"1.0.0","source":"./fixture"}`), Agents: []domain.AgentComponent{{Name: "native-fixture:selected"}}, Parameters: pluginconfig.ParameterDeclarations{"greeting": {Type: "string", Title: "Greeting", Description: "Snapshot option", Sensitive: true}}}, Configuration: pluginconfig.Configuration{"greeting": "snapshot-option"}}}
	input.Capabilities.Plugins = append(input.Capabilities.Plugins, domain.SelectedPlugin{Release: domain.PluginRelease{Slug: "missing-fixture", Source: "fixture", Version: "1.0.0", RetrievalRef: "fixture:missing", NativeEntry: []byte(`{"name":"missing-fixture","version":"1.0.0","source":"./unavailable"}`), Agents: []domain.AgentComponent{{Name: "missing-fixture:worker"}}}})
	require.NoError(t, input.Capabilities.Validate())
	launch, err := (NativeLaunchConfig{Binary: binary}).prepare(sandboxLayout, input, owner(input), "high")
	require.NoError(t, err)
	diagnostic := "/tmp/talgent-native-" + storageKey(input.WorkID+":"+fixtureAllocationID) + ".stderr"
	t.Cleanup(func() { _ = os.Remove(diagnostic) })
	process := exec.CommandContext(ctx, "sh", "-c", launch.Command)
	process.Dir = workspace
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "CLAUDE_CODE_PROJECT_DIR_NAME=native-plugin-test", "ANTHROPIC_API_KEY=local-test-only", "ANTHROPIC_BASE_URL=" + inference.URL, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "TALGENT_WORK_ID=fixture", "TALGENT_WORK_CREDENTIAL=fixture-credential"}
	results := installFixturePlugin(t, ctx, launch, input, workspace, plugin, process.Env)
	require.Len(t, results, 2)
	require.True(t, results[0].Installed)
	require.False(t, results[1].Installed)
	require.Equal(t, "installation_failed", results[1].Reason)
	stdin, err := process.StdinPipe()
	require.NoError(t, err)
	stdout, err := process.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, process.Start())
	t.Cleanup(func() { _ = process.Process.Kill() })
	encoder := json.NewEncoder(stdin)
	require.NoError(t, encoder.Encode(map[string]any{"type": "control_request", "request_id": "initialize", "request": map[string]any{"subtype": "initialize"}}))
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	completed := false
	loadsObserved := false
	for scanner.Scan() {
		var message struct {
			Type     string
			Response struct {
				RequestID string `json:"request_id"`
				Subtype   string
			}
		}
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &message))
		var native nativeprocess.Message
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &native))
		if plugins, observed := nativeprocess.PluginLoads(&native); observed {
			loadsObserved = true
			require.Len(t, plugins, 1, "failed installation must not appear as loaded")
			require.Equal(t, "native-fixture", plugins[0].Slug)
			require.Equal(t, "1.0.0", plugins[0].Version)
		}
		if message.Type == "control_response" && message.Response.RequestID == "initialize" {
			require.Equal(t, "success", message.Response.Subtype, "the pinned Harness accepts the startup Agent selection")
			require.NoError(t, encoder.Encode(map[string]any{"type": "user", "uuid": uuid.NewString(), "session_id": launch.SessionID, "parent_tool_use_id": nil, "message": map[string]string{"role": "user", "content": "Begin."}}))
		}
		if message.Type == "result" {
			completed = true
			break
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, loadsObserved, "the production parser must recognize the actual native inventory")
	if !completed {
		data, _ := os.ReadFile(diagnostic)
		t.Fatalf("native result missing: %s", data)
	}
	require.NoError(t, process.Process.Signal(syscall.SIGINT))
	require.NoError(t, process.Wait())
	option, err := os.ReadFile(filepath.Join(root, "plugin-option"))
	require.NoError(t, err)
	require.Equal(t, "snapshot-option", string(option), "the frozen Sensitive configuration reaches the Plugin under our settings key")
	_, err = os.Stat(filepath.Join(root, ".claude", "projects", "native-plugin-test", launch.SessionID+".jsonl"))
	require.NoError(t, err, "the pinned Harness keeps a conversation's transcript where a restart looks for it")
}
