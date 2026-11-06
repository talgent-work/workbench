package infrastructure_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

func TestProviderCompatibilityRequiresExplicitBindingAndProxyCompatibleConfiguration(t *testing.T) {
	binding := domain.ProviderAccessBinding{Provider: "github", Target: "github-api", Authentication: "credential_proxy"}
	configuration := domain.MCPConfiguration{Command: "github-mcp-server", Arguments: []string{"stdio"}, Environment: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"}}
	evidence, err := infrastructure.VerifyProviderConfiguration(configuration, binding)
	require.NoError(t, err)
	require.Equal(t, binding, evidence.Binding)
	require.Equal(t, configuration, evidence.Configuration)
	for _, change := range []struct {
		name  string
		apply func(*domain.MCPConfiguration, *domain.ProviderAccessBinding)
	}{
		{"no provider inferred from executable", func(_ *domain.MCPConfiguration, b *domain.ProviderAccessBinding) { b.Provider = "" }},
		{"no plaintext credential", func(c *domain.MCPConfiguration, _ *domain.ProviderAccessBinding) {
			c.Environment["GITHUB_PERSONAL_ACCESS_TOKEN"] = "real-credential-canary"
		}},
		{"no alternate target", func(c *domain.MCPConfiguration, _ *domain.ProviderAccessBinding) {
			c.Environment["GITHUB_HOST"] = "other.example"
		}},
		{"no alternate authentication", func(_ *domain.MCPConfiguration, b *domain.ProviderAccessBinding) { b.Authentication = "bearer" }},
		{"no unverified consumer", func(c *domain.MCPConfiguration, _ *domain.ProviderAccessBinding) { c.Command = "another-consumer" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			candidate, requirement := configuration.Clone(), binding
			change.apply(&candidate, &requirement)
			_, err := infrastructure.VerifyProviderConfiguration(candidate, requirement)
			require.ErrorIs(t, err, domain.ErrCredentialTargetInapplicable)
			require.NotContains(t, err.Error(), "real-credential-canary")
		})
	}
	configuration.Environment["GITHUB_PERSONAL_ACCESS_TOKEN"] = "changed"
	require.Equal(t, "${GITHUB_PERSONAL_ACCESS_TOKEN}", evidence.Configuration.Environment["GITHUB_PERSONAL_ACCESS_TOKEN"], "published compatibility evidence owns its configuration")
}
