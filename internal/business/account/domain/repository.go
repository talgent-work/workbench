package domain

import (
	"context"
	"time"
)

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
	// ForgetReplacedSecrets drops the replaced secrets of sessions whose
	// absolute expiry is before expiredBefore, at most limit of them, and
	// reports how many it dropped; such sessions issue nothing.
	ForgetReplacedSecrets(ctx context.Context, expiredBefore time.Time, limit int) (int64, error)
}

// AgentAccountRepository is the collection of Agent Accounts, one per Work.
// Inserting a second Agent Account for a Work fails with
// ErrAgentAccountExists.
type AgentAccountRepository interface {
	Get(ctx context.Context, work WorkID) (*AgentAccount, error)
	Save(ctx context.Context, account *AgentAccount) error
}
