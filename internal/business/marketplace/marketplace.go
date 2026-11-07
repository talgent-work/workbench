package marketplace

import (
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	marketplaceconnect "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/connectlog"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
	"go.uber.org/fx"
	"xorm.io/xorm"
)

// Directory is a private namespace on the existing mounted content volume.
// Owned archives persist there; release metadata is queried from MySQL.
type Config struct {
	Synchronization    SynchronizationConfig     `json:"synchronization" yaml:"synchronization" toml:"synchronization"`
	ProviderConnectors []ProviderConnectorConfig `json:"provider-connectors" yaml:"provider-connectors" toml:"provider-connectors"`
	PlatformConnector  PlatformConnectorConfig   `json:"platform-connector" yaml:"platform-connector" toml:"platform-connector"`
	Connections        ConnectionConfig          `json:"connections" yaml:"connections" toml:"connections"`
	Directory          string                    `json:"directory" yaml:"directory" toml:"directory"`
	Name               string                    `json:"name" yaml:"name" toml:"name"`
	Owner              string                    `json:"owner" yaml:"owner" toml:"owner"`
}

func (c Config) Validate() error {
	if err := c.Synchronization.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(c.Directory) || strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Owner) == "" {
		return errors.New("marketplace: absolute catalog directory, name and owner are required")
	}
	return nil
}

type handlerParams struct {
	fx.In
	App     *application.Application
	Config  Config
	Handler *marketplaceconnect.Handler
	Catalog *marketplacehttp.CatalogHandler
	Mux     *http.ServeMux
	Logger  *slog.Logger
}

// Database is the Workbench core's MySQL engine, holding catalog and
// connection records; Queue is Marketplace's task lane.
const (
	Database = "workbench"
	Queue    = "marketplace"
)

var Module = fx.Module("domain.marketplace",
	ConnectionsModule,
	SynchronizationModule,
	pkgtaskqueue.Bind(Queue),
	pkgmysql.Bind(Database),
	fx.Provide(func(config Config, engine *xorm.Engine) (*infrastructure.CatalogStore, error) {
		if err := config.Validate(); err != nil {
			return nil, err
		}
		return infrastructure.NewCatalogStore(engine, config.Directory, config.Name, config.Owner)
	}),
	fx.Provide(fx.Annotate(infrastructure.NewInstalledPluginRepository,
		fx.As(new(domain.InstalledPluginRepository)), fx.As(new(query.InstalledPluginQueryRepository)),
	)),
	fx.Provide(infrastructure.NewConnectorRepository, infrastructure.NewInstalledConnectorRepository),
	fx.Provide(func(connectors *infrastructure.ConnectorRepository, installed *infrastructure.InstalledConnectorRepository) application.ConnectorRepositories {
		return application.ConnectorRepositories{Catalog: connectors, Installed: installed, Queries: connectors}
	}),
	fx.Invoke(registerPlatformConnectors),
	fx.Provide(func(store *infrastructure.CatalogStore) application.CatalogPort { return store }),
	fx.Provide(fx.Annotate(infrastructure.NewProviderAuthorizationQueries, fx.As(new(query.ProviderAuthorizationQueryRepository)))),
	fx.Provide(application.NewApplication, marketplaceconnect.NewHandler, marketplacehttp.NewCatalogHandler),
	fx.Invoke(func(params handlerParams) {
		path, handler := marketplacepublicv1connect.NewMarketplaceServiceHandler(params.Handler,
			connect.WithInterceptors(connectlog.NewInterceptor(params.Logger), gatewayActor()))
		params.Mux.Handle(path, handler)
		selectionPath, selectionHandler := marketplaceprivatev1connect.NewSelectionServiceHandler(marketplaceconnect.NewSelectionHandler(params.App, params.Config.Connections.CredentialToken), connect.WithInterceptors(connectlog.NewInterceptor(params.Logger), gatewayActor()))
		params.Mux.Handle(selectionPath, selectionHandler)
		params.Mux.Handle(marketplacehttp.ContentPath, params.Catalog)
	}),
)

// gatewayActor admits only calls the gateway made for an authenticated person.
func gatewayActor() connect.Interceptor {
	return authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})
}
