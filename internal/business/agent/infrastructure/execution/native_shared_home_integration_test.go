//go:build integration

package execution

import (
	"bufio"
	"bytes"
	"compress/gzip"
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/pkg/nativetest"
)

// Our preparation commands run in parallel against one shared HOME and each
// Work's Harness loads its own pinned release, Skills and Plugin parameters,
// also when resumed after another version populated the HOME. Only inference
// is controlled; no sandbox lifecycle is claimed here.
func TestNativeParallelWorksKeepPluginVersionsAndParametersInSharedHome(t *testing.T) {
	binary := nativetest.Require(t, "CLAUDE_BINARY")[0]
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	home := t.TempDir()
	inference, requests := sharedHomeInference(t)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "ANTHROPIC_API_KEY=fixture-only", "ANTHROPIC_BASE_URL=" + inference.URL, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "TALGENT_WORK_ID=fixture", "TALGENT_WORK_CREDENTIAL=fixture-credential"}
	config := NativeLaunchConfig{Binary: binary}
	type nativeWork struct {
		workspace, label, version string
		request                   domain.StartExecutionRequest
		launch                    nativeLaunch
		prepare, process          *exec.Cmd
		installation, output      bytes.Buffer
	}
	works := []*nativeWork{}
	for i, label := range []string{"left", "right"} {
		work := &nativeWork{workspace: filepath.Join(home, label), label: label, version: fmt.Sprintf("%d.0.0", i+1)}
		require.NoError(t, os.MkdirAll(work.workspace, 0700))
		work.request = request()
		work.request.WorkID = uuid.NewString()
		work.request.HarnessSessionReference = uuid.NewString()
		work.request.ContextMode = domain.ContextFresh
		work.request.WorkModel = domain.WorkModelSelection{ConfigurationID: "claude-sonnet-4-6"}
		work.request.Capabilities = domain.CapabilitySnapshot{Connectors: request().Capabilities.Connectors, Prompt: "WORK_" + label, StartupAgent: "shared:worker", Plugins: []domain.SelectedPlugin{{
			Release:       domain.PluginRelease{Slug: "shared", Source: "fixture", Version: work.version, RetrievalRef: "fixture:" + work.version, NativeEntry: []byte(fmt.Sprintf(`{"name":"shared","version":%q,"source":"./fixture"}`, work.version)), Skills: []domain.SkillComponent{{Name: "shared:" + label}}, Agents: []domain.AgentComponent{{Name: "shared:worker"}}, Parameters: pluginconfig.ParameterDeclarations{"greeting": {Type: "string", Title: "Greeting", Description: "Retained value"}}},
			Configuration: pluginconfig.Configuration{"greeting": "PRIVATE_" + label},
		}}}
		var err error
		work.launch, err = config.prepare(sandboxLayout, work.request, owner(work.request), "high")
		require.NoError(t, err)
		diagnostic := "/tmp/talgent-native-" + storageKey(work.request.WorkID+":"+fixtureAllocationID) + ".stderr"
		t.Cleanup(func() { _ = os.Remove(diagnostic) })
		plugin := filepath.Join(work.workspace, ".claude", "talgent", "talgent-work-"+storageKey(work.request.WorkID), "fixture")
		write := func(name, body string) {
			t.Helper()
			file := filepath.Join(plugin, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
			require.NoError(t, os.WriteFile(file, []byte(body), 0600))
		}
		write(".claude-plugin/plugin.json", fmt.Sprintf(`{"name":"shared","version":%q,"userConfig":{"greeting":{"type":"string","title":"Greeting","description":"Retained value"}}}`, work.version))
		write("skills/"+label+"/SKILL.md", "---\nname: "+label+"\ndescription: Work specific skill\nargument-hint: \"[value]\"\n---\nSKILL_"+label+"_"+work.version+"=$ARGUMENTS\n")
		roleTool := []string{"Read", "Grep"}[i]
		write("agents/worker.md", "---\nname: worker\ndescription: Work specific role\ntools: "+roleTool+"\n---\nROLE_"+label+"_"+work.version+"\n")
		write("hooks/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"node \"${CLAUDE_PLUGIN_ROOT}/record.cjs\""}]}]}}`)
		write("record.cjs", `const fs=require('node:fs'); fs.appendFileSync('observed-option',process.env.CLAUDE_PLUGIN_OPTION_GREETING+'\n');`)
		work.prepare = exec.CommandContext(ctx, "sh", "-c", work.launch.PreparationCommand)
		work.prepare.Dir, work.prepare.Env = work.workspace, env
		work.prepare.Stdout, work.prepare.Stderr = &work.installation, &work.installation
		works = append(works, work)
	}
	// Launch both preparations before waiting; the production HOME lock must
	// protect native global registries while each project keeps its own selection.
	for _, work := range works {
		require.NoError(t, work.prepare.Start())
	}
	for _, work := range works {
		require.NoError(t, work.prepare.Wait(), "%s", work.installation.String())
		result := installationResults(work.installation.Bytes(), 0, work.request.Capabilities)
		require.Len(t, result, 1)
		require.True(t, result[0].Installed, "%s: %s", work.label, result[0].Reason)
		work.process = nativeSharedHomeProcess(t, ctx, work.launch, work.workspace, env, "/shared:"+work.label+" original-"+work.label, &work.output)
	}
	for _, work := range works {
		require.NoError(t, work.process.Start())
	}
	for _, work := range works {
		require.NoError(t, work.process.Wait(), "%s", work.output.String())
		assertSharedHomeInventory(t, work.output.Bytes(), work.label, work.version)
		option, err := os.ReadFile(filepath.Join(work.workspace, "observed-option"))
		require.NoError(t, err)
		require.Equal(t, "PRIVATE_"+work.label+"\n", string(option))
	}
	// The inference gate opens only after both Works request inference. This
	// proves simultaneous use of the shared HOME, rather than serial smoke tests.
	for range works {
		select {
		case <-requests:
		case <-ctx.Done():
			t.Fatal("parallel native inference was not observed")
		}
	}
	for _, work := range works {
		for _, name := range []string{"settings.json", "settings.local.json"} {
			settings, err := os.ReadFile(filepath.Join(work.workspace, ".claude", name))
			require.NoError(t, err)
			require.NotContains(t, string(settings), "PRIVATE_", "snapshot parameters must not leak into persistent project settings")
		}
	}
	// Reuse the first Work after the other version populated HOME's native cache.
	// No installation or current catalog lookup is performed on this continuation.
	retained := works[0]
	retained.request.ContextMode = domain.ContextContinue
	nextOwner := Allocation{WorkID: retained.request.WorkID, ID: uuid.NewString()}
	resumed, err := config.prepare(sandboxLayout, retained.request, nextOwner, "high")
	require.NoError(t, err)
	require.Equal(t, retained.launch.SessionID, resumed.SessionID)
	diagnostic := "/tmp/talgent-native-" + storageKey(nextOwner.WorkID+":"+nextOwner.ID) + ".stderr"
	t.Cleanup(func() { _ = os.Remove(diagnostic) })
	var output bytes.Buffer
	process := nativeSharedHomeProcess(t, ctx, resumed, retained.workspace, env, "/shared:left resumed-left", &output)
	require.NoError(t, process.Run(), "%s", output.String())
	assertSharedHomeInventory(t, output.Bytes(), "left", "1.0.0")
	option, err := os.ReadFile(filepath.Join(retained.workspace, "observed-option"))
	require.NoError(t, err)
	require.Equal(t, "PRIVATE_left\nPRIVATE_left\n", string(option))
}

func nativeSharedHomeProcess(t *testing.T, ctx context.Context, launch nativeLaunch, workspace string, env []string, input string, output *bytes.Buffer) *exec.Cmd {
	t.Helper()
	var stdin bytes.Buffer
	encoder := json.NewEncoder(&stdin)
	require.NoError(t, encoder.Encode(map[string]any{"type": "control_request", "request_id": "init", "request": map[string]string{"subtype": "initialize"}}))
	require.NoError(t, encoder.Encode(map[string]any{"type": "user", "uuid": uuid.NewString(), "session_id": launch.SessionID, "parent_tool_use_id": nil, "message": map[string]string{"role": "user", "content": input}}))
	process := exec.CommandContext(ctx, "sh", "-c", launch.Command)
	process.Dir, process.Env, process.Stdin = workspace, env, &stdin
	process.Stdout, process.Stderr = output, output
	return process
}

func assertSharedHomeInventory(t *testing.T, output []byte, label, version string) {
	t.Helper()
	state := nativeprocess.ProtocolState{}
	observed, completed := false, false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var message nativeprocess.Message
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &message))
		state.ObserveCapabilities(&message, "init")
		if plugins, found := nativeprocess.PluginLoads(&message); found {
			observed = true
			require.Len(t, plugins, 1)
			require.Equal(t, "shared", plugins[0].Slug)
			require.Equal(t, version, plugins[0].Version)
		}
		completed = completed || message.Type == "result"
	}
	require.NoError(t, scanner.Err())
	require.True(t, observed)
	require.True(t, completed)
	require.True(t, state.Capabilities.Observed)
	names := []string{}
	for _, command := range state.Capabilities.Commands {
		if strings.HasPrefix(command.Name, "shared:") {
			names = append(names, command.Name)
		}
	}
	require.Equal(t, []string{"shared:" + label}, names)
}

func sharedHomeInference(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	requests := make(chan string, 8)
	ready := make(chan struct{})
	var arrivals atomic.Int32
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "count_tokens") {
			_, _ = io.WriteString(w, `{"input_tokens":10}`)
			return
		}
		if !strings.Contains(r.URL.Path, "/messages") {
			http.NotFound(w, r)
			return
		}
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "gzip", 400)
				return
			}
			defer gz.Close()
			reader = gz
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			http.Error(w, "body", 400)
			return
		}
		requests <- string(body)
		if arrivals.Add(1) >= 2 {
			once.Do(func() { close(ready) })
		}
		select {
		case <-ready:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "content": []any{}, "model": "claude-sonnet-4-6", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "done"}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}},
			{"type": "message_stop"},
		}
		for _, event := range events {
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		}
	}))
	t.Cleanup(server.Close)
	return server, requests
}
