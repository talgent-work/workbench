// Package personalwork is a person's own use of their Agent: the Personal
// Works they start for themselves, their files and sharing, and the
// person's Personal Library. Agent decides every access itself before calling
// Storage, which keeps the files.
package personalwork

import (
	"context"
	"errors"
	"time"
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

// PersonalWorkSummary is one Personal Work as its owner's list shows it.
type PersonalWorkSummary struct {
	WorkID, Title string
	// State is the Work State as Works are shown.
	State   string
	Waiting bool
	// StateReason is why it stopped, when it is Stopped.
	StateReason string
	// DisconnectedSince is the origin of its current Disconnection; zero
	// while observed.
	DisconnectedSince    time.Time
	CreatedAt, UpdatedAt time.Time
}

// LibraryWork is one Work whose Artifacts a person's Personal Library holds:
// one of their Personal Works, or one they hold a Work Share in, shared by
// its owner.
type LibraryWork struct {
	WorkID, Title string
	InputSource   string
	// Personal tells the Work is a Personal Work, whose File Space it owns.
	Personal bool
	// SharedBy is the owner who shared it; empty for the person's own Work.
	SharedBy string
}

// Works reads the Works a person's own pages show.
type Works interface {
	// ListPersonalWorks lists a definition's Works without a Subject or a
	// Project, most recently updated first.
	ListPersonalWorks(ctx context.Context, agentID string, limit int) ([]PersonalWorkSummary, error)
	// ListLibraryWorks lists the person's Personal Works under their
	// definition and the Works with an input source they hold a Work Share
	// in, closed ones included, at most limit.
	ListLibraryWorks(ctx context.Context, person, agentID string, limit int) ([]LibraryWork, error)
}
