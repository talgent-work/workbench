package application

import (
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

var (
	ErrUnauthenticated = errors.New("marketplace: authenticated user required")
	ErrInvalidQuery    = errors.New("marketplace: invalid query")
	ErrReleaseNotFound = errors.New("marketplace: plugin release not found")
	ErrCatalogNotReady = errors.New("marketplace: catalog has not been synchronized")
	ErrArchiveNotFound = errors.New("marketplace: published archive not found")
)

type Application struct {
	*PersonalConnections
	authorizations      query.ProviderAuthorizationQueryRepository
	installations       domain.InstalledPluginRepository
	queries             query.InstalledPluginQueryRepository
	catalog             PluginCatalogQueryRepository
	connectors          domain.ConnectorRepository
	installedConnectors domain.InstalledConnectorRepository
	connectorQueries    query.ConnectorQueryRepository
	installedQueries    query.InstalledConnectorQueryRepository
}

type ConnectorRepositories struct {
	Catalog          domain.ConnectorRepository
	Installed        domain.InstalledConnectorRepository
	CatalogQueries   query.ConnectorQueryRepository
	InstalledQueries query.InstalledConnectorQueryRepository
}

func NewApplication(installations domain.InstalledPluginRepository, queries query.InstalledPluginQueryRepository, catalog PluginCatalogQueryRepository, authorizations query.ProviderAuthorizationQueryRepository, connections *PersonalConnections, connectors ConnectorRepositories) *Application {
	return &Application{PersonalConnections: connections, installations: installations, queries: queries, catalog: catalog, authorizations: authorizations,
		connectors: connectors.Catalog, installedConnectors: connectors.Installed, connectorQueries: connectors.CatalogQueries, installedQueries: connectors.InstalledQueries}
}
