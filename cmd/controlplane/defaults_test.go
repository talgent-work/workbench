package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-jimu/components/config/loader"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Sandboxes and the Cloud's execution quota share one namespace, apart from
// the platform's: a quota counts only the Pods in its own namespace.
func TestSandboxesAndTheExecutionQuotaShareTheSandboxNamespace(t *testing.T) {
	option := loadDefaults(t)
	require.Equal(t, "talgent-sandbox", option.AgentExecution.SandboxNamespace)
	require.Equal(t, "talgent-sandbox", option.Computer.Cloud.Namespace)

	t.Setenv("SANDBOX_NAMESPACE", "runs")
	option = loadDefaults(t)
	require.Equal(t, "runs", option.AgentExecution.SandboxNamespace)
	require.Equal(t, "runs", option.Computer.Cloud.Namespace)
}

// A sandbox reaches the platform at the entry's public origin, and the
// Collector by Service and namespace.
func TestASandboxReachesPlatformServicesByServiceAndNamespace(t *testing.T) {
	t.Setenv("PLATFORM_API_ADDRESS", "https://workbench.example.com")
	environment := loadDefaults(t).AgentExecution.Environment
	require.Equal(t, "https://workbench.example.com", environment["TALGENT_API_ADDRESS"])
	require.Equal(t, "http://otel-collector.talgent:4318", environment["OTEL_EXPORTER_OTLP_ENDPOINT"])
}

// A profile declares Built-in Plugins for a Work Caller kind beside the
// platform scope, which it leaves as the defaults declare it.
func TestAProfileAddsAWorkCallerKindsBuiltInPlugins(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	defaults, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../configs/controlplane/defaults.yml"))
	require.NoError(t, err)
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "defaults.yml"), defaults, 0o600))
	profile := "agent-built-in-plugins:\n  work-caller-kinds:\n    project:\n      - extension-plugin\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "controlplane_extension.yml"), []byte(profile), 0o600))
	var option Option
	require.NoError(t, loader.Load(&option, loader.WithConfigurationDirectory(directory, "defaults"), loader.WithConfigFilePrefix("controlplane"), loader.WithProfilesAlias("extension")))

	builtIns, err := option.AgentBuiltInPlugins.Plugins()
	require.NoError(t, err)
	require.Equal(t, []string{"talgent", "talgent-api", "extension-plugin"}, builtIns.Required(domain.WorkCaller{Kind: "project", ID: "p"}))
	require.Equal(t, []string{"talgent", "talgent-api"}, builtIns.Required(domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "p"}))
}

func loadDefaults(t *testing.T) Option {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	directory := filepath.Join(filepath.Dir(source), "../../configs/controlplane")
	var option Option
	require.NoError(t, loader.Load(&option, loader.WithConfigurationDirectory(directory, "defaults"), loader.WithConfigFilePrefix("controlplane")))
	return option
}

// Every Work loads the core plugin and the core API plugin, and a Work loads
// nothing more for its Work Caller's kind unless a deployment's profile adds
// that kind's plugins.
func TestEveryWorkLoadsTheCorePluginsByDefault(t *testing.T) {
	builtIns, err := loadDefaults(t).AgentBuiltInPlugins.Plugins()
	require.NoError(t, err)
	require.Equal(t, []string{"talgent", "talgent-api"}, builtIns.Required(domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "p"}))
	require.Equal(t, []string{"talgent", "talgent-api"}, builtIns.Required(domain.WorkCaller{Kind: "project", ID: "p"}))
}

// A deployment whose Agents have no direct access to the core's API empties
// AGENT_API_PLUGIN, and its Works load the core plugin alone.
func TestWithoutDirectAPIAccessWorksLoadNoAPIPlugin(t *testing.T) {
	t.Setenv("AGENT_API_PLUGIN", "")
	builtIns, err := loadDefaults(t).AgentBuiltInPlugins.Plugins()
	require.NoError(t, err)
	require.Equal(t, []string{"talgent"}, builtIns.Required(domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "p"}))
}
