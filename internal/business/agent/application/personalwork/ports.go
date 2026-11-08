// Package personalwork is a person's own use of their Agent: the Personal
// Works they start for themselves, their files and sharing, and the
// person's Personal Library. Agent decides every access itself before calling
// Storage, which keeps the files.
package personalwork

import (
	"context"
	"errors"
)

var (
	ErrInvalidRequest = errors.New("agent: invalid personal work request")
	// ErrReaderUnknown means the User Directory has no such person.
	ErrReaderUnknown = errors.New("agent: the reader is not in the User Directory")
)

// DirectoryUser is one person in the User Directory.
type DirectoryUser struct {
	UserID, Username, DisplayName, AvatarURL string
}

// AccountPort reaches Account's User Directory: everyone in the deployment's
// one enterprise, the scope a Work without a Project is shared within.
type AccountPort interface {
	SearchUsers(ctx context.Context, query string, limit int) ([]DirectoryUser, error)
	GetUsers(ctx context.Context, userIDs []string) ([]DirectoryUser, error)
}
