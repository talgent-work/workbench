package application

import (
	"log/slog"
	"time"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// Application is Account's use-case entry: GitHub sign-in, Login Session
// refresh and sign-out, Agent Accounts and their Identity Tokens,
// Applications and the client credentials grant, signing keys and the User
// Directory.
type Application struct {
	accounts  domain.AccountRepository
	sessions  domain.LoginSessionRepository
	github    GitHubIdentityPort
	tokens    IdentityTokenPort
	directory query.UserDirectoryQueryRepository
	lifetimes domain.SessionLifetimes
	now       func() time.Time

	agentAccounts      domain.AgentAccountRepository
	agentTokenLifetime time.Duration

	applications             domain.ApplicationRepository
	applicationQueries       query.ApplicationQueryRepository
	applicationTokenLifetime time.Duration
	secretOverlap            time.Duration
	publisher                AccountPublisherPort
	logger                   *slog.Logger
}

// Dependencies groups what Account's use cases consume.
type Dependencies struct {
	Accounts  domain.AccountRepository
	Sessions  domain.LoginSessionRepository
	GitHub    GitHubIdentityPort
	Tokens    IdentityTokenPort
	Directory query.UserDirectoryQueryRepository
	Lifetimes domain.SessionLifetimes

	AgentAccounts domain.AgentAccountRepository
	// AgentTokenLifetime bounds an agent's Identity Token, far longer than a
	// run (An agent's token is long).
	AgentTokenLifetime time.Duration

	Applications       domain.ApplicationRepository
	ApplicationQueries query.ApplicationQueryRepository
	// ApplicationTokenLifetime bounds an Application's Identity Token, which
	// is never refreshed (An Application's tokens are short).
	ApplicationTokenLifetime time.Duration
	// SecretOverlap is how long a replaced Client Secret keeps matching after
	// rotation (Rotation overlap).
	SecretOverlap time.Duration
	// Publisher publishes Account's Published Facts; nil publishes nothing.
	Publisher AccountPublisherPort
	// Logger defaults to slog's default logger.
	Logger *slog.Logger
	// Now defaults to the wall clock.
	Now func() time.Time
}

func NewApplication(deps Dependencies) *Application {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	var publisher AccountPublisherPort = noPublisher{}
	if deps.Publisher != nil {
		publisher = deps.Publisher
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Application{
		accounts:  deps.Accounts,
		sessions:  deps.Sessions,
		github:    deps.GitHub,
		tokens:    deps.Tokens,
		directory: deps.Directory,
		lifetimes: deps.Lifetimes,
		now:       now,

		agentAccounts:      deps.AgentAccounts,
		agentTokenLifetime: deps.AgentTokenLifetime,

		applications:             deps.Applications,
		applicationQueries:       deps.ApplicationQueries,
		applicationTokenLifetime: deps.ApplicationTokenLifetime,
		secretOverlap:            deps.SecretOverlap,
		publisher:                publisher,
		logger:                   logger,
	}
}
