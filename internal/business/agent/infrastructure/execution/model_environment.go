package execution

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// sandboxSecretEnvironment names credentials the sandbox only ever receives as
// credential-proxy placeholders.
var sandboxSecretEnvironment = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN", "OPENSANDBOX_EGRESS_AUTH_TOKEN", "OPEN_SANDBOX_API_KEY", "OPENSANDBOX_API_KEY", "OPENSANDBOX_SERVER_API_KEY"}

// Names that carry a credential, such as a vendor's own MOONSHOT_API_KEY; a
// template's settings are visible in the sandbox and never hold one.
var credentialName = regexp.MustCompile(`(?i)(^|_)(KEY|TOKEN|SECRET|PASSWORD|CREDENTIALS?)$`)

// Model selection and credentials are derived from the Model Configuration;
// a template's extra Harness settings cannot override them.
var modelManagedEnvironment = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL",
	"CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_EFFORT_LEVEL",
}

// errModelConfigurationNotProvided reports that an admitted start names a Model
// Configuration the platform no longer provides, so no sandbox can expand it.
var errModelConfigurationNotProvided = fmt.Errorf("%w: it is not provided", domain.ErrModelConfigurationWithdrawn)

// validateModelTemplates checks what every provided Model Template brings
// into a run's environment: an endpoint the credential Vault can bind its key
// to, and settings that neither carry a credential nor override what the
// Model Configuration decides.
func validateModelTemplates(models command.PlatformCatalogPort) error {
	if models == nil {
		return errors.New("agent: the platform's model configurations are required")
	}
	for _, model := range models.ProvidedModels() {
		template := model.Template
		if _, err := credentialURL(template.BaseURL); err != nil {
			return fmt.Errorf("agent: model template %q: %w", template.ID, err)
		}
		for key := range template.Environment {
			if slices.Contains(modelManagedEnvironment, key) || slices.Contains(sandboxSecretEnvironment, key) || credentialName.MatchString(key) {
				return fmt.Errorf("agent: model template %q cannot set %s", template.ID, key)
			}
		}
	}
	return nil
}

// modelEnvironment expands the Model Template the Work's Model Configuration
// uses at this start: its endpoint, Claude Code's model tiers, the Work's main
// model when the template names one, the subagent model, the default effort and the
// vendor's Harness settings.
func modelEnvironment(env map[string]string, template command.ModelTemplate) {
	maps.Copy(env, template.Environment)
	env["ANTHROPIC_BASE_URL"] = template.BaseURL
	env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = template.OpusModel
	env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = template.SonnetModel
	env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = template.HaikuModel
	env["ANTHROPIC_DEFAULT_FABLE_MODEL"] = template.FableModel
	if template.MainModel != "" {
		env["ANTHROPIC_MODEL"] = template.MainModel
	}
	env["CLAUDE_CODE_SUBAGENT_MODEL"] = template.SubagentModel
	if template.DefaultThinkingLevel != "" {
		env["CLAUDE_CODE_EFFORT_LEVEL"] = template.DefaultThinkingLevel
	}
}
