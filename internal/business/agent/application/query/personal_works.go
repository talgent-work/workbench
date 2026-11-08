package query

import "time"

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
