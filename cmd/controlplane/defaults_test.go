package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-jimu/components/config/loader"
	"github.com/stretchr/testify/require"
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

func loadDefaults(t *testing.T) Option {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	directory := filepath.Join(filepath.Dir(source), "../../configs/controlplane")
	var option Option
	require.NoError(t, loader.Load(&option, loader.WithConfigurationDirectory(directory, "defaults"), loader.WithConfigFilePrefix("controlplane")))
	return option
}

// Every Work loads the core plugin and the core API plugin, and a Work of a
// Project loads nothing more unless a deployment's profile adds the Project
// scope's plugins.
func TestEveryWorkLoadsTheCorePluginsByDefault(t *testing.T) {
	builtIns, err := loadDefaults(t).AgentBuiltInPlugins.Plugins()
	require.NoError(t, err)
	require.Equal(t, []string{"talgent", "talgent-api"}, builtIns.Required(""))
	require.Equal(t, []string{"talgent", "talgent-api"}, builtIns.Required("project"))
}

// A deployment whose Agents have no direct access to the core's API empties
// AGENT_API_PLUGIN, and its Works load the core plugin alone.
func TestWithoutDirectAPIAccessWorksLoadNoAPIPlugin(t *testing.T) {
	t.Setenv("AGENT_API_PLUGIN", "")
	builtIns, err := loadDefaults(t).AgentBuiltInPlugins.Plugins()
	require.NoError(t, err)
	require.Equal(t, []string{"talgent"}, builtIns.Required(""))
}
