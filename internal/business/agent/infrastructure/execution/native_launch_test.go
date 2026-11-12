package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// Without a main model in the Work's Model Configuration, the startup Agent's
// declared model or the Harness default applies.
func TestNativeLaunchPassesNoModelWithoutAWorkMainModel(t *testing.T) {
	input := request()
	input.ContextMode = domain.ContextFresh
	args, _ := launchArgs(t, input, "", "ANTHROPIC_MODEL=")
	require.NotContains(t, args, "--model")
}

func TestNativeLaunchPreservesArgumentsAndSeparatesDiagnostics(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "arguments.jsonl")
	binary := filepath.Join(root, "native ' recorder")
	require.NoError(t, os.WriteFile(binary, []byte("#!/usr/bin/env python3\nimport os,sys,json\nassert 'CLAUDE_CODE_EFFORT_LEVEL' not in os.environ\nprint('native diagnostic',file=sys.stderr)\nwith open(os.environ['ARGV_FILE'],'a') as f: f.write(json.dumps(sys.argv[1:])+'\\n')\n"), 0700))
	input := request()
	input.WorkID = uuid.NewString()
	input.ContextMode = domain.ContextFresh
	input.Capabilities.Prompt = "one ' two\n$(touch '" + filepath.Join(root, "injected") + "') `false` $HOME"
	input.Capabilities.StartupAgent = "fixed:reviewer ' quoted"
	input.Soul = "Be terse ' and $(echo kind)"
	input.Capabilities.Plugins = []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "fixed", Source: "owned", Version: "1", RetrievalRef: "archive:fixed", NativeEntry: []byte(`{"name":"fixed","version":"1","source":{"source":"url","url":"https://example.invalid/fixed.zip","sha256":"abc"}}`), Agents: []domain.AgentComponent{{Name: input.Capabilities.StartupAgent}}, Parameters: pluginconfig.ParameterDeclarations{"message": {Type: "string", Title: "Message", Description: "Message"}}}, Configuration: pluginconfig.Configuration{"message": "quoted ' option"}}}
	config := NativeLaunchConfig{Binary: binary}
	input.Capabilities.Connectors[0].URL = "http://gateway.test/mcp?retained='quoted'"
	launch, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	marker := "/tmp/talgent-native-" + storageKey(input.WorkID+":"+fixtureAllocationID)
	t.Cleanup(func() { _ = os.Remove(marker); _ = os.Remove(marker + ".stderr") })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", launch.Command)
	cmd.Env = append(os.Environ(), "ARGV_FILE="+output, "CLAUDE_CODE_EFFORT_LEVEL=max", "ANTHROPIC_MODEL=work ' model")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run())
	require.Empty(t, stderr.String(), "native diagnostics must not enter the replay channel")
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var args []string
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(data))), &args))
	value := func(flag string) string {
		for i, arg := range args {
			if arg == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		t.Fatalf("missing argument %s", flag)
		return ""
	}
	require.Equal(t, "work ' model", value("--model"), "the Work's main model outranks the startup Agent's declared model")
	require.Equal(t, "max", value("--effort"))
	require.Equal(t, input.Capabilities.Prompt, value("--system-prompt"))
	require.Equal(t, input.Capabilities.StartupAgent, value("--agent"))
	require.Equal(t, input.Soul, value("--append-system-prompt"), "the Soul follows the startup Agent's prompt")
	require.Equal(t, input.Capabilities.Prompt, value("--system-prompt"), "the Soul never replaces the prompt")
	require.Equal(t, "off", value("--system-prompt-snapshot"))
	require.Equal(t, "project,local", value("--setting-sources"), "HOME user settings, including their MCP servers and retention sweep, stay disabled")
	require.Equal(t, launch.SessionID, value("--session-id"))
	require.NotContains(t, args, "--plugin-dir")
	require.NotContains(t, args, "--plugin-url")
	require.Contains(t, launch.PreparationCommand, "fixed.zip")
	require.Equal(t, "stream-json", value("--input-format"))
	require.Equal(t, "stream-json", value("--output-format"))
	require.Contains(t, args, "--replay-user-messages")
	require.Equal(t, "stdio", value("--permission-prompt-tool"))
	require.Equal(t, "bypassPermissions", value("--permission-mode"))
	require.NotContains(t, args, "--allowedTools", "bypass mode needs no allow rule; the Gateway enforces authority")
	require.NotContains(t, args, "--dangerously-skip-permissions")
	var mcp struct {
		MCPServers map[string]struct {
			Type, URL string
			Headers   map[string]string
		}
	}
	require.NoError(t, json.Unmarshal([]byte(value("--mcp-config")), &mcp))
	require.Equal(t, "http", mcp.MCPServers["talgent"].Type)
	require.Equal(t, "${TALGENT_GATEWAY_ADDRESS:-http://gateway.test}/mcp?retained='quoted'", mcp.MCPServers["talgent"].URL)
	require.Empty(t, mcp.MCPServers["talgent"].Headers, "the Credential Proxy adds the Work's Identity Token; the declaration carries none")
	var settings struct {
		PluginConfigs map[string]struct{ Options map[string]string }
	}
	require.NoError(t, json.Unmarshal([]byte(value("--settings")), &settings))
	require.Equal(t, "quoted ' option", settings.PluginConfigs["fixed@talgent"].Options["message"])
	_, err = os.Stat(filepath.Join(root, "injected"))
	require.True(t, os.IsNotExist(err), "prompt must not execute shell syntax")
	diagnostic, err := os.ReadFile(marker + ".stderr")
	require.NoError(t, err)
	require.Contains(t, string(diagnostic), "native diagnostic")
}

// A start whose startup Agent no release it took declares any more launches
// without one (Dropped startup Agent); the Work's selection stays.
func TestNativeLaunchRunsWithoutADroppedStartupAgent(t *testing.T) {
	config := NativeLaunchConfig{Binary: "claude"}
	input := request()
	input.ContextMode = domain.ContextContinue
	input.Capabilities.StartupAgent = "talgent:retired-role"
	require.True(t, input.Capabilities.StartupAgentDropped())
	launch, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.NotContains(t, launch.Command, "--agent")
	require.NotContains(t, launch.Command, "retired-role")
}

// The Harness's plugin configuration names each Plugin by the catalog its
// release came from, the marketplace every Work installs it from; no Work
// identity appears in it.
func TestNativeLaunchNamesPluginConfigurationByCatalog(t *testing.T) {
	config := NativeLaunchConfig{Binary: "claude"}
	input := request()
	input.ContextMode = domain.ContextFresh
	input.Capabilities.Plugins = []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "talgent", Source: "owned", Version: "1", RetrievalRef: "archive:talgent", NativeEntry: []byte(`{"name":"talgent","version":"1"}`)}}}
	launch, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Contains(t, launch.Command, `"talgent@talgent":`)
	require.Contains(t, launch.PreparationCommand, `"name":"talgent"`)
	require.NotContains(t, launch.Command, "talgent-work-")
}

// A resumed Work rebuilds its system prompt, so the current Soul applies; a
// Work whose definition has no Soul appends nothing.
func TestResumeAppliesTheCurrentSoul(t *testing.T) {
	config := NativeLaunchConfig{Binary: "claude"}
	input := request()
	input.ContextMode = domain.ContextContinue
	plain, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.NotContains(t, plain.Command, "--append-system-prompt")
	require.Contains(t, plain.Command, "'--system-prompt-snapshot' 'off'")
	input.Soul = "Answer in haiku."
	resumed, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Contains(t, resumed.Command, "--resume '"+resumed.SessionID+"'")
	require.Contains(t, resumed.Command, "'--append-system-prompt' 'Answer in haiku.'")
	require.Contains(t, resumed.Command, "'--system-prompt-snapshot' 'off'")
}

func TestEveryRunOfAWorkContinuesItsHarnessSession(t *testing.T) {
	config := NativeLaunchConfig{Binary: "claude"}
	input := request()
	input.ContextMode = domain.ContextFresh
	fresh, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	input.ContextMode = domain.ContextContinue
	resumed, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Equal(t, fresh.SessionID, resumed.SessionID)
	require.Contains(t, resumed.Command, "\"${CLAUDE_CONFIG_DIR:?}\"/projects/*/'"+fresh.SessionID+".jsonl'", "a resume continues the conversation whose transcript exists")
	input.ContextMode = domain.ContextRecover
	restarted, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Equal(t, fresh.SessionID, restarted.SessionID)
	require.Contains(t, restarted.Command, "\"${CLAUDE_CONFIG_DIR:?}\"/projects/*/'"+fresh.SessionID+".jsonl'", "a restart continues the conversation whose transcript exists")
	input.WorkID = "another-session"
	separate, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.NotEqual(t, restarted.SessionID, separate.SessionID)
	input.Capabilities.Plugins = []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "missing"}}}
	_, err = config.prepare(sandboxLayout, input, owner(input), "")
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities)
}

func TestNativeLaunchDeclaresSelectedConnectorsWithPlaceholdersOnly(t *testing.T) {
	input := request()
	github := connectorcatalogtest.GitHub().SelectedConnector
	remote := domain.SelectedConnector{ID: "01900000-0000-7000-8000-000000000388", Name: "remote", Type: "http", URL: "https://mcp.example.test/mcp", Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"},
		Authentication: "provider_authorization", Provider: "example", Target: "example-api", ProviderAuthentication: "credential_proxy"}
	input.Capabilities.Connectors = append(input.Capabilities.Connectors, github, remote)
	launch, err := (NativeLaunchConfig{Binary: "claude"}).prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Contains(t, launch.Command, `"github":{"args":["stdio"],"command":"github-mcp-server","env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"${GITHUB_PERSONAL_ACCESS_TOKEN}"}}`)
	require.Contains(t, launch.Command, `"remote":{"headers":{"Authorization":"Bearer ${TOKEN}"},"type":"http","url":"https://mcp.example.test/mcp"}`)
}

// A Work keeps the Talgent Connector it was created with, possibly at an
// address the deployment has since renamed. The Harness reaches it at the
// sandbox's current Gateway address, the one the Credential Proxy adds the
// Work's Identity Token to, and at the retained origin only where the sandbox
// names no Gateway.
func TestNativeLaunchReachesTheTalgentConnectorAtTheSandboxGatewayAddress(t *testing.T) {
	input := request()
	input.Capabilities.Connectors[0].URL = "http://connector.talgent.svc.cluster.local/mcp"
	launch, err := (NativeLaunchConfig{Binary: "claude"}).prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Contains(t, launch.Command, `"talgent":{"type":"http","url":"${TALGENT_GATEWAY_ADDRESS:-http://connector.talgent.svc.cluster.local}/mcp"}`)
}

// On a Managed Computer the Talgent Connector is reached through the local
// proxy, whose URL and token the Harness expands from the run's environment;
// no other Connector is declared there.
func TestManagedLaunchDeclaresTheTalgentConnectorThroughTheLocalProxy(t *testing.T) {
	input := request()
	input.ComputerID = "01900000-0000-7000-8000-0000000000c1"
	// A snapshot established before Managed Connector availability may hold
	// a Provider-authorized Connector; it is not declared.
	input.Capabilities.Connectors = append(input.Capabilities.Connectors, connectorcatalogtest.GitHub().SelectedConnector)
	launch, err := (NativeLaunchConfig{Binary: "claude"}).prepare(machineLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Contains(t, launch.Command, `'{"mcpServers":{"talgent":{"headers":{"Authorization":"Bearer ${TALGENT_PROXY_TOKEN}"},"type":"http","url":"${TALGENT_PROXY_URL}/mcp"}}}'`)
	require.NotContains(t, launch.Command, "github-mcp-server")
	require.NotContains(t, launch.Command, "connector.talgent.svc", "the in-cluster Gateway address is not reachable from the machine")
}

// An Application's own endpoint is declared as the Application gave it,
// headers unchanged, beside the Work's Connectors, on a Cloud and on a
// Managed Computer alike.
func TestNativeLaunchDeclaresApplicationEndpointsAsGiven(t *testing.T) {
	for name, layout := range map[string]launchLayout{"cloud": sandboxLayout, "managed": machineLayout} {
		t.Run(name, func(t *testing.T) {
			input := request()
			input.Capabilities.ApplicationEndpoints = []domain.ApplicationMCPEndpoint{{Name: "tickets", URL: "https://desk.example/mcp", Headers: map[string]string{"Authorization": "Bearer desk-key"}}}
			launch, err := (NativeLaunchConfig{Binary: "claude"}).prepare(layout, input, owner(input), "")
			require.NoError(t, err)
			require.Contains(t, launch.Command, `"tickets":{"headers":{"Authorization":"Bearer desk-key"},"type":"http","url":"https://desk.example/mcp"}`)
		})
	}
}
