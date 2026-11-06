package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func validTemplate() ModelTemplate {
	return ModelTemplate{ID: "glm-5.3-coding-plan", Vendor: "zhipu", BaseURL: "https://open.bigmodel.cn/api/anthropic", OpusModel: "glm-5.3[1m]", SonnetModel: "glm-5.3[1m]", HaikuModel: "glm-5.3-flash[1m]", FableModel: "glm-5.3[1m]", SubagentModel: "glm-5.3-flash[1m]", ThinkingLevels: []string{"low", "high", "max"}, DefaultThinkingLevel: "max", Environment: map[string]string{"API_TIMEOUT_MS": "3000000"}}
}

func validConfiguration() ModelConfiguration {
	return ModelConfiguration{ID: "glm", Name: "GLM 5.3", Template: "glm-5.3-coding-plan", APIKey: "secret-key-value"}
}

func TestModelCatalogLoadsMountedFilesInPlatformOrder(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates.yml")
	configurations := filepath.Join(dir, "configurations.yml")
	require.NoError(t, os.WriteFile(templates, []byte(`templates:
  - id: deepseek-flash
    vendor: deepseek
    base-url: https://api.deepseek.com/anthropic
    opus-model: deepseek-flash[1m]
    sonnet-model: deepseek-flash[1m]
    haiku-model: deepseek-flash
    fable-model: deepseek-flash[1m]
    subagent-model: deepseek-flash
    thinking-levels: [low, high, max]
    default-thinking-level: max
  - id: glm-5.3-coding-plan
    vendor: zhipu
    base-url: https://open.bigmodel.cn/api/anthropic
    opus-model: glm-5.3[1m]
    sonnet-model: glm-5.3[1m]
    haiku-model: glm-5.3-flash[1m]
    fable-model: glm-5.3[1m]
    subagent-model: glm-5.3-flash[1m]
    environment:
      API_TIMEOUT_MS: "3000000"
`), 0600))
	require.NoError(t, os.WriteFile(configurations, []byte(`configurations:
  - id: deepseek
    name: DeepSeek
    template: deepseek-flash
    api-key: deepseek-key
  - id: glm-personal
    name: GLM 5.3
    template: glm-5.3-coding-plan
    api-key: glm-key
  - id: glm-team
    name: GLM 5.3 (team)
    template: glm-5.3-coding-plan
    api-key: glm-team-key
`), 0600))
	catalog, err := LoadModelCatalog(ModelCatalogConfig{TemplatesPath: templates, ConfigurationsPath: configurations})
	require.NoError(t, err)
	provided := catalog.Provided()
	require.Len(t, provided, 3)
	require.Equal(t, "deepseek", provided[0].Configuration.ID, "platform order decides the default")
	require.Equal(t, provided[1].Template, provided[2].Template, "several configurations may use one template")
	team, ok := catalog.Resolve("glm-team")
	require.True(t, ok)
	require.Equal(t, "glm-team-key", team.Configuration.APIKey)
	require.Equal(t, "3000000", team.Template.Environment["API_TIMEOUT_MS"])
	_, ok = catalog.Resolve("withdrawn")
	require.False(t, ok)
}

func TestModelCatalogRejectsInvalidPlatformData(t *testing.T) {
	for name, change := range map[string]func(*ModelTemplate, *ModelConfiguration){
		"unknown template":         func(_ *ModelTemplate, c *ModelConfiguration) { c.Template = "missing" },
		"unknown vendor":           func(m *ModelTemplate, _ *ModelConfiguration) { m.Vendor = "doubao" },
		"missing key":              func(_ *ModelTemplate, c *ModelConfiguration) { c.APIKey = " " },
		"missing name":             func(_ *ModelTemplate, c *ModelConfiguration) { c.Name = "" },
		"invalid configuration id": func(_ *ModelTemplate, c *ModelConfiguration) { c.ID = "GLM 5.3" },
		"plain HTTP endpoint":      func(m *ModelTemplate, _ *ModelConfiguration) { m.BaseURL = "http://open.bigmodel.cn/api/anthropic" },
		"missing haiku model":      func(m *ModelTemplate, _ *ModelConfiguration) { m.HaikuModel = "" },
		"invalid main model":       func(m *ModelTemplate, _ *ModelConfiguration) { m.MainModel = " auto" },
		"unknown thinking level":   func(m *ModelTemplate, _ *ModelConfiguration) { m.ThinkingLevels = []string{"low", "ultra"} },
		"default outside levels":   func(m *ModelTemplate, _ *ModelConfiguration) { m.ThinkingLevels = []string{"low", "high"} },
		"template sets model": func(m *ModelTemplate, _ *ModelConfiguration) {
			m.Environment = map[string]string{"ANTHROPIC_MODEL": "other"}
		},
		"template sets a credential": func(m *ModelTemplate, _ *ModelConfiguration) {
			m.Environment = map[string]string{"ANTHROPIC_AUTH_TOKEN": "x"}
		},
		"template sets effort": func(m *ModelTemplate, _ *ModelConfiguration) {
			m.Environment = map[string]string{"CLAUDE_CODE_EFFORT_LEVEL": "max"}
		},
		"template sets a GitHub token": func(m *ModelTemplate, _ *ModelConfiguration) { m.Environment = map[string]string{"GH_TOKEN": "x"} },
		"template sets a vendor key": func(m *ModelTemplate, _ *ModelConfiguration) {
			m.Environment = map[string]string{"MOONSHOT_API_KEY": "x"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			template, configuration := validTemplate(), validConfiguration()
			change(&template, &configuration)
			_, err := NewModelCatalog([]ModelTemplate{template}, []ModelConfiguration{configuration})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret-key-value")
		})
	}
	template := validTemplate()
	template.Environment = map[string]string{"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "262144"}
	_, err := NewModelCatalog([]ModelTemplate{template}, []ModelConfiguration{validConfiguration()})
	require.NoError(t, err, "a token budget is not a credential")
	_, err = NewModelCatalog([]ModelTemplate{validTemplate(), validTemplate()}, []ModelConfiguration{validConfiguration()})
	require.ErrorContains(t, err, "declared twice")
	_, err = NewModelCatalog([]ModelTemplate{validTemplate()}, []ModelConfiguration{validConfiguration(), validConfiguration()})
	require.ErrorContains(t, err, "declared twice")
	_, err = NewModelCatalog([]ModelTemplate{validTemplate()}, nil)
	require.Error(t, err, "a platform without model configurations cannot start Works")
}

func TestModelCatalogReadErrorsNeverQuoteTheSecret(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates.yml")
	configurations := filepath.Join(dir, "configurations.yml")
	require.NoError(t, os.WriteFile(templates, []byte("templates: []\n"), 0600))
	require.NoError(t, os.WriteFile(configurations, []byte("configurations:\n  - id: glm\n    api-key: secret-key-value\n    unexpected: secret-key-value\n"), 0600))
	_, err := LoadModelCatalog(ModelCatalogConfig{TemplatesPath: templates, ConfigurationsPath: configurations})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-key-value")
}
