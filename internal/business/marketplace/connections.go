package marketplace

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"

	domainevent "github.com/go-jimu/components/ddd/event"
	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"

	"github.com/talgent-work/contracts/gen/go/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/messagepublisher"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/scopemaintenance"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/tokenmaintenance"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	scmgithub "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/github"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/persistence"
	marketplaceconnect "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
	"github.com/talgent-work/workbench/internal/pkg/eventlog"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
)

type ConnectionConfig struct {
	CredentialToken string                  `json:"credential-token" toml:"credential-token" yaml:"credential-token"`
	GitHub          scmgithub.Config        `json:"github" toml:"github" yaml:"github"`
	Maintenance     tokenmaintenance.Config `json:"maintenance" toml:"maintenance" yaml:"maintenance"`
}

type connectionHandlerParams struct {
	fx.In
	Config ConnectionConfig

	App     *application.PersonalConnections
	Mux     *http.ServeMux
	Logger  *slog.Logger
	Webhook *marketplacehttp.WebhookHandler
}

var ConnectionsModule = fx.Module(
	"domain.marketplace.connections",
	fx.Provide(func(cfg Config) ConnectionConfig { return cfg.Connections }),
	pkgmysql.Bind(Database),
	fx.Provide(func(cfg ConnectionConfig) scmgithub.Config { return cfg.GitHub }),
	fx.Provide(func(cfg ConnectionConfig) tokenmaintenance.Config { return cfg.Maintenance }),
	fx.Provide(fx.Annotate(
		persistence.NewProviderAuthorizations,
		fx.As(new(application.ProviderConnectionQueryRepository)), fx.As(fx.Self()),
	)),
	// Every save of an authorization goes through the store that dispatches
	// its recorded credential changes.
	fx.Provide(fx.Annotate(
		func(repository *persistence.ProviderAuthorizations, events *domainevent.InMemoryDispatcher, logger *slog.Logger) *application.CredentialChangeDispatch {
			return application.NewCredentialChangeDispatch(repository, events, logger)
		},
		fx.As(new(domain.ProviderAuthorizationRepository)), fx.As(new(application.AuthorizationStore)),
	)),
	fx.Provide(newCredentialEventDispatcher, fx.Private),
	fx.Provide(messagepublisher.NewCredentialChangePublisher),
	fx.Invoke(func(lc fx.Lifecycle, events *domainevent.InMemoryDispatcher, publisher *messagepublisher.CredentialChangePublisher, logger *slog.Logger) {
		events.Subscribe(eventlog.Handler(logger, publisher))
		lc.Append(fx.Hook{OnStop: events.Close})
	}),
	fx.Provide(fx.Annotate(persistence.NewAuthorizationMaintenanceQueries, fx.As(new(query.AuthorizationMaintenanceQueryRepository)), fx.As(new(query.AuthorizationEventQueryRepository)))),
	fx.Provide(fx.Annotate(
		scmgithub.NewCatalog,
		fx.As(new(application.ProviderAccessObserver)),
	)),
	fx.Provide(fx.Annotate(scopemaintenance.NewScheduler, fx.As(new(application.AccessScopeObservations)), fx.As(fx.Self()))),
	fx.Provide(func(candidates query.AuthorizationMaintenanceQueryRepository, scheduler *scopemaintenance.Scheduler) *scopemaintenance.ReconcileScanProcessor {
		return scopemaintenance.NewReconcileScanProcessor(candidates, scheduler, time.Now)
	}),
	fx.Provide(func(connections *application.PersonalConnections) *scopemaintenance.ObserveProcessor {
		return scopemaintenance.NewObserveProcessor(connections)
	}),
	fx.Provide(fx.Annotate(
		scmgithub.NewTokenCipher,
		fx.As(new(scmgithub.TokenCipher)),
	)),
	fx.Provide(fx.Annotate(
		scmgithub.NewAuthorizer,
		fx.As(new(application.ProviderAuthorizer)),
	)),
	fx.Provide(fx.Annotate(
		scmgithub.NewCredentialSupplier,
		fx.As(new(credential.RepositoryCredentialSupplier)), fx.As(new(credential.ServiceCredentialSupplier)),
	)),
	fx.Provide(fx.Annotate(
		scmgithub.NewAuthorizationRefresher,
		fx.As(new(tokenmaintenance.AuthorizationRefresher)),
	)),
	fx.Provide(func(cfg scmgithub.Config, app *application.Application, logger *slog.Logger) (*marketplacehttp.WebhookHandler, error) {
		return marketplacehttp.NewWebhookHandler(cfg.WebhookSecret, app, logger)
	}),
	fx.Provide(fx.Annotate(scmgithub.NewAuthorizationPort, fx.As(new(domain.ProviderAuthorizationPort)))),
	fx.Provide(func(
		authorizations domain.ProviderAuthorizationRepository,
		candidates query.AuthorizationMaintenanceQueryRepository,
		enqueuer componenttaskqueue.Enqueuer,
		registry *componenttaskqueue.SchemaRegistry,
		cfg tokenmaintenance.Config,
	) *tokenmaintenance.RefreshScanProcessor {
		return tokenmaintenance.NewRefreshScanProcessor(authorizations, candidates, enqueuer, registry, cfg, time.Now)
	}),
	fx.Provide(func(
		refresher tokenmaintenance.AuthorizationRefresher,
	) *tokenmaintenance.RefreshAuthorizationProcessor {
		return tokenmaintenance.NewRefreshAuthorizationProcessor(refresher)
	}),
	fx.Provide(application.NewPersonalConnections),
	TaskqueueModule,
	fx.Invoke(func(params connectionHandlerParams) {
		params.Mux.Handle(marketplacehttp.GitHubWebhookPath, params.Webhook)
		server := connectserver.New(params.Logger, gatewayActor())
		marketplaceprivatev1connect.RegisterCredentialServiceHandler(server, marketplaceconnect.NewCredentialHandler(params.App, params.Config.CredentialToken))
		connectserver.Mount(params.Mux, server)
	}),
)

func newCredentialEventDispatcher(logger *slog.Logger) *domainevent.InMemoryDispatcher {
	// No close delay: its close hook is the module's first, so every producer
	// has stopped by the time it closes.
	return domainevent.NewDispatcher(
		domainevent.WithDelayClose(0),
		domainevent.WithHandlerTimeout(30*time.Second),
		domainevent.WithUnhandledEventHandler(func(unhandled domainevent.UnhandledContext) {
			logger.Debug("orphan event (no subscriber)", slog.String("event_kind", string(unhandled.Event.Kind())))
		}),
	)
}
