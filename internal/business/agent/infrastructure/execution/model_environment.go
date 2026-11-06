package execution

import "maps"

// sandboxSecretEnvironment names credentials the sandbox only ever receives as
// credential-proxy placeholders.
var sandboxSecretEnvironment = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN", "OPENSANDBOX_EGRESS_AUTH_TOKEN", "OPEN_SANDBOX_API_KEY", "OPENSANDBOX_API_KEY", "OPENSANDBOX_SERVER_API_KEY"}

// modelEnvironment expands the Model Template the Work's Model Configuration
// uses at this start: its endpoint, Claude Code's model tiers, the Work's main
// model when the template names one, the subagent model, the default effort and the
// vendor's Harness settings.
func modelEnvironment(env map[string]string, template ModelTemplate) {
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
