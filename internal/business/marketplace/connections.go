package marketplace

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

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
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/credentialcipher"
	scmgithub "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/github"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthprovider"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/persistence"
	marketplaceconnect "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
	"github.com/talgent-work/workbench/internal/pkg/database"
	"github.com/talgent-work/workbench/internal/pkg/eventlog"
)

type ConnectionConfig struct {
	CredentialToken string `json:"credential-token" toml:"credential-token" yaml:"credential-token"`
	// CredentialCipherKey encrypts every Provider's credentials at rest; a
	// deployment that declares a Provider needs it.
	CredentialCipherKey string `json:"credential-cipher-key" toml:"credential-cipher-key" yaml:"credential-cipher-key"`
	// GitHub is the GitHub App adapter beside the github Provider declaration.
	GitHub      scmgithub.Config        `json:"github" toml:"github" yaml:"github"`
	Maintenance tokenmaintenance.Config `json:"maintenance" toml:"maintenance" yaml:"maintenance"`
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
	database.Bind(Database),
	fx.Provide(newProviderCatalog, fx.Private),
	fx.Provide(func(catalog *providerCatalog) application.ProviderDeclarations { return catalog }),
	fx.Provide(func(cfg ConnectionConfig, catalog *providerCatalog) (scmgithub.Config, error) {
		return catalog.gitHub(cfg.GitHub, cfg.CredentialCipherKey)
	}),
	fx.Provide(func(cfg ConnectionConfig) tokenmaintenance.Config { return cfg.Maintenance }),
	fx.Provide(func(cfg ConnectionConfig, catalog *providerCatalog) (application.CredentialCipher, error) {
		if len(catalog.declarations) > 0 && cfg.CredentialCipherKey == "" {
			return nil, errors.New("marketplace: connections.credential-cipher-key is required when a Provider is declared")
		}
		return credentialcipher.New(cfg.CredentialCipherKey)
	}),
	fx.Provide(persistence.NewProviderAuthorizations),
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
	fx.Provide(func(catalog *providerCatalog, cfg scmgithub.Config, cipher application.CredentialCipher, httpClient *http.Client) (application.ProviderAuthorizationPorts, error) {
		ports := providerPorts{}
		if cfg.Enabled {
			port, err := scmgithub.NewAuthorizationPort(cfg, cipher)
			if err != nil {
				return nil, err
			}
			ports[domain.ProviderGitHub] = port
		}
		for _, client := range catalog.oauthClients() {
			port, err := oauthprovider.NewPort(client, cipher, httpClient)
			if err != nil {
				return nil, err
			}
			ports[client.Provider] = port
		}
		return ports, nil
	}),
	fx.Provide(func(catalog *providerCatalog, cipher application.CredentialCipher, httpClient *http.Client) (application.OAuthProviderPort, error) {
		return oauthprovider.NewAuthorizer(catalog.oauthClients(), cipher, httpClient)
	}),
	fx.Provide(func(store application.AuthorizationStore, providers application.ProviderDeclarations, ports application.ProviderAuthorizationPorts,
		cipher application.CredentialCipher, cfg tokenmaintenance.Config) (*application.ProviderCredentials, error) {
		maintenance, err := cfg.Normalize()
		if err != nil {
			return nil, err
		}
		return application.NewProviderCredentials(store, providers, ports, cipher, application.CredentialTiming{
			RequestSkew: maintenance.RequestRefreshSkew.Duration(), ProactiveWindow: maintenance.ProactiveRefreshWindow.Duration(),
			LeaseDuration: maintenance.RefreshLeaseDuration.Duration(),
		}), nil
	}),
	fx.Provide(scmgithub.NewCatalog, fx.Private),
	fx.Provide(fx.Annotate(scopemaintenance.NewScheduler, fx.As(new(application.MarketplaceTaskQueuePort)), fx.As(fx.Self()))),
	fx.Provide(func(candidates query.ProviderAuthorizationQueryRepository, scheduler *scopemaintenance.Scheduler) *scopemaintenance.ReconcileScanProcessor {
		return scopemaintenance.NewReconcileScanProcessor(candidates, scheduler, time.Now)
	}),
	fx.Provide(func(connections *application.PersonalConnections) *scopemaintenance.ObserveProcessor {
		return scopemaintenance.NewObserveProcessor(connections)
	}),
	fx.Provide(scmgithub.NewAuthorizer, scmgithub.NewCredentialSupplier, fx.Private),
	fx.Provide(fx.Annotate(scmgithub.NewGitHub, fx.As(new(application.GitHubPort)))),
	fx.Provide(func(cfg scmgithub.Config, app *application.Application, logger *slog.Logger) (*marketplacehttp.WebhookHandler, error) {
		return marketplacehttp.NewWebhookHandler(cfg.WebhookSecret, app, logger)
	}),
	fx.Provide(func(
		authorizations domain.ProviderAuthorizationRepository,
		candidates query.ProviderAuthorizationQueryRepository,
		enqueuer componenttaskqueue.Enqueuer,
		registry *componenttaskqueue.SchemaRegistry,
		cfg tokenmaintenance.Config,
	) *tokenmaintenance.RefreshScanProcessor {
		return tokenmaintenance.NewRefreshScanProcessor(authorizations, candidates, enqueuer, registry, cfg, time.Now)
	}),
	fx.Provide(func(credentials *application.ProviderCredentials) *tokenmaintenance.RefreshAuthorizationProcessor {
		return tokenmaintenance.NewRefreshAuthorizationProcessor(credentials)
	}),
	fx.Provide(func(store application.AuthorizationStore, queries query.ProviderAuthorizationQueryRepository, providers application.ProviderDeclarations,
		github application.GitHubPort, oauth application.OAuthProviderPort, tasks application.MarketplaceTaskQueuePort, credentials *application.ProviderCredentials) *application.PersonalConnections {
		return application.NewPersonalConnections(application.ConnectionDependencies{Authorizations: store, Queries: queries, Providers: providers,
			GitHub: github, OAuth: oauth, Tasks: tasks, Credentials: credentials})
	}),
	TaskqueueModule,
	fx.Invoke(func(params connectionHandlerParams) {
		params.Mux.Handle(marketplacehttp.GitHubWebhookPath, params.Webhook)
		server := connectserver.New(params.Logger)
		marketplaceprivatev1connect.RegisterCredentialServiceHandler(server, marketplaceconnect.NewCredentialHandler(params.App, params.Config.CredentialToken))
		connectserver.Mount(params.Mux, server)
	}),
)

func newCredentialEventDispatcher(logger *slog.Logger) *domainevent.InMemoryDispatcher {
	return domainevent.NewDispatcher(
		domainevent.WithHandlerTimeout(30*time.Second),
		domainevent.WithUnhandledEventHandler(func(unhandled domainevent.UnhandledContext) {
			logger.Debug("orphan event (no subscriber)", slog.String("event_kind", string(unhandled.Event.Kind())))
		}),
	)
}
