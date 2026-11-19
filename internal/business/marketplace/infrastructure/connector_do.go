package infrastructure

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type connectorDO struct {
	ID             string `xorm:"id pk"`
	OwnerKind      uint8  `xorm:"owner_kind"`
	OwnerID        string `xorm:"owner_id"`
	Name           string `xorm:"name"`
	State          uint8  `xorm:"state"`
	Required       bool   `xorm:"required"`
	Description    string `xorm:"description"`
	Category       string `xorm:"category"`
	IconMediaType  string `xorm:"icon_media_type"`
	Icon           []byte `xorm:"icon"`
	IconDigest     string `xorm:"icon_digest"`
	Configuration  string `xorm:"configuration"`
	Authentication uint8  `xorm:"authentication"`
	PublishedAt    int64  `xorm:"published_at"`
	Version        int    `xorm:"version"`
	CreatedAt      int64  `xorm:"created_at"`
	UpdatedAt      int64  `xorm:"updated_at"`
	DeletedAt      int64  `xorm:"deleted_at"`
}

func (connectorDO) TableName() string { return "marketplace_connector" }

// connectorColumns are the facts every read needs; the icon image itself is
// read only with the aggregate.
var connectorColumns = []string{"id", "owner_kind", "owner_id", "name", "state", "required", "description", "category", "icon_media_type", "icon_digest",
	"configuration", "authentication", "published_at", "version"}

var connectorAggregateColumns = append([]string{"icon"}, connectorColumns...)

// connectorChangeColumns are what a saved change may replace; owner, name,
// requirement and authentication are fixed for life.
var connectorChangeColumns = []string{"state", "description", "category", "icon_media_type", "icon", "icon_digest", "configuration", "published_at", "updated_at"}

type connectorConfigurationRecord struct {
	Type        string                `json:"type,omitempty"`
	URL         string                `json:"url,omitempty"`
	Environment map[string]string     `json:"env,omitempty"`
	Headers     map[string]string     `json:"headers,omitempty"`
	Provider    *providerAccessRecord `json:"provider,omitempty"`
	Command     string                `json:"command,omitempty"`
	Arguments   []string              `json:"args,omitempty"`
}

type providerAccessRecord struct {
	Provider       string `json:"provider"`
	Target         string `json:"target"`
	Authentication string `json:"authentication"`
}

type installedConnectorDO struct {
	ID          string `xorm:"id pk"`
	OwnerID     string `xorm:"owner_id"`
	ConnectorID string `xorm:"connector_id"`
	State       uint8  `xorm:"state"`
	Version     int    `xorm:"version"`
	CreatedAt   int64  `xorm:"created_at"`
	UpdatedAt   int64  `xorm:"updated_at"`
	DeletedAt   int64  `xorm:"deleted_at"`
}

func (installedConnectorDO) TableName() string { return "marketplace_installed_connector" }

var installedConnectorColumns = []string{"id", "owner_id", "connector_id", "state", "version"}

var connectorAuthentication = map[string]uint8{domain.WorkIdentityAuthentication: 1, domain.ProviderAuthorizationAuthentication: 2, domain.NoAuthentication: 3}

var connectorStates = map[domain.ConnectorState]string{domain.ConnectorPrivate: query.ConnectorPrivate, domain.ConnectorPublished: query.ConnectorPublished, domain.ConnectorWithdrawn: query.ConnectorWithdrawn}

var ownerKinds = map[domain.OwnerKind]string{domain.OwnerPlatform: query.OwnerPlatform, domain.OwnerPerson: query.OwnerPerson, domain.OwnerApplication: query.OwnerApplication}

func connectorToDO(connector *domain.Connector) (connectorDO, error) {
	configuration := connectorConfigurationRecord{Command: connector.Configuration.Command, Arguments: connector.Configuration.Arguments, Type: connector.Configuration.Type, URL: connector.Configuration.URL, Environment: connector.Configuration.Environment, Headers: connector.Configuration.Headers}
	if binding := connector.ProviderAccessBinding; binding != nil {
		configuration.Provider = &providerAccessRecord{Provider: binding.Provider, Target: binding.Target, Authentication: binding.Authentication}
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return connectorDO{}, err
	}
	row := connectorDO{ID: connector.ID, OwnerKind: uint8(connector.Owner.Kind), OwnerID: connector.Owner.ID, Name: connector.Name, State: uint8(connector.State), Required: connector.Required,
		Description: connector.Presentation.Description, Category: connector.Presentation.Category,
		Configuration: string(encoded), Authentication: connectorAuthentication[connector.Authentication], Version: connector.Version}
	if !connector.PublishedAt.IsZero() {
		row.PublishedAt = connector.PublishedAt.UnixMilli()
	}
	if icon := connector.Presentation.Icon; icon != nil {
		row.IconMediaType, row.Icon, row.IconDigest = icon.MediaType, icon.Data, icon.Digest()
	}
	return row, nil
}

func connectorFromDO(row connectorDO) (*domain.Connector, error) {
	if row.Version < 1 {
		return nil, domain.ErrInvalidConnector
	}
	configuration, err := decodeConnectorConfiguration(row.Configuration)
	if err != nil {
		return nil, err
	}
	connector := &domain.Connector{ID: row.ID, Owner: domain.ConnectorOwner{Kind: domain.OwnerKind(row.OwnerKind), ID: row.OwnerID}, Name: row.Name,
		State: domain.ConnectorState(row.State), Required: row.Required,
		Presentation:  domain.CatalogPresentation{Description: row.Description, Category: row.Category},
		Configuration: domain.MCPConfiguration{Command: configuration.Command, Arguments: configuration.Arguments, Type: configuration.Type, URL: configuration.URL, Environment: configuration.Environment, Headers: configuration.Headers},
		Version:       row.Version}
	if row.PublishedAt > 0 {
		connector.PublishedAt = time.UnixMilli(row.PublishedAt).UTC()
	}
	if row.IconMediaType != "" {
		connector.Presentation.Icon = &domain.ConnectorIcon{MediaType: row.IconMediaType, Data: row.Icon}
	}
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

func catalogConnectorFromDO(row connectorDO) (query.CatalogConnector, error) {
	configuration, err := decodeConnectorConfiguration(row.Configuration)
	if err != nil {
		return query.CatalogConnector{}, err
	}
	item := query.CatalogConnector{ID: row.ID, Name: row.Name, Description: row.Description, Category: row.Category, Required: row.Required,
		Owner: query.ConnectorOwner{Kind: ownerKinds[domain.OwnerKind(row.OwnerKind)], ID: row.OwnerID}, State: connectorStates[domain.ConnectorState(row.State)],
		IconDigest: row.IconDigest, Version: row.Version, PublishedAtMillis: row.PublishedAt,
		Configuration: query.ConnectorConfiguration{Type: configuration.Type, URL: configuration.URL, Command: configuration.Command,
			Arguments: configuration.Arguments, Environment: configuration.Environment, Headers: configuration.Headers}}
	if item.Owner.Kind == "" || item.State == "" {
		return query.CatalogConnector{}, errors.New("marketplace: corrupt Connector owner or state")
	}
	for name, value := range connectorAuthentication {
		if value == row.Authentication {
			item.Authentication = name
		}
	}
	if binding := configuration.Provider; binding != nil {
		item.Provider = &query.ConnectorProvider{Provider: binding.Provider, Target: binding.Target, Authentication: binding.Authentication}
	}
	return item, nil
}

func decodeConnectorConfiguration(raw string) (connectorConfigurationRecord, error) {
	var configuration connectorConfigurationRecord
	if err := json.Unmarshal([]byte(raw), &configuration); err != nil {
		return connectorConfigurationRecord{}, errors.New("marketplace: corrupt Connector configuration")
	}
	return configuration, nil
}

func installedConnectorToDO(installation *domain.InstalledConnector) installedConnectorDO {
	return installedConnectorDO{ID: installation.ID, OwnerID: installation.OwnerID, ConnectorID: installation.ConnectorID, State: uint8(installation.State), Version: installation.Version}
}

func installedConnectorFromDO(row installedConnectorDO) (*domain.InstalledConnector, error) {
	if row.Version < 1 {
		return nil, domain.ErrInvalidInstallation
	}
	installation := &domain.InstalledConnector{ID: row.ID, OwnerID: row.OwnerID, ConnectorID: row.ConnectorID, State: domain.InstallationState(row.State), Version: row.Version}
	return installation, installation.Validate()
}
