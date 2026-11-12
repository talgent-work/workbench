package execution

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-jimu/components/config/loader"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// The core's Standalone Work role and roles a caller supplies from its own plugin.
var defaultRoleCases = []struct{ purpose, scope, plugin, agent string }{
	{"work", "employee", "talgent", "talgent:work-executor"},
	{"work", "employee", "caller-plugin", "caller-plugin:case-executor"},
	{"project_coordination", "project", "caller-plugin", "caller-plugin:coordinator"},
}

func TestConfiguredRolesResolveDeploymentCapabilities(t *testing.T) {
	t.Chdir(t.TempDir())
	config := productionNativeConfig(t)
	for _, tc := range defaultRoleCases {
		t.Run(tc.purpose, func(t *testing.T) {
			definitions := &nativeDefaultDefinition{}
			_, err := command.NewEnsureAgentDefinitionHandler(definitions, definitions).Handle(t.Context(), suppliedRoleDefinition(tc.scope, "owner", tc.purpose, tc.plugin, tc.agent))
			require.NoError(t, err)
			input := request()
			definition := definitions.value.ProvideCapabilities()
			require.Equal(t, []string{tc.plugin}, definition.RequiredPlugins)
			input.Capabilities, err = domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: definition.RequiredPlugins, DefaultStartupAgent: definition.StartupAgent}, []domain.PluginRelease{{CatalogName: "talgent", Slug: tc.plugin, Source: "owned", Version: "1.0.0", RetrievalRef: "archive:runtime", NativeEntry: []byte(`{"name":"` + tc.plugin + `","version":"1.0.0","source":{"source":"url","url":"https://example.invalid/runtime.zip","sha256":"abc"}}`), Agents: []domain.AgentComponent{{Name: tc.agent}}}})
			require.NoError(t, err)
			launch, err := config.prepare(sandboxLayout, input, owner(input), "")
			require.NoError(t, err, "launch must consume the selected immutable runtime release")
			require.Contains(t, launch.PreparationCommand, "runtime.zip")
			require.NotContains(t, launch.Command, "--plugin-dir")
			require.Contains(t, launch.Command, "'--agent' '"+input.Capabilities.StartupAgent+"'")
		})
	}
}

func productionNativeConfig(t *testing.T) NativeLaunchConfig {
	t.Helper()
	directory := filepath.Join(nativeDefaultsRepositoryRoot(t), "configs/controlplane")
	var config struct {
		Execution struct {
			Native NativeLaunchConfig `json:"native"`
		} `json:"agent-execution"`
	}
	require.NoError(t, loader.Load(&config, loader.WithConfigurationDirectory(directory, "defaults"), loader.WithConfigFilePrefix("controlplane")))
	return config.Execution.Native
}

// Compiled CI tests execute from the repository root; go test uses the package
// directory. Anchor production artifacts to their source instead of either cwd.
func nativeDefaultsRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must locate the test source")
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../../.."))
}

type nativeDefaultDefinition struct {
	query.AgentDefinitionQueryRepository
	value *domain.AgentDefinition
}

func (r *nativeDefaultDefinition) Get(context.Context, string) (*domain.AgentDefinition, error) {
	return r.value, nil
}
func (r *nativeDefaultDefinition) Save(_ context.Context, a *domain.AgentDefinition) error {
	r.value = a
	return nil
}
func (*nativeDefaultDefinition) FindForPurpose(context.Context, query.GetAgentDefinitionForPurpose) (string, error) {
	return "", domain.ErrAgentDefinitionNotFound
}

func suppliedRoleDefinition(scope, owner, purpose, plugin, startupAgent string) command.EnsureAgentDefinition {
	return command.EnsureAgentDefinition{AgentDefinitionInput: domain.AgentDefinitionInput{Owners: []string{"owner"},
		UsageScope: domain.UsageScope{Kind: scope, Reference: owner}, Purpose: purpose,
		Capabilities: domain.CapabilityConfiguration{StartupAgent: startupAgent, RequiredPlugins: []string{plugin}},
	}, EmployeeDisplayName: "Jack"}
}
