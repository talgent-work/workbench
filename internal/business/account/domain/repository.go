package domain

import "context"

// AccountRepository is the collection of Accounts. Inserting a second Account
// for one GitHub Binding fails with ErrGitHubBindingTaken.
type AccountRepository interface {
	Get(ctx context.Context, id AccountID) (*Account, error)
	FindByGitHubBinding(ctx context.Context, binding GitHubBinding) (*Account, error)
	Save(ctx context.Context, account *Account) error
}

// LoginSessionRepository is the collection of Login Sessions.
type LoginSessionRepository interface {
	Get(ctx context.Context, id string) (*LoginSession, error)
	// FindByRefreshSecret finds the session that holds or has replaced the
	// digest, and says which; ErrLoginSessionNotFound when none ever held it.
	FindByRefreshSecret(ctx context.Context, digest RefreshSecretDigest) (*LoginSession, PresentedRefreshSecret, error)
	Save(ctx context.Context, session *LoginSession) error
}
