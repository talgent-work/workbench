package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/platformcatalog"
)

func TestNativeSandboxExpandsTheWorkModelConfigurationTemplate(t *testing.T) {
	base, _, fixture := sandboxClient(t)
	deepseek := command.ModelTemplate{ID: "deepseek-flash", Vendor: "deepseek", BaseURL: "https://api.deepseek.com/anthropic", OpusModel: "deepseek-flash[1m]", SonnetModel: "deepseek-flash[1m]", HaikuModel: "deepseek-flash", FableModel: "deepseek-flash[1m]", SubagentModel: "deepseek-flash", ThinkingLevels: []string{"low", "high", "max"}, DefaultThinkingLevel: "max", Environment: map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "786432"}}
	glm := command.ModelTemplate{ID: "glm-5.3-coding-plan", Vendor: "zhipu", BaseURL: "https://open.bigmodel.cn/api/anthropic", OpusModel: "glm-5.3[1m]", SonnetModel: "glm-5.3-sonnet-tier", HaikuModel: "glm-5.3-flash[1m]", FableModel: "glm-5.3-fable-tier", SubagentModel: "glm-5.3-subagent", ThinkingLevels: []string{"low", "high", "max"}, DefaultThinkingLevel: "high", Environment: map[string]string{"API_TIMEOUT_MS": "3000000"}}
	models := func(template string) *platformcatalog.Catalog {
		return catalog([]command.ModelTemplate{deepseek, glm}, command.ModelConfiguration{ID: "deepseek", Name: "DeepSeek", Template: "deepseek-flash", APIKey: "deepseek-key"}, command.ModelConfiguration{ID: "glm", Name: "GLM 5.3", Template: template, APIKey: "glm-key"})
	}
	settings := config()
	settings.Models = models("glm-5.3-coding-plan")
	provider, err := NewSandboxes(base.client, base.pods, settings)
	require.NoError(t, err)
	selected := request()
	selected.WorkModel = domain.WorkModelSelection{ConfigurationID: "glm"}
	_, err = provider.submitRequest(t.Context(), selected, "execution-token-fixture")
	require.NoError(t, err)
	env := fixture.create.Env
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", env["ANTHROPIC_BASE_URL"])
	require.Equal(t, "glm-5.3[1m]", env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	require.Equal(t, "glm-5.3-sonnet-tier", env["ANTHROPIC_DEFAULT_SONNET_MODEL"], "each tier is passed as the template declares it")
	require.NotContains(t, env, "ANTHROPIC_MODEL", "without a main model Claude Code picks its default tier")
	require.Equal(t, "glm-5.3-flash[1m]", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	require.Equal(t, "glm-5.3-fable-tier", env["ANTHROPIC_DEFAULT_FABLE_MODEL"])
	require.Equal(t, "glm-5.3-subagent", env["CLAUDE_CODE_SUBAGENT_MODEL"], "subagents use the template's own subagent model")
	require.Equal(t, "high", env["CLAUDE_CODE_EFFORT_LEVEL"], "the template default seeds the initial effort")
	require.Equal(t, "3000000", env["API_TIMEOUT_MS"])
	require.NotContains(t, env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "another template's settings never leak in")
	require.Equal(t, "opensandbox-credential-proxy", env["ANTHROPIC_AUTH_TOKEN"])
	for _, value := range env {
		require.NotEqual(t, "glm-key", value, "the key reaches only the credential Vault")
		require.NotEqual(t, "lifecycle-secret", value)
	}
	require.NotContains(t, env, "OPENSANDBOX_API_KEY")
	require.NotContains(t, env, "AGENT_CONTROL_TOKEN")

	// The configuration now names another template: the next start uses it.
	settings.Models = models("deepseek-flash")
	provider, err = NewSandboxes(base.client, base.pods, settings)
	require.NoError(t, err)
	_, err = provider.submitRequest(t.Context(), selected, "execution-token-fixture")
	require.NoError(t, err)
	require.Equal(t, "https://api.deepseek.com/anthropic", fixture.create.Env["ANTHROPIC_BASE_URL"])
	require.Equal(t, "deepseek-flash[1m]", fixture.create.Env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	require.Equal(t, "deepseek-flash", fixture.create.Env["CLAUDE_CODE_SUBAGENT_MODEL"])

	// A template naming a main model starts the Work on it; the opus tier stays.
	routed := command.ModelTemplate{ID: "kimi-k3-token-plan", Vendor: "kimi", BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic", MainModel: "auto", OpusModel: "k3", SonnetModel: "k3", HaikuModel: "k3", FableModel: "k3", SubagentModel: "k3"}
	settings.Models = catalog([]command.ModelTemplate{routed}, command.ModelConfiguration{ID: "glm", Name: "Kimi K3", Template: "kimi-k3-token-plan", APIKey: "kimi-key"})
	provider, err = NewSandboxes(base.client, base.pods, settings)
	require.NoError(t, err)
	_, err = provider.submitRequest(t.Context(), selected, "execution-token-fixture")
	require.NoError(t, err)
	require.Equal(t, "auto", fixture.create.Env["ANTHROPIC_MODEL"])
	require.Equal(t, "k3", fixture.create.Env["ANTHROPIC_DEFAULT_OPUS_MODEL"])

	selected.WorkModel = domain.WorkModelSelection{ConfigurationID: "withdrawn"}
	_, err = provider.submitRequest(t.Context(), selected, "execution-token-fixture")
	require.ErrorIs(t, err, errModelConfigurationNotProvided)

	for _, key := range modelManagedEnvironment {
		shared := config()
		shared.Environment = map[string]string{key: "global"}
		require.Error(t, shared.Validate(), "%s belongs to the Work's model configuration", key)
	}
}

// What a Model Template brings into a run's environment is checked when the
// sandboxes are composed: an endpoint the Vault binds its key to, and
// settings that neither carry a credential nor override the configuration.
func TestSandboxesRefuseAModelTemplateThatOverridesItsConfiguration(t *testing.T) {
	valid := command.ModelTemplate{ID: "glm-5.3-coding-plan", Vendor: "zhipu", BaseURL: "https://open.bigmodel.cn/api/anthropic", OpusModel: "glm-5.3[1m]", SonnetModel: "glm-5.3[1m]", HaikuModel: "glm-5.3-flash[1m]", FableModel: "glm-5.3[1m]", SubagentModel: "glm-5.3-flash[1m]"}
	configuration := command.ModelConfiguration{ID: "glm", Name: "GLM 5.3", Template: valid.ID, APIKey: "secret-key-value"}
	for name, change := range map[string]func(*command.ModelTemplate){
		"plain HTTP endpoint":          func(m *command.ModelTemplate) { m.BaseURL = "http://open.bigmodel.cn/api/anthropic" },
		"template sets model":          func(m *command.ModelTemplate) { m.Environment = map[string]string{"ANTHROPIC_MODEL": "other"} },
		"template sets a credential":   func(m *command.ModelTemplate) { m.Environment = map[string]string{"ANTHROPIC_AUTH_TOKEN": "x"} },
		"template sets effort":         func(m *command.ModelTemplate) { m.Environment = map[string]string{"CLAUDE_CODE_EFFORT_LEVEL": "max"} },
		"template sets a GitHub token": func(m *command.ModelTemplate) { m.Environment = map[string]string{"GH_TOKEN": "x"} },
		"template sets a vendor key":   func(m *command.ModelTemplate) { m.Environment = map[string]string{"MOONSHOT_API_KEY": "x"} },
	} {
		t.Run(name, func(t *testing.T) {
			template := valid
			change(&template)
			settings := config()
			settings.Models = catalog([]command.ModelTemplate{template}, configuration)
			err := settings.Validate()
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret-key-value")
		})
	}
	budget := valid
	budget.Environment = map[string]string{"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "262144"}
	settings := config()
	settings.Models = catalog([]command.ModelTemplate{budget}, configuration)
	require.NoError(t, settings.Validate(), "a token budget is not a credential")
}
