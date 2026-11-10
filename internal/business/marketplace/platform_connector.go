package marketplace

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	"go.uber.org/fx"
)

// ProviderConnectorConfig registers one Provider-authenticated catalog entry.
// Deployment configuration is the platform's registration authority.
type ProviderConnectorConfig struct {
	Name           string            `json:"name" yaml:"name" toml:"name"`
	Description    string            `json:"description" yaml:"description" toml:"description"`
	Category       string            `json:"category" yaml:"category" toml:"category"`
	Provider       string            `json:"provider" yaml:"provider" toml:"provider"`
	Target         string            `json:"target" yaml:"target" toml:"target"`
	Authentication string            `json:"authentication" yaml:"authentication" toml:"authentication"`
	Type           string            `json:"type" yaml:"type" toml:"type"`
	URL            string            `json:"url" yaml:"url" toml:"url"`
	Command        string            `json:"command" yaml:"command" toml:"command"`
	Arguments      []string          `json:"args" yaml:"args" toml:"args"`
	Headers        map[string]string `json:"headers" yaml:"headers" toml:"headers"`
	// CredentialEnvironment names variables whose value is the native
	// "${NAME}" reference to the credential-proxy placeholder. Deployment
	// configuration expands "${...}" itself, so the reference is built here.
	CredentialEnvironment []string `json:"credential-environment" yaml:"credential-environment" toml:"credential-environment"`
}

// PlatformConnectorConfig locates the platform Connector every Work connects
// to as its talgent MCP server, where the deployment runs one.
type PlatformConnectorConfig struct {
	// GatewayAddress is where sandboxes reach the Gateway; empty, the
	// deployment runs none and publishes no platform Connector.
	GatewayAddress string `json:"gateway-address" yaml:"gateway-address" toml:"gateway-address"`
	Description    string `json:"description" yaml:"description" toml:"description"`
	Category       string `json:"category" yaml:"category" toml:"category"`
}

func (c PlatformConnectorConfig) publication() (application.ConnectorPublication, error) {
	address, err := url.Parse(strings.TrimRight(strings.TrimSpace(c.GatewayAddress), "/"))
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" || address.RawQuery != "" || address.Fragment != "" {
		return application.ConnectorPublication{}, errors.New("marketplace: the platform Connector needs the Gateway's address")
	}
	// The sandbox's Credential Proxy adds the Work's Identity Token to every
	// request to the Gateway; the configuration names none.
	configuration := domain.MCPConfiguration{Type: "http", URL: address.String() + "/mcp"}
	return application.ConnectorPublication{Name: "talgent", Configuration: configuration,
		Presentation: domain.CatalogPresentation{Description: c.Description, Category: c.Category}}, configuration.Validate()
}

func (c ProviderConnectorConfig) publication() (application.ConnectorPublication, error) {
	configuration := domain.MCPConfiguration{Type: c.Type, URL: c.URL, Command: c.Command, Arguments: c.Arguments, Headers: c.Headers}
	for _, name := range c.CredentialEnvironment {
		if configuration.Environment == nil {
			configuration.Environment = map[string]string{}
		}
		configuration.Environment[name] = "${" + name + "}"
	}
	binding := domain.ProviderAccessBinding{Provider: c.Provider, Target: c.Target, Authentication: c.Authentication}
	evidence, err := infrastructure.VerifyProviderConfiguration(configuration, binding)
	if err != nil {
		return application.ConnectorPublication{}, err
	}
	return application.ConnectorPublication{Name: c.Name, Configuration: configuration, Provider: &binding, Evidence: &evidence,
		Presentation: domain.CatalogPresentation{Description: c.Description, Category: c.Category}}, nil
}

// connectorPublications are the catalog entries the deployment registers: the
// platform Connector where it names the Gateway's address, and every Provider
// Connector.
func connectorPublications(config Config) ([]application.ConnectorPublication, error) {
	publications := make([]application.ConnectorPublication, 0, len(config.ProviderConnectors)+1)
	if strings.TrimSpace(config.PlatformConnector.GatewayAddress) != "" {
		platform, err := config.PlatformConnector.publication()
		if err != nil {
			return nil, err
		}
		publications = append(publications, platform)
	}
	for _, registration := range config.ProviderConnectors {
		publication, err := registration.publication()
		if err != nil {
			return nil, err
		}
		publications = append(publications, publication)
	}
	return publications, nil
}

func registerPlatformConnectors(lifecycle fx.Lifecycle, app *application.Application, config Config, logger *slog.Logger) {
	lifecycle.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		publications, err := connectorPublications(config)
		if err != nil {
			return err
		}
		for _, publication := range publications {
			if err := app.PublishConnector(ctx, publication); err != nil {
				logger.ErrorContext(ctx, "platform Connector publication failed", slog.String("connector", publication.Name), slog.Any("error", err))
				return err
			}
		}
		logger.InfoContext(ctx, "platform Connectors published", slog.Int("count", len(publications)))
		return nil
	}})
}
