package marketplace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"go.uber.org/fx"
)

// PlatformConnectorEntry registers one platform Connector people install,
// authenticated by a declared Provider at one of its targets. Deployment
// configuration is the platform's registration authority.
type PlatformConnectorEntry struct {
	Description string            `json:"description" yaml:"description" toml:"description"`
	Category    string            `json:"category" yaml:"category" toml:"category"`
	Provider    string            `json:"provider" yaml:"provider" toml:"provider"`
	Target      string            `json:"target" yaml:"target" toml:"target"`
	Type        string            `json:"type" yaml:"type" toml:"type"`
	URL         string            `json:"url" yaml:"url" toml:"url"`
	Command     string            `json:"command" yaml:"command" toml:"command"`
	Arguments   []string          `json:"args" yaml:"args" toml:"args"`
	Headers     map[string]string `json:"headers" yaml:"headers" toml:"headers"`
	// CredentialEnvironment names variables whose value is the native
	// "${NAME}" reference to the credential-proxy placeholder. Deployment
	// configuration expands "${...}" itself, so the reference is built here.
	CredentialEnvironment []string `json:"credential-environment" yaml:"credential-environment" toml:"credential-environment"`
}

// platformConnectorName is the required platform Connector's native name.
const platformConnectorName = "talgent"

// PlatformConnectorConfig locates the platform Connector every Work connects
// to as its talgent MCP server, where the deployment runs one.
type PlatformConnectorConfig struct {
	// GatewayAddress is where sandboxes reach the Gateway; empty, the
	// deployment runs none and publishes no platform Connector.
	GatewayAddress string `json:"gateway-address" yaml:"gateway-address" toml:"gateway-address"`
	Description    string `json:"description" yaml:"description" toml:"description"`
	Category       string `json:"category" yaml:"category" toml:"category"`
}

func (c PlatformConnectorConfig) publication() (application.PlatformConnector, error) {
	address, err := url.Parse(strings.TrimRight(strings.TrimSpace(c.GatewayAddress), "/"))
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" || address.RawQuery != "" || address.Fragment != "" {
		return application.PlatformConnector{}, errors.New("marketplace: the platform Connector needs the Gateway's address")
	}
	// The sandbox's Credential Proxy adds the Work's Identity Token to every
	// request to the Gateway; the configuration names none.
	configuration := domain.MCPConfiguration{Type: "http", URL: address.String() + "/mcp"}
	return application.PlatformConnector{Name: platformConnectorName, Configuration: configuration,
		Presentation: domain.CatalogPresentation{Description: c.Description, Category: c.Category}}, configuration.Validate()
}

func (c PlatformConnectorEntry) publication(name string) application.PlatformConnector {
	configuration := domain.MCPConfiguration{Type: c.Type, URL: c.URL, Command: c.Command, Arguments: c.Arguments, Headers: c.Headers}
	for _, variable := range c.CredentialEnvironment {
		if configuration.Environment == nil {
			configuration.Environment = map[string]string{}
		}
		configuration.Environment[variable] = "${" + variable + "}"
	}
	// The Connector's address must lie within the declared target, which
	// publication checks against the Provider declarations.
	binding := domain.ProviderAccessBinding{Provider: c.Provider, Target: c.Target, Authentication: "credential_proxy"}
	return application.PlatformConnector{Name: name, Configuration: configuration, Provider: &binding,
		Presentation: domain.CatalogPresentation{Description: c.Description, Category: c.Category}}
}

// connectorPublications are the catalog entries the deployment registers: the
// platform Connector where it names the Gateway's address, and every platform
// Connector whose Provider the deployment enables. A Connector naming a
// Provider that is not declared at all is a configuration error.
func connectorPublications(config Config) ([]application.PlatformConnector, error) {
	publications := make([]application.PlatformConnector, 0, len(config.Connectors)+1)
	if strings.TrimSpace(config.PlatformConnector.GatewayAddress) != "" {
		platform, err := config.PlatformConnector.publication()
		if err != nil {
			return nil, err
		}
		publications = append(publications, platform)
	}
	for _, name := range slices.Sorted(maps.Keys(config.Connectors)) {
		entry := config.Connectors[name]
		provider, declared := config.Providers[entry.Provider]
		if !declared {
			return nil, fmt.Errorf("marketplace: Connector %s names Provider %q, which is not declared", name, entry.Provider)
		}
		if enabled, err := provider.enabled(entry.Provider); err != nil || !enabled {
			if err != nil {
				return nil, err
			}
			continue
		}
		publications = append(publications, entry.publication(name))
	}
	return publications, nil
}

// platformConnectorNames are the names of every Platform Connector the
// configuration declares, enabled or not, which no owner adds.
func platformConnectorNames(config Config) []string {
	return append(slices.Sorted(maps.Keys(config.Connectors)), platformConnectorName)
}

func registerPlatformConnectors(lifecycle fx.Lifecycle, app *application.Application, config Config, logger *slog.Logger) {
	lifecycle.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		publications, err := connectorPublications(config)
		if err != nil {
			return err
		}
		for _, publication := range publications {
			if err := app.EstablishPlatformConnector(ctx, publication); err != nil {
				logger.ErrorContext(ctx, "platform Connector publication failed", slog.String("connector", publication.Name), slog.Any("error", err))
				return err
			}
		}
		logger.InfoContext(ctx, "platform Connectors published", slog.Int("count", len(publications)))
		return nil
	}})
}
