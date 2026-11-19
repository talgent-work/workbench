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

// Connector is one Connector as its reading owner sees it: its publisher,
// its Lifecycle State, the owner's personal status and, for a Provider
// Connector, the owner's current connection to that Provider. Neither status
// implies a working connection.
type Connector struct {
	ID, Name, Description, Category string
	Required                        bool
	// Offered reports whether the Connector is in the owner's catalog, so new
	// installations and Works may select it.
	Offered bool
	// State is private, published or withdrawn.
	State     string
	Publisher Publisher
	// Transport is http or stdio.
	Transport  string
	IconDigest string
	// Version is the concurrency token for changing the Connector itself.
	Version           int
	PublishedAtMillis int64
	// Settings are present only for the owner's own Connector.
	Settings *ConnectorSettings
	Status   string
	// InstallationVersion is the concurrency token for installation changes;
	// zero when the owner has no installation.
	InstallationVersion          int
	Provider, ProviderConnection string
}

// Publisher names who provides a Connector.
type Publisher struct {
	// Kind is platform, person or application.
	Kind, DisplayName string
	// Own reports whether the reading owner made the Connector.
	Own bool
}

// ConnectorSettings is an owner's Connector connection without credentials.
type ConnectorSettings struct {
	URL, Command string
	Arguments    []string
}

type ConnectorDetail struct {
	Connector
	// Plugins are catalog Plugins whose MCP declarations address this entry.
	Plugins []ConnectorPlugin
}

type ConnectorPlugin struct{ Slug, Description string }

type ConnectorInstallationCommand struct {
	OwnerID, ConnectorID string
	Version              int
}

// ListConnectors returns the owner's Connector catalog: the Platform
// Connectors, the Published Connectors and the owner's own Private ones.
func (a *Application) ListConnectors(ctx context.Context, owner string) ([]Connector, error) {
	return a.connectorViews(ctx, owner, func(view Connector) bool { return view.Offered })
}

// ListInstalledConnectors returns required entries and the owner's
// installations, including those of Withdrawn Connectors, which remain
// removable.
func (a *Application) ListInstalledConnectors(ctx context.Context, owner string) ([]Connector, error) {
	return a.connectorViews(ctx, owner, func(view Connector) bool { return view.Status != ConnectorNotInstalled })
}

// ListOwnedConnectors returns every Connector the owner made, Withdrawn ones
// included.
func (a *Application) ListOwnedConnectors(ctx context.Context, owner string) ([]Connector, error) {
	return a.connectorViews(ctx, owner, func(view Connector) bool { return view.Publisher.Own })
}

// GetConnector reads a Connector of the owner's catalog, one the owner made or
// one the owner installed.
func (a *Application) GetConnector(ctx context.Context, owner, id string) (ConnectorDetail, error) {
	views, err := a.connectorViews(ctx, owner, func(view Connector) bool { return view.ID == id })
	if err != nil {
		return ConnectorDetail{}, err
	}
	if len(views) == 0 {
		return ConnectorDetail{}, domain.ErrConnectorNotFound
	}
	plugins, err := a.pluginsUsingConnector(ctx, id)
	if err != nil {
		return ConnectorDetail{}, err
	}
	return ConnectorDetail{Connector: views[0], Plugins: plugins}, nil
}

// InstallConnector does not consult Provider authorization. Repeating it keeps
// the existing installation and its enablement.
func (a *Application) InstallConnector(ctx context.Context, owner, id string) (ConnectorDetail, error) {
	if strings.TrimSpace(owner) == "" {
		return ConnectorDetail{}, ErrUnauthenticated
	}
	connector, err := a.visibleConnector(ctx, owner, id)
	if err != nil {
		return ConnectorDetail{}, err
	}
	if _, err = a.installedConnectors.Get(ctx, owner, connector.ID); errors.Is(err, domain.ErrConnectorInstallationNotFound) {
		installation, err := domain.InstallConnector(owner, connector)
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
	return a.GetConnector(ctx, owner, id)
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

func (a *Application) changeConnectorInstallation(ctx context.Context, command ConnectorInstallationCommand, change func(*domain.InstalledConnector)) (ConnectorDetail, error) {
	installation, err := a.loadConnectorInstallation(ctx, command)
	if err != nil {
		return ConnectorDetail{}, err
	}
	change(installation)
	if err := a.installedConnectors.Save(ctx, installation); err != nil {
		return ConnectorDetail{}, err
	}
	return a.GetConnector(ctx, command.OwnerID, command.ConnectorID)
}

func (a *Application) loadConnectorInstallation(ctx context.Context, command ConnectorInstallationCommand) (*domain.InstalledConnector, error) {
	if strings.TrimSpace(command.OwnerID) == "" {
		return nil, ErrUnauthenticated
	}
	installation, err := a.installedConnectors.Get(ctx, command.OwnerID, command.ConnectorID)
	if err != nil {
		return nil, err
	}
	if installation.Version != command.Version {
		return nil, domain.ErrConcurrentModification
	}
	return installation, nil
}

// visibleConnector loads a Connector the owner may see: one in the owner's
// catalog or one the owner made. Any other reads as absent, so a Private
// Connector's existence never reaches another owner.
func (a *Application) visibleConnector(ctx context.Context, owner, id string) (*domain.Connector, error) {
	connector, err := a.connectors.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !connector.InCatalogOf(owner) && !connector.Owner.Is(owner) {
		return nil, domain.ErrConnectorNotFound
	}
	return connector, nil
}

// connectorViews reads what the owner sees of every Connector the keep
// filter admits, ordered by name.
func (a *Application) connectorViews(ctx context.Context, owner string, keep func(Connector) bool) ([]Connector, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, ErrUnauthenticated
	}
	catalog, err := a.connectorQueries.ListForOwner(ctx, owner)
	if err != nil {
		return nil, err
	}
	installed, err := a.installedQueries.ListInstalled(ctx, owner)
	if err != nil {
		return nil, err
	}
	authorizations, err := a.authorizations.ListPersonalAuthorizations(ctx, owner)
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
		installation, isInstalled := installations[entry.ID]
		if !entry.InCatalogOf(owner) && !entry.OwnedBy(owner) && !isInstalled {
			continue
		}
		view := connectorView(owner, entry)
		if entry.Required {
			view.Status = ConnectorAlwaysEnabled
		} else if isInstalled {
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
		if keep(view) {
			views = append(views, view)
		}
	}
	return views, a.namePublishers(ctx, views)
}

func connectorView(owner string, entry query.CatalogConnector) Connector {
	view := Connector{ID: entry.ID, Name: entry.Name, Description: entry.Description, Category: entry.Category, Required: entry.Required,
		Offered: entry.InCatalogOf(owner), State: entry.State, Publisher: Publisher{Kind: entry.Owner.Kind, Own: entry.OwnedBy(owner)},
		Transport: transport(entry.Configuration.Type), IconDigest: entry.IconDigest, Version: entry.Version, PublishedAtMillis: entry.PublishedAtMillis,
		Status: ConnectorNotInstalled}
	if view.Publisher.Own {
		view.Settings = &ConnectorSettings{URL: entry.Configuration.URL, Command: entry.Configuration.Command, Arguments: entry.Configuration.Arguments}
	}
	if entry.Owner.Kind != query.OwnerPlatform {
		view.Publisher.DisplayName = entry.Owner.ID
	}
	return view
}

func transport(kind string) string {
	if kind == "http" {
		return "http"
	}
	return "stdio"
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
// those whose declarations address the Connector's endpoint. Only Published
// Connectors relate to the platform-wide Plugin catalog.
func (a *Application) pluginsUsingConnector(ctx context.Context, id string) ([]ConnectorPlugin, error) {
	published, err := a.connectorQueries.ListPublished(ctx)
	if err != nil {
		return nil, err
	}
	index := indexConnectorEndpoints(published)
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
