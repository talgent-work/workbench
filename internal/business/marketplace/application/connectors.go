package application

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

const (
	ConnectorAlwaysEnabled = "always_enabled"
	ConnectorEnabled       = "enabled"
	ConnectorDisabled      = "disabled"
	ConnectorNotInstalled  = "not_installed"

	ProviderConnected         = "connected"
	ProviderNotConnected      = "not_connected"
	ProviderReconnectRequired = "reconnect_required"
)

// Connector is one catalog entry as seen by the authenticated user: its
// personal status and, for Provider-authenticated entries, the user's current
// connection to that Provider. Neither status implies a working connection.
type Connector struct {
	Name, Description, Category string
	Required, Offered           bool
	Status                      string
	// InstallationVersion is the concurrency token for personal changes; zero
	// when the user has no installation.
	InstallationVersion          int
	Provider, ProviderConnection string
}

type ConnectorDetail struct {
	Connector
	// Plugins are catalog Plugins whose MCP declarations address this entry.
	Plugins []ConnectorPlugin
}

type ConnectorPlugin struct{ Slug, Description string }

type ConnectorInstallationCommand struct {
	OwnerID, Name string
	Version       int
}

// ConnectorSelection is trusted startup input for Agent: the catalog with offer
// status and the owner's enabled installations. Agent composes the Work choice.
type ConnectorSelection struct {
	Catalog []query.CatalogConnector
	// EnabledConnectorIDs identifies the owner's enabled installations.
	EnabledConnectorIDs []string
}

// ListConnectors returns the browsable catalog: every Offered entry.
func (a *Application) ListConnectors(ctx context.Context, actor string) ([]Connector, error) {
	views, err := a.connectorViews(ctx, actor)
	if err != nil {
		return nil, err
	}
	result := make([]Connector, 0, len(views))
	for _, view := range views {
		if view.Offered {
			result = append(result, view)
		}
	}
	return result, nil
}

// ListInstalledConnectors returns required entries and the user's installations,
// including installations of entries no longer offered, which remain removable.
func (a *Application) ListInstalledConnectors(ctx context.Context, actor string) ([]Connector, error) {
	views, err := a.connectorViews(ctx, actor)
	if err != nil {
		return nil, err
	}
	result := make([]Connector, 0, len(views))
	for _, view := range views {
		if view.Status != ConnectorNotInstalled {
			result = append(result, view)
		}
	}
	return result, nil
}

func (a *Application) GetConnector(ctx context.Context, actor, name string) (ConnectorDetail, error) {
	views, err := a.connectorViews(ctx, actor)
	if err != nil {
		return ConnectorDetail{}, err
	}
	for _, view := range views {
		if view.Name != name || (!view.Offered && view.Status == ConnectorNotInstalled) {
			continue
		}
		plugins, err := a.pluginsUsingConnector(ctx, name)
		if err != nil {
			return ConnectorDetail{}, err
		}
		return ConnectorDetail{Connector: view, Plugins: plugins}, nil
	}
	return ConnectorDetail{}, domain.ErrConnectorNotFound
}

// InstallConnector does not consult Provider authorization. Repeating it keeps
// the existing installation and its enablement.
func (a *Application) InstallConnector(ctx context.Context, actor, name string) (ConnectorDetail, error) {
	if strings.TrimSpace(actor) == "" {
		return ConnectorDetail{}, ErrUnauthenticated
	}
	connector, err := a.connectors.GetByName(ctx, name)
	if err != nil {
		return ConnectorDetail{}, err
	}
	if _, err = a.installedConnectors.Get(ctx, actor, connector.ID); errors.Is(err, domain.ErrConnectorInstallationNotFound) {
		installation, err := domain.InstallConnector(actor, connector)
		if err != nil {
			return ConnectorDetail{}, err
		}
		// A concurrent install of the same pair converges on the stored record.
		if err = a.installedConnectors.Save(ctx, installation); err != nil && !errors.Is(err, domain.ErrConcurrentModification) {
			return ConnectorDetail{}, err
		}
	} else if err != nil {
		return ConnectorDetail{}, err
	}
	return a.GetConnector(ctx, actor, name)
}

func (a *Application) EnableConnector(ctx context.Context, command ConnectorInstallationCommand) (ConnectorDetail, error) {
	return a.changeConnectorInstallation(ctx, command, (*domain.InstalledConnector).Enable)
}

func (a *Application) DisableConnector(ctx context.Context, command ConnectorInstallationCommand) (ConnectorDetail, error) {
	return a.changeConnectorInstallation(ctx, command, (*domain.InstalledConnector).Disable)
}

// UninstallConnector cancels no Provider authorization and uninstalls no Plugin.
// Uninstalling an absent installation succeeds.
func (a *Application) UninstallConnector(ctx context.Context, command ConnectorInstallationCommand) error {
	installation, err := a.loadConnectorInstallation(ctx, command)
	if errors.Is(err, domain.ErrConnectorInstallationNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	installation.Uninstall()
	return a.installedConnectors.Delete(ctx, installation)
}

// ReadConnectorSelection is restricted to trusted Agent startup composition.
func (a *Application) ReadConnectorSelection(ctx context.Context, owner string) (ConnectorSelection, error) {
	if strings.TrimSpace(owner) == "" {
		return ConnectorSelection{}, ErrUnauthenticated
	}
	connectors, err := a.connectors.List(ctx)
	if err != nil {
		return ConnectorSelection{}, err
	}
	installed, err := a.installedQueries.ListInstalled(ctx, owner)
	if err != nil {
		return ConnectorSelection{}, err
	}
	selection := ConnectorSelection{Catalog: make([]query.CatalogConnector, 0, len(connectors))}
	for _, connector := range connectors {
		entry, err := selectionEntry(connector)
		if err != nil {
			return ConnectorSelection{}, err
		}
		selection.Catalog = append(selection.Catalog, entry)
	}
	for _, installation := range installed {
		if installation.Enabled {
			selection.EnabledConnectorIDs = append(selection.EnabledConnectorIDs, installation.ConnectorID)
		}
	}
	return selection, nil
}

func (a *Application) changeConnectorInstallation(ctx context.Context, command ConnectorInstallationCommand, change func(*domain.InstalledConnector)) (ConnectorDetail, error) {
	installation, err := a.loadConnectorInstallation(ctx, command)
	if err != nil {
		return ConnectorDetail{}, err
	}
	change(installation)
	if err := a.installedConnectors.Save(ctx, installation); err != nil {
		return ConnectorDetail{}, err
	}
	return a.GetConnector(ctx, command.OwnerID, command.Name)
}

func (a *Application) loadConnectorInstallation(ctx context.Context, command ConnectorInstallationCommand) (*domain.InstalledConnector, error) {
	if strings.TrimSpace(command.OwnerID) == "" {
		return nil, ErrUnauthenticated
	}
	connector, err := a.connectors.GetByName(ctx, command.Name)
	if err != nil {
		return nil, err
	}
	installation, err := a.installedConnectors.Get(ctx, command.OwnerID, connector.ID)
	if err != nil {
		return nil, err
	}
	if installation.Version != command.Version {
		return nil, domain.ErrConcurrentModification
	}
	return installation, nil
}

func (a *Application) connectorViews(ctx context.Context, actor string) ([]Connector, error) {
	if strings.TrimSpace(actor) == "" {
		return nil, ErrUnauthenticated
	}
	catalog, err := a.connectorQueries.ListCatalog(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := a.installedQueries.ListInstalled(ctx, actor)
	if err != nil {
		return nil, err
	}
	authorizations, err := a.authorizations.ListPersonalAuthorizations(ctx, actor)
	if err != nil {
		return nil, err
	}
	installations := make(map[string]query.InstalledConnector, len(installed))
	for _, installation := range installed {
		installations[installation.ConnectorID] = installation
	}
	connections := make(map[string]string, len(authorizations))
	for _, authorization := range authorizations {
		connections[authorization.Provider] = providerConnection(authorization.Status)
	}
	views := make([]Connector, 0, len(catalog))
	for _, entry := range catalog {
		view := Connector{Name: entry.Name, Description: entry.Description, Category: entry.Category, Required: entry.Required, Offered: entry.Offered, Status: ConnectorNotInstalled}
		if installation, found := installations[entry.ID]; entry.Required {
			view.Status = ConnectorAlwaysEnabled
		} else if found {
			view.Status, view.InstallationVersion = ConnectorDisabled, installation.Version
			if installation.Enabled {
				view.Status = ConnectorEnabled
			}
		}
		if entry.Provider != nil {
			view.Provider, view.ProviderConnection = entry.Provider.Provider, ProviderNotConnected
			if connection, found := connections[entry.Provider.Provider]; found {
				view.ProviderConnection = connection
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func providerConnection(status string) string {
	switch status {
	case string(domain.ProviderAuthorizationActive):
		return ProviderConnected
	case string(domain.ProviderAuthorizationReconnectRequired):
		return ProviderReconnectRequired
	default:
		return ProviderNotConnected
	}
}

// pluginsUsingConnector scans catalog Plugins declaring MCP servers and keeps
// those whose declarations address the named Connector's endpoint.
func (a *Application) pluginsUsingConnector(ctx context.Context, name string) ([]ConnectorPlugin, error) {
	catalog, err := a.connectorQueries.ListCatalog(ctx)
	if err != nil {
		return nil, err
	}
	index := indexConnectorEndpoints(catalog)
	var id string
	for _, entry := range catalog {
		if entry.Name == name {
			id = entry.ID
		}
	}
	var plugins []ConnectorPlugin
	for cursor := ""; ; {
		page, err := a.catalog.List(ctx, cursor, 100, "mcp")
		if errors.Is(err, ErrCatalogNotReady) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, release := range page.Releases {
			if index.usedBy(release, id) {
				plugins = append(plugins, ConnectorPlugin{Slug: release.Slug, Description: release.Description})
			}
		}
		if page.NextCursor == "" {
			return plugins, nil
		}
		cursor = page.NextCursor
	}
}

// selectionEntry is what a new Work may select: the connection the Connector
// provides while Offered, or only its identity otherwise so Agent can reject a
// selection of it.
func selectionEntry(connector *domain.Connector) (query.CatalogConnector, error) {
	provided, err := connector.ProvideConnection()
	if errors.Is(err, domain.ErrConnectorNotOffered) {
		return query.CatalogConnector{ID: connector.ID, Name: connector.Name, Required: connector.Required}, nil
	}
	if err != nil {
		return query.CatalogConnector{}, err
	}
	entry := query.CatalogConnector{ID: provided.ID, Name: provided.Name, Description: provided.Presentation.Description, Category: provided.Presentation.Category,
		Required: provided.Required, Offered: true, Authentication: provided.Authentication,
		Configuration: query.ConnectorConfiguration{Type: provided.Configuration.Type, URL: provided.Configuration.URL, Command: provided.Configuration.Command,
			Arguments: provided.Configuration.Arguments, Environment: provided.Configuration.Environment, Headers: provided.Configuration.Headers}}
	if binding := provided.ProviderAccessBinding; binding != nil {
		entry.Provider = &query.ConnectorProvider{Provider: binding.Provider, Target: binding.Target, Authentication: binding.Authentication}
	}
	return entry, nil
}
