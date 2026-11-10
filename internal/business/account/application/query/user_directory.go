package query

import "context"

// User is one User Directory entry.
type User struct {
	ID          string
	Username    string
	Email       string
	DisplayName string
	AvatarURL   string
}

// UserDirectoryQueryRepository reads the platform-wide directory of Accounts.
type UserDirectoryQueryRepository interface {
	// SearchByUsername matches usernames containing the term, ignoring case,
	// ordered by username.
	SearchByUsername(ctx context.Context, term string, limit int) ([]User, error)
	// GetByIDs returns the known Accounts among ids; unknown ids are omitted.
	GetByIDs(ctx context.Context, ids []string) ([]User, error)
}
