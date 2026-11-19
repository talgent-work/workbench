package application

import (
	"errors"
	"log/slog"

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
	publishers          PublisherDirectory
	platformNames       []string
	logger              *slog.Logger
}

// ConnectorDependencies are what Connector use cases reach beside the
// Plugin catalog.
type ConnectorDependencies struct {
	Catalog          domain.ConnectorRepository
	Installed        domain.InstalledConnectorRepository
	CatalogQueries   query.ConnectorQueryRepository
	InstalledQueries query.InstalledConnectorQueryRepository
	// Publishers names the people and Applications who publish Connectors.
	Publishers PublisherDirectory
	// PlatformNames are the deployment's Platform Connector names, which no
	// owner adds (Reserved platform names).
	PlatformNames []string
	Logger        *slog.Logger
}

func NewApplication(installations domain.InstalledPluginRepository, queries query.InstalledPluginQueryRepository, catalog PluginCatalogQueryRepository, authorizations query.ProviderAuthorizationQueryRepository, connections *PersonalConnections, connectors ConnectorDependencies) *Application {
	logger := connectors.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Application{PersonalConnections: connections, installations: installations, queries: queries, catalog: catalog, authorizations: authorizations,
		connectors: connectors.Catalog, installedConnectors: connectors.Installed, connectorQueries: connectors.CatalogQueries, installedQueries: connectors.InstalledQueries,
		publishers: connectors.Publishers, platformNames: connectors.PlatformNames, logger: logger}
}
