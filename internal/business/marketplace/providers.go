package marketplace

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	scmgithub "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/github"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthprovider"
)

// ProviderConfig declares one Provider: its authorization server and client,
// the targets its one authorization serves, how each target's credential
// derives and whether target credentials expire. The github Provider is
// reached through the GitHub App adapter, every other one through OAuth with
// PKCE.
type ProviderConfig struct {
	// Enabled, default true, leaves a Provider out of a deployment without its
	// client; its platform Connectors are left out with it.
	Enabled               string                  `json:"enabled" yaml:"enabled" toml:"enabled"`
	DisplayName           string                  `json:"display-name" yaml:"display-name" toml:"display-name"`
	ClientID              string                  `json:"client-id" yaml:"client-id" toml:"client-id"`
	ClientSecret          string                  `json:"client-secret" yaml:"client-secret" toml:"client-secret"`
	AuthorizationEndpoint string                  `json:"authorization-endpoint" yaml:"authorization-endpoint" toml:"authorization-endpoint"`
	TokenEndpoint         string                  `json:"token-endpoint" yaml:"token-endpoint" toml:"token-endpoint"`
	RevocationEndpoint    string                  `json:"revocation-endpoint" yaml:"revocation-endpoint" toml:"revocation-endpoint"`
	UserinfoEndpoint      string                  `json:"userinfo-endpoint" yaml:"userinfo-endpoint" toml:"userinfo-endpoint"`
	Scopes                []string                `json:"scopes" yaml:"scopes" toml:"scopes"`
	Targets               map[string]TargetConfig `json:"targets" yaml:"targets" toml:"targets"`
	// TargetCredentials is shared (every target takes the authorization's own
	// credential) or resource-indicator (one credential per target, RFC 8707).
	TargetCredentials       string `json:"target-credentials" yaml:"target-credentials" toml:"target-credentials"`
	TargetCredentialsExpire bool   `json:"target-credentials-expire" yaml:"target-credentials-expire" toml:"target-credentials-expire"`
}

// TargetConfig is one destination of a Provider's credentials: the host
// patterns they reach and, for resource-indicator credentials, the resource.
type TargetConfig struct {
	Hosts    []string `json:"hosts" yaml:"hosts" toml:"hosts"`
	Resource string   `json:"resource" yaml:"resource" toml:"resource"`
}

func (c ProviderConfig) enabled(name string) (bool, error) {
	if strings.TrimSpace(c.Enabled) == "" {
		return true, nil
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(c.Enabled))
	if err != nil {
		return false, fmt.Errorf("marketplace: Provider %s: enabled must be boolean", name)
	}
	return enabled, nil
}

func (c ProviderConfig) declaration(name string) (domain.ProviderDeclaration, error) {
	declaration := domain.ProviderDeclaration{Name: domain.Provider(name), DisplayName: strings.TrimSpace(c.DisplayName),
		Derivation: domain.TargetCredentialDerivation(strings.TrimSpace(c.TargetCredentials)), CredentialsExpire: c.TargetCredentialsExpire,
		ObservesAccessScope: name == string(domain.ProviderGitHub)}
	for _, target := range slices.Sorted(maps.Keys(c.Targets)) {
		config := c.Targets[target]
		hosts := make([]domain.HostPattern, len(config.Hosts))
		for i, host := range config.Hosts {
			hosts[i] = domain.HostPattern(strings.ToLower(strings.TrimSpace(host)))
		}
		declaration.Targets = append(declaration.Targets, domain.ProviderTarget{Name: target, Hosts: hosts, Resource: strings.TrimSpace(config.Resource)})
	}
	if err := declaration.Validate(); err != nil {
		return domain.ProviderDeclaration{}, fmt.Errorf("marketplace: Provider %s: %w", name, err)
	}
	if name == string(domain.ProviderGitHub) && declaration.Derivation != domain.SharedTargetCredential {
		return domain.ProviderDeclaration{}, fmt.Errorf("marketplace: Provider github takes shared target credentials")
	}
	return declaration, nil
}

func (c ProviderConfig) client(name string) oauthprovider.Client {
	return oauthprovider.Client{Provider: domain.Provider(name), ClientID: strings.TrimSpace(c.ClientID), ClientSecret: strings.TrimSpace(c.ClientSecret),
		AuthorizationEndpoint: strings.TrimSpace(c.AuthorizationEndpoint), TokenEndpoint: strings.TrimSpace(c.TokenEndpoint),
		RevocationEndpoint: strings.TrimSpace(c.RevocationEndpoint), UserinfoEndpoint: strings.TrimSpace(c.UserinfoEndpoint), Scopes: c.Scopes}
}

// providerCatalog is the Provider part of the PlatformConfigurationPort, read
// from the deployment configuration once.
type providerCatalog struct {
	declarations []domain.ProviderDeclaration
	clients      map[domain.Provider]oauthprovider.Client
}

var _ application.ProviderDeclarations = (*providerCatalog)(nil)

func newProviderCatalog(config Config) (*providerCatalog, error) {
	catalog := &providerCatalog{clients: map[domain.Provider]oauthprovider.Client{}}
	targets := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(config.Providers)) {
		provider := config.Providers[name]
		enabled, err := provider.enabled(name)
		if err != nil || !enabled {
			if err != nil {
				return nil, err
			}
			continue
		}
		declaration, err := provider.declaration(name)
		if err != nil {
			return nil, err
		}
		// One target name means one Vault credential in a Work.
		for _, target := range declaration.Targets {
			if owner, taken := targets[target.Name]; taken {
				return nil, fmt.Errorf("marketplace: target %s is declared by Providers %s and %s", target.Name, owner, name)
			}
			targets[target.Name] = name
		}
		client := provider.client(name)
		if err := client.Validate(); err != nil {
			return nil, err
		}
		catalog.declarations = append(catalog.declarations, declaration)
		catalog.clients[declaration.Name] = client
	}
	return catalog, nil
}

func (c *providerCatalog) Providers() []domain.ProviderDeclaration {
	return slices.Clone(c.declarations)
}

func (c *providerCatalog) Provider(name domain.Provider) (domain.ProviderDeclaration, bool) {
	index := slices.IndexFunc(c.declarations, func(d domain.ProviderDeclaration) bool { return d.Name == name })
	if index < 0 {
		return domain.ProviderDeclaration{}, false
	}
	return c.declarations[index], true
}

// oauthClients are the clients of every declared Provider apart from GitHub.
func (c *providerCatalog) oauthClients() []oauthprovider.Client {
	var clients []oauthprovider.Client
	for _, declaration := range c.declarations {
		if declaration.Name != domain.ProviderGitHub {
			clients = append(clients, c.clients[declaration.Name])
		}
	}
	return clients
}

// gitHub completes the GitHub App adapter's settings from the github
// declaration: the adapter is enabled exactly when github is declared.
func (c *providerCatalog) gitHub(adapter scmgithub.Config, cipherKey string) scmgithub.Config {
	client, declared := c.clients[domain.ProviderGitHub]
	adapter.Enabled = declared
	adapter.OAuthClientID, adapter.OAuthClientSecret = client.ClientID, client.ClientSecret
	adapter.AuthorizationEndpoint, adapter.TokenEndpoint = client.AuthorizationEndpoint, client.TokenEndpoint
	adapter.TokenCipherKey = cipherKey
	return adapter
}

// providerPorts selects each declared Provider's adapter.
type providerPorts map[domain.Provider]domain.ProviderAuthorizationPort

func (p providerPorts) For(provider domain.Provider) (domain.ProviderAuthorizationPort, bool) {
	port, ok := p[provider]
	return port, ok
}
