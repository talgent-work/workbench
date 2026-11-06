package application

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// Application is Account's use-case entry: GitHub sign-in, Login Session
// refresh and sign-out, Identity Token keys and the User Directory.
type Application struct {
	accounts  domain.AccountRepository
	sessions  domain.LoginSessionRepository
	github    GitHubIdentityPort
	tokens    IdentityTokenPort
	retention RefreshSecretRetention
	directory query.UserDirectoryRepository
	lifetimes domain.SessionLifetimes
	now       func() time.Time
}

// Dependencies groups what Account's use cases consume.
type Dependencies struct {
	Accounts  domain.AccountRepository
	Sessions  domain.LoginSessionRepository
	GitHub    GitHubIdentityPort
	Tokens    IdentityTokenPort
	Retention RefreshSecretRetention
	Directory query.UserDirectoryRepository
	Lifetimes domain.SessionLifetimes
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
		retention: deps.Retention,
		directory: deps.Directory,
		lifetimes: deps.Lifetimes,
		now:       now,
	}
}
