package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// ProviderDeclarations is the Provider part of the PlatformConfigurationPort:
// the Providers the deployment declares. Only the deployment adds one.
type ProviderDeclarations interface {
	// Providers lists every declaration, ordered by name.
	Providers() []domain.ProviderDeclaration
	Provider(domain.Provider) (domain.ProviderDeclaration, bool)
}

// ProviderAuthorizationPorts selects the adapter that reaches a declared
// Provider for the authorization's Domain-owned Port.
type ProviderAuthorizationPorts interface {
	For(domain.Provider) (domain.ProviderAuthorizationPort, bool)
}

// OAuthProviderPort runs the OAuth authorization code flow with PKCE against
// the authorization server of every declared Provider apart from GitHub.
type OAuthProviderPort interface {
	BeginProviderAuthorization(ctx context.Context, input BeginProviderAuthorizationInput) (BeginProviderAuthorizationResult, error)
	CompleteProviderAuthorization(ctx context.Context, input CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error)
}

// CredentialCipher protects the credentials Talgent holds for an owner at
// rest; plaintext leaves it only for trusted supply.
type CredentialCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// ListProviders lists every declared Provider with the owner's connection.
func (a *PersonalConnections) ListProviders(ctx context.Context, actor string) ([]query.DeclaredProvider, error) {
	if actor == "" {
		return nil, ErrUnauthenticated
	}
	declarations := a.providers.Providers()
	result := make([]query.DeclaredProvider, 0, len(declarations))
	for _, declaration := range declarations {
		state, err := a.queries.GetProviderConnectionState(ctx, actor, declaration.Name)
		if err != nil {
			return nil, err
		}
		provider := query.DeclaredProvider{Name: string(declaration.Name), DisplayName: declaration.DisplayName,
			TargetCredentialsExpire: declaration.CredentialsExpire, ObservesAccessScope: declaration.ObservesAccessScope, Connection: state}
		for _, target := range declaration.Targets {
			hosts := make([]string, len(target.Hosts))
			for i, host := range target.Hosts {
				hosts[i] = string(host)
			}
			paths := make([]string, len(target.Paths))
			for i, p := range target.Paths {
				paths[i] = string(p)
			}
			provider.Targets = append(provider.Targets, query.ProviderTarget{Name: target.Name, Hosts: hosts, Paths: paths})
		}
		result = append(result, provider)
	}
	return result, nil
}

// declared refuses a Provider the deployment does not declare.
func (a *PersonalConnections) declared(provider domain.Provider) (domain.ProviderDeclaration, error) {
	declaration, ok := a.providers.Provider(provider)
	if !ok {
		return domain.ProviderDeclaration{}, ErrProviderUnavailable
	}
	return declaration, nil
}

// establishment selects how the Provider authorizes: GitHub through its App,
// every other Provider through OAuth.
func (a *PersonalConnections) establishment(provider domain.Provider) (OAuthProviderPort, error) {
	if _, err := a.declared(provider); err != nil {
		return nil, err
	}
	if provider == domain.ProviderGitHub {
		return a.github, nil
	}
	if a.oauth == nil {
		return nil, ErrProviderUnavailable
	}
	return a.oauth, nil
}
