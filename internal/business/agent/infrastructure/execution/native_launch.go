package execution

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type NativeLaunchConfig struct {
	Binary string `json:"binary" yaml:"binary" toml:"binary"`
}

func (c NativeLaunchConfig) Validate() error {
	if strings.TrimSpace(c.Binary) == "" || strings.ContainsRune(c.Binary, 0) {
		return errors.New("agent: native Claude binary is required")
	}
	return nil
}

type nativeLaunch struct {
	Command            string
	PreparationCommand string
	SessionID          string
}

// launchLayout is where a realization runs the Harness and its preparation.
// Every realization runs the same Harness with the same arguments in the same
// /workspace layout; a layout changes only what lies outside the Work's files.
type launchLayout struct {
	// prefix begins every process.
	prefix string
	// diagnostic is the shell word of the Harness's diagnostics file name.
	diagnostic func(name string) string
	// server declares one of the Work's Connectors to the Harness, or
	// reports that it is not declared.
	server func(connector domain.SelectedConnector) (map[string]any, bool)
	// recovered continues a recovered start's conversation where its
	// transcript is, or starts it fresh.
	recovered func(sessionID string, fresh []string) string
	// preparation runs the preparation script with its budget and arguments.
	preparation func(budget, arguments string) string
	// harnessChoice admits a start's own permission mode and sandbox, which
	// only a Managed Computer's Works have; elsewhere the Harness bypasses
	// permission prompts.
	harnessChoice bool
}

// sandboxDiagnostics is where a Cloud sandbox keeps Harness diagnostics.
const sandboxDiagnostics = "/tmp/"

// sandboxLayout is a Cloud sandbox's: diagnostics in /tmp, HOME the Work's
// mounted HOME, and preparation bounded by coreutils timeout.
var sandboxLayout = launchLayout{
	diagnostic: func(name string) string { return shellWord(sandboxDiagnostics + name) },
	server:     func(connector domain.SelectedConnector) (map[string]any, bool) { return nativeServer(connector), true },
	recovered:  recoveredContext,
	preparation: func(budget, arguments string) string {
		return "umask 077; " + budget + "mkdir -p \"$HOME/.claude\" && exec timeout -k 5s \"${remaining}s\" node -e" + arguments + " 2>/dev/null"
	},
}

// machineLayout is a Managed Computer's. The machine maps /workspace to the
// Work root it names in TALGENT_WORK_ROOT, and nothing outside the Work's
// files changes on the platform's behalf (Computer model, Machine boundary):
//   - Plugins are installed into, and loaded from, a package cache inside the
//     Work root (CLAUDE_CODE_PLUGIN_CACHE_DIR) instead of the machine's
//     ~/.claude/plugins; their selection stays in the Work root's project
//     settings, as on a Cloud;
//   - Harness diagnostics go to the Work root instead of /tmp;
//   - the Harness's own HOME is the machine's, so a recovered start finds the
//     transcript under the machine's Claude configuration;
//   - preparation checks out with the machine's own git credentials, which
//     its "machine" argument keeps, and relies on the script's own deadline
//     instead of coreutils timeout, which macOS lacks.
//
// The Work's Connectors are declared by machineServer, and a Model
// Configuration is reached through Talgent Computer's local proxy, which sets
// the run's ANTHROPIC_BASE_URL. User-level settings stay excluded by
// --setting-sources as on a Cloud.
var machineLayout = launchLayout{
	prefix:        managedRoot,
	harnessChoice: true,
	diagnostic:    func(name string) string { return "\"$talgent_root/.talgent/\"" + shellWord(name) },
	server:        machineServer,
	recovered:     recoveredManagedContext,
	preparation: func(budget, arguments string) string {
		return managedRoot + "umask 077; " + budget + "mkdir -p \"$CLAUDE_CODE_PLUGIN_CACHE_DIR\" && exec node -e" + arguments + " machine 2>/dev/null"
	},
}

// managedRoot is how every Managed process begins: it names the Work root
// and keeps the Plugin package cache there.
const managedRoot = "talgent_root=\"${TALGENT_WORK_ROOT:?}\"; mkdir -p \"$talgent_root/.talgent\" || exit 1; export CLAUDE_CODE_PLUGIN_CACHE_DIR=\"$talgent_root/.talgent/claude-plugins\"; "

// prepare renders the Work's Harness launch and preparation in a layout.
func (c NativeLaunchConfig) prepare(layout launchLayout, request domain.StartExecutionRequest, owner Allocation, thinkingLevel string) (nativeLaunch, error) {
	if err := c.Validate(); err != nil {
		return nativeLaunch{}, err
	}
	if err := validateStartRequest(request); err != nil {
		return nativeLaunch{}, err
	}
	if thinkingLevel != "" && !slices.Contains(domain.ThinkingLevels, thinkingLevel) {
		return nativeLaunch{}, domain.ErrExecutionRequest
	}
	if starting := request.StartingContext; starting != nil && (starting.Position == "" || strings.ContainsRune(starting.SourceWorkID+starting.SourceSessionReference+starting.Position, 0)) {
		return nativeLaunch{}, domain.ErrExecutionRequest
	}
	if !layout.harnessChoice && (request.PermissionMode != "" || request.Capabilities.Sandbox) {
		return nativeLaunch{}, domain.ErrExecutionRequest
	}
	sessionID := nativeSessionID(request.WorkID, request.HarnessSessionReference)
	// Bypass mode needs no allow rule for the Gateway's tools; the Gateway
	// enforces each caller's authority on every call. Prompts of any other
	// mode reach the person through the stdio permission prompt tool.
	args := []string{c.Binary, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--replay-user-messages", "--permission-prompt-tool", "stdio"}
	args = append(args, permissionArguments(request.PermissionMode)...)
	args = append(args, "--system-prompt", request.Capabilities.Prompt)
	switch request.ContextMode {
	case domain.ContextContinue:
		args = append(args, "--resume", sessionID)
	case domain.ContextFresh:
		args = append(args, freshContext(request.StartingContext, sessionID)...)
	}
	// The Soul follows the startup Agent's prompt and never replaces it. The
	// native Harness otherwise renders the system prompt once per conversation
	// and reuses it on resume; rebuilding it on every request lets a resumed
	// Work take up its current Soul and upgraded Plugins.
	if request.Soul != "" {
		args = append(args, "--append-system-prompt", request.Soul)
	}
	args = append(args, "--system-prompt-snapshot", "off")
	// The pinned native CLI skips its default HOME retention sweep when user
	// settings are disabled. Keep workspace configuration and explicit snapshot
	// settings; neither platform config supplies a finite retention period.
	args = append(args, "--setting-sources", "project,local")
	if request.Capabilities.StartupAgent != "" {
		args = append(args, "--agent", request.Capabilities.StartupAgent)
	}
	// Platform declarations come from the Work's retained Marketplace Connectors.
	// Plugin-owned MCP declarations remain in their native packages. Work identity
	// and current Provider credentials are supplied separately at runtime.
	servers := make(map[string]any, len(request.Capabilities.Connectors))
	for _, connector := range request.Capabilities.Connectors {
		if server, declared := layout.server(connector); declared {
			servers[connector.Name] = server
		}
	}
	mcp, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return nativeLaunch{}, err
	}
	args = append(args, "--mcp-config", string(mcp))
	preparation, marketplace, err := c.preparationCommand(layout, request, owner)
	if err != nil {
		return nativeLaunch{}, err
	}
	configs := make(map[string]any, len(request.Capabilities.Plugins))
	for _, selection := range request.Capabilities.Plugins {
		options := map[string]any(selection.Configuration)
		if options == nil {
			options = map[string]any{}
		}
		configs[selection.Release.Slug+"@"+marketplace] = map[string]any{"options": options}
	}

	// Snapshot settings are passed only to this process, never written over HOME.
	flagSettings := map[string]any{"pluginConfigs": configs}
	if request.Capabilities.Sandbox {
		// The Work runs sandboxed or not at all: the Harness refuses to start
		// without a working sandbox, and no command may leave it.
		flagSettings["sandbox"] = map[string]any{"enabled": true, "failIfUnavailable": true, "allowUnsandboxedCommands": false}
	}
	settings, err := json.Marshal(flagSettings)
	if err != nil {
		return nativeLaunch{}, err
	}
	args = append(args, "--settings", string(settings))
	words := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return nativeLaunch{}, domain.ErrExecutionRequest
		}
		words[i] = shellWord(arg)
	}
	// Keep diagnostic bytes out of execd's mixed replay buffer. The remaining
	// process output is the native protocol; diagnostics have sandbox lifetime.
	diagnostic := layout.diagnostic(diagnosticName(owner))
	// The environment override wins over later native apply_flag_settings.
	// Convert the Model Template default to an initial CLI setting so adjustments
	// remain governed by the Harness. Quoted arguments preserve literal values.
	effort := "${CLAUDE_CODE_EFFORT_LEVEL-}"
	if thinkingLevel != "" {
		effort = shellWord(thinkingLevel)
	}
	// A startup Agent's declared model outranks ANTHROPIC_MODEL in the pinned
	// CLI; the Work's chosen main model is passed as the CLI flag that outranks it.
	model := "if test -n \"${ANTHROPIC_MODEL-}\"; then set -- \"$@\" --model \"$ANTHROPIC_MODEL\"; fi; "
	recovered := ""
	if request.ContextMode == domain.ContextRecover {
		// A recovered start resumes the Work's own conversation once it
		// exists; until then it starts it as a fresh start would.
		recovered = layout.recovered(sessionID, freshContext(request.StartingContext, sessionID))
	}
	command := layout.prefix + "exec 2>>" + diagnostic + "; talgent_effort=" + effort + "; unset CLAUDE_CODE_EFFORT_LEVEL; if test -n \"$talgent_effort\"; then set -- --effort \"$talgent_effort\"; else set --; fi; " + model + recovered + "exec " + strings.Join(words, " ") + " \"$@\""
	return nativeLaunch{Command: command, SessionID: sessionID, PreparationCommand: preparation}, nil
}

// diagnosticName is the file a live allocation's Harness writes its
// diagnostics to, in its layout's place for them.
func diagnosticName(owner Allocation) string {
	return "talgent-native-" + storageKey(owner.WorkID+":"+owner.ID) + ".stderr"
}

// permissionArguments launch the Harness in the Work's permission mode: a
// Cloud Work, which has none, and a Managed Work by default bypass permission
// prompts. A Managed Work in another mode may still switch to bypassing while
// it runs, which the Harness allows only to a process launched to allow it.
func permissionArguments(mode domain.PermissionMode) []string {
	if mode == "" || mode == domain.PermissionBypass {
		return []string{"--permission-mode", string(domain.PermissionBypass)}
	}
	return []string{"--permission-mode", string(mode), "--allow-dangerously-skip-permissions"}
}

// recoveredContext continues the Work's conversation when its transcript
// exists in the Work's HOME, where the sandbox's CLAUDE_CONFIG_DIR and
// CLAUDE_CODE_PROJECT_DIR_NAME place it, and otherwise starts it as fresh
// does. A transcript the Harness cannot resume fails the start like any other
// Harness error.
func recoveredContext(sessionID string, fresh []string) string {
	id := shellWord(sessionID)
	return "if test -e \"${CLAUDE_CONFIG_DIR:?}/projects/${CLAUDE_CODE_PROJECT_DIR_NAME:?}/\"" + shellWord(sessionID+".jsonl") +
		"; then set -- \"$@\" --resume " + id + "; else set -- \"$@\"" + shellWords(fresh) + "; fi; "
}

// recoveredManagedContext is recoveredContext on a Managed Computer, where
// the Harness keeps its transcripts in the machine's own Claude configuration
// under the project of the Work root, which stays the Work's for its life.
// The session identity is the Work's own, so any project holding it is the
// Work's.
func recoveredManagedContext(sessionID string, fresh []string) string {
	id := shellWord(sessionID)
	return "talgent_transcript=; for talgent_file in \"${CLAUDE_CONFIG_DIR:-$HOME/.claude}\"/projects/*/" + shellWord(sessionID+".jsonl") +
		"; do if test -e \"$talgent_file\"; then talgent_transcript=1; fi; done; if test -n \"$talgent_transcript\"; then set -- \"$@\" --resume " + id + "; else set -- \"$@\"" + shellWords(fresh) + "; fi; "
}

func shellWord(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// shellWords renders arguments, each preceded by a space.
func shellWords(args []string) string {
	var out strings.Builder
	for _, arg := range args {
		out.WriteString(" " + shellWord(arg))
	}
	return out.String()
}

// nativeSessionID maps a Work's opaque Harness Session reference to the one
// native Session UUID of that Work, without merging different Works'
// transcripts. The reference remains opaque outside Agent.
func nativeSessionID(workID, sessionReference string) string {
	identity, _ := json.Marshal([]string{workID, sessionReference})
	return uuid.NewSHA1(uuid.NameSpaceOID, identity).String()
}

// freshContext starts the Work's own conversation: empty, or a fork of the
// source Work's conversation up to the named position. Works of one
// definition share its HOME and run in /workspace, so the source transcript
// is the one the native Harness resumes; the source continues unchanged.
func freshContext(starting *domain.StartingContext, sessionID string) []string {
	if starting == nil {
		return []string{"--session-id", sessionID}
	}
	return []string{"--resume", nativeSessionID(starting.SourceWorkID, starting.SourceSessionReference), "--fork-session", "--session-id", sessionID, "--resume-session-at", starting.Position}
}

// machineServer declares a Managed Work's Connector. A Work established on a
// Managed Computer holds only Connectors available there
// (CapabilitySnapshot.AvailableOn); a snapshot established before Managed
// Connector availability is filtered by the same rule. An available
// Connector reaches the platform Connector's server with the Work's own identity
// through Talgent Computer's local proxy, whose one /mcp endpoint adds the
// Work credential and Space Token — the Talgent Connector is the only such
// Connector. The run's proxy URL and token stay variables the Harness
// expands from its environment, so neither enters the launch command.
func machineServer(connector domain.SelectedConnector) (map[string]any, bool) {
	if !connector.AvailableOnManaged() || !connector.UsesWorkIdentity() {
		return nil, false
	}
	return map[string]any{"type": "http", "url": "${TALGENT_PROXY_URL}/mcp", "headers": map[string]string{"Authorization": "Bearer ${TALGENT_PROXY_TOKEN}"}}, true
}

// nativeServer renders a retained Connector as a native MCP declaration. Its
// environment and headers hold only credential placeholders.
func nativeServer(connector domain.SelectedConnector) map[string]any {
	if connector.Type == "http" {
		server := map[string]any{"type": "http", "url": sandboxConnectorURL(connector)}
		if len(connector.Headers) > 0 {
			server["headers"] = connector.Headers
		}
		return server
	}
	server := map[string]any{"command": connector.Command, "args": connector.Arguments}
	if len(connector.Environment) > 0 {
		server["env"] = connector.Environment
	}
	return server
}

// sandboxConnectorURL is where a Cloud sandbox's Harness reaches a retained
// Connector. The Talgent Connector, reached with the Work's own identity, is
// served at the sandbox's Gateway address, which the Harness expands from its
// environment: the Credential Proxy adds the Work credential only to requests
// to that address, and a Work keeps the Connector address of its creation
// even after the deployment renames the Gateway. Where the sandbox names no
// Gateway, the retained origin applies.
func sandboxConnectorURL(connector domain.SelectedConnector) string {
	address, err := url.Parse(connector.URL)
	if !connector.UsesWorkIdentity() || err != nil || address.Host == "" || address.User != nil {
		return connector.URL
	}
	origin := address.Scheme + "://" + address.Host
	address.Scheme, address.Host = "", ""
	return "${" + gatewayAddressEnv + ":-" + origin + "}" + address.String()
}
