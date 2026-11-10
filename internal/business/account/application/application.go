package application

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// Application is Account's use-case entry: GitHub sign-in, Login Session
// refresh and sign-out, Agent Accounts and their Identity Tokens, signing
// keys and the User Directory.
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
	// Now defaults to the wall clock.
	Now func() time.Time
}

func NewApplication(deps Dependencies) *Application {
	now := deps.Now
	if now == nil {
		now = time.Now
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
	}
}
