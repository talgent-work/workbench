package infrastructure

import (
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// VerifyProviderConfiguration validates an explicitly registered relationship.
// It never derives a Provider from a URL, executable, or OAuth server name.
func VerifyProviderConfiguration(configuration domain.MCPConfiguration, binding domain.ProviderAccessBinding) (domain.ProviderCompatibility, error) {
	if binding.Provider != "github" || binding.Authentication != "credential_proxy" {
		return domain.ProviderCompatibility{}, domain.ErrCredentialTargetInapplicable
	}
	switch binding.Target {
	case "github-remote-mcp":
		if configuration.Type != "http" || (configuration.URL != "https://api.githubcopilot.com/mcp/" && configuration.URL != "https://api.githubcopilot.com/mcp") || configuration.Command != "" || len(configuration.Environment) > 0 {
			return domain.ProviderCompatibility{}, domain.ErrCredentialTargetInapplicable
		}
		// The consumer supplies only the public proxy marker, never a real token.
		for key, value := range configuration.Headers {
			if key != "Authorization" || value != "Bearer ${GITHUB_PERSONAL_ACCESS_TOKEN}" {
				return domain.ProviderCompatibility{}, domain.ErrCredentialTargetInapplicable
			}
		}
	case "github-api":
		// The supported MCP consumer's contract is deliberately narrow. Other CLI
		// tools use the independently selected outbound API target, without a registry.
		if (configuration.Type != "" && configuration.Type != "stdio") || configuration.Command != "github-mcp-server" || configuration.URL != "" || len(configuration.Arguments) != 1 || configuration.Arguments[0] != "stdio" || len(configuration.Headers) > 0 {
			return domain.ProviderCompatibility{}, domain.ErrCredentialTargetInapplicable
		}
		for key, value := range configuration.Environment {
			if key != "GITHUB_PERSONAL_ACCESS_TOKEN" || value != "${GITHUB_PERSONAL_ACCESS_TOKEN}" {
				return domain.ProviderCompatibility{}, domain.ErrCredentialTargetInapplicable
			}
		}
	default:
		return domain.ProviderCompatibility{}, domain.ErrCredentialTargetInapplicable
	}
	return domain.ProviderCompatibility{Configuration: configuration.Clone(), Binding: binding}, nil
}
