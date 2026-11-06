package infrastructure

import (
	"encoding/json"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func installationToDO(plugin *domain.InstalledPlugin, encrypted string) installedPluginDO {
	return installedPluginDO{ID: plugin.ID, UserID: plugin.UserID, Slug: plugin.Slug, State: uint8(plugin.State), Configuration: encrypted, Version: plugin.Version}
}

func installationFromDO(row installedPluginDO, manual domain.Configuration) (*domain.InstalledPlugin, error) {
	plugin := &domain.InstalledPlugin{ID: row.ID, UserID: row.UserID, Slug: row.Slug, State: domain.InstallationState(row.State), ManualConfiguration: manual.Clone(), Version: row.Version}
	if row.Version < 1 {
		return nil, domain.ErrInvalidInstallation
	}
	if err := plugin.Validate(); err != nil {
		return nil, err
	}
	return plugin, nil
}

func installationReadModel(row installedPluginDO, manual domain.Configuration) query.InstalledPlugin {
	return query.InstalledPlugin{UserID: row.UserID, Slug: row.Slug, Enabled: row.State == uint8(domain.Enabled), Configuration: manual.Clone(), Version: row.Version}
}

func decodeConfiguration(encoded []byte) (domain.Configuration, error) {
	var values map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, errors.New("marketplace: corrupt stored configuration")
	}
	result := make(domain.Configuration, len(values))
	for name, value := range values {
		switch typed := value.(type) {
		case string, float64, bool:
			result[name] = typed
		case []any:
			items := make([]string, len(typed))
			for index, item := range typed {
				text, ok := item.(string)
				if !ok {
					return nil, errors.New("marketplace: corrupt stored configuration")
				}
				items[index] = text
			}
			result[name] = items
		default:
			return nil, errors.New("marketplace: corrupt stored configuration")
		}
	}
	return result, nil
}

var connectorAuthentication = map[string]uint8{domain.WorkIdentityAuthentication: 1, domain.ProviderAuthorizationAuthentication: 2}

func connectorToDO(connector *domain.Connector) (connectorDO, error) {
	configuration := connectorConfigurationRecord{Command: connector.Configuration.Command, Arguments: connector.Configuration.Arguments, Type: connector.Configuration.Type, URL: connector.Configuration.URL, Environment: connector.Configuration.Environment, Headers: connector.Configuration.Headers}
	if binding := connector.ProviderAccessBinding; binding != nil {
		configuration.Provider = &providerAccessRecord{Provider: binding.Provider, Target: binding.Target, Authentication: binding.Authentication}
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return connectorDO{}, err
	}
	return connectorDO{ID: connector.ID, Name: connector.Name, State: uint8(connector.State), Required: connector.Required,
		Description: connector.Presentation.Description, Category: connector.Presentation.Category,
		Configuration: string(encoded), Authentication: connectorAuthentication[connector.Authentication], Version: connector.Version}, nil
}

func connectorFromDO(row connectorDO) (*domain.Connector, error) {
	if row.Version < 1 {
		return nil, domain.ErrInvalidConnector
	}
	var configuration connectorConfigurationRecord
	if err := json.Unmarshal([]byte(row.Configuration), &configuration); err != nil {
		return nil, errors.New("marketplace: corrupt Connector configuration")
	}
	connector := &domain.Connector{ID: row.ID, Name: row.Name, State: domain.ConnectorState(row.State), Required: row.Required,
		Presentation:  domain.CatalogPresentation{Description: row.Description, Category: row.Category},
		Configuration: domain.MCPConfiguration{Command: configuration.Command, Arguments: configuration.Arguments, Type: configuration.Type, URL: configuration.URL, Environment: configuration.Environment, Headers: configuration.Headers},
		Version:       row.Version}
	for name, value := range connectorAuthentication {
		if value == row.Authentication {
			connector.Authentication = name
		}
	}
	if binding := configuration.Provider; binding != nil {
		connector.ProviderAccessBinding = &domain.ProviderAccessBinding{Provider: binding.Provider, Target: binding.Target, Authentication: binding.Authentication}
	}
	return connector, connector.Validate()
}

func installedConnectorToDO(installation *domain.InstalledConnector) installedConnectorDO {
	return installedConnectorDO{ID: installation.ID, UserID: installation.UserID, ConnectorID: installation.ConnectorID, State: uint8(installation.State), Version: installation.Version}
}

func installedConnectorFromDO(row installedConnectorDO) (*domain.InstalledConnector, error) {
	if row.Version < 1 {
		return nil, domain.ErrInvalidInstallation
	}
	installation := &domain.InstalledConnector{ID: row.ID, UserID: row.UserID, ConnectorID: row.ConnectorID, State: domain.InstallationState(row.State), Version: row.Version}
	return installation, installation.Validate()
}
