package query

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// StandaloneWorkSummary is one Standalone Work as its owner's list shows it.
type StandaloneWorkSummary struct {
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
// one of their Standalone Works, or one they hold a Work Share in, shared by
// its owner.
type LibraryWork struct {
	WorkID, Title string
	// PublicationTarget is the File Space the Work publishes Artifacts into.
	PublicationTarget domain.Directory
	// Standalone tells the Work is a Standalone Work, whose File Spaces it owns.
	Standalone bool
	// SharedBy is the owner who shared it; empty for the person's own Work.
	SharedBy string
}
