// Package providertest declares Providers as a deployment does, for tests
// that run the Marketplace.
package providertest

import "github.com/talgent-work/workbench/internal/business/marketplace/domain"

// Declarations are a deployment's Provider declarations.
type Declarations []domain.ProviderDeclaration

func (d Declarations) Providers() []domain.ProviderDeclaration { return d }

func (d Declarations) Provider(name domain.Provider) (domain.ProviderDeclaration, bool) {
	for _, declaration := range d {
		if declaration.Name == name {
			return declaration, true
		}
	}
	return domain.ProviderDeclaration{}, false
}

// GitHub is the github declaration of the deployment's base configuration:
// one authorization serving GitHub's API and remote MCP server with its own
// credential.
func GitHub() domain.ProviderDeclaration {
	return domain.ProviderDeclaration{Name: domain.ProviderGitHub, DisplayName: "GitHub", Derivation: domain.SharedTargetCredential,
		CredentialsExpire: true, ObservesAccessScope: true, Targets: []domain.ProviderTarget{
			{Name: "github-api", Hosts: []domain.HostPattern{"api.github.com"}},
			{Name: "github-remote-mcp", Hosts: []domain.HostPattern{"api.githubcopilot.com"}},
		}}
}

// Enterprise is an enterprise identity provider whose target credentials,
// one per internal service, never expire.
func Enterprise() domain.ProviderDeclaration {
	return domain.ProviderDeclaration{Name: "example-sso", DisplayName: "Example SSO", Derivation: domain.SharedTargetCredential,
		Targets: []domain.ProviderTarget{{Name: "example-wiki", Hosts: []domain.HostPattern{"wiki.example.com"}}}}
}
