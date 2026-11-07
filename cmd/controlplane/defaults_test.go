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

// From the sandbox namespace, a platform service resolves by service.namespace.
func TestASandboxReachesPlatformServicesByServiceAndNamespace(t *testing.T) {
	environment := loadDefaults(t).AgentExecution.Environment
	require.Equal(t, "http://controlplane.talgent", environment["TALGENT_API_ADDRESS"])
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
