package query

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// OwnedDefinition is an owner's definition located without loading it: its
// identity and the Work Caller of its Standalone Works, its owner.
type OwnedDefinition struct {
	AgentID         string
	OwnerWorkCaller domain.WorkCaller
}

// StandaloneWorksFilter narrows a definition's Standalone Works, the Works
// whose Work Caller is its owner: to those carrying every one of Labels and,
// when States is not empty, to those in one of these Work States. At most
// Limit are listed.
type StandaloneWorksFilter struct {
	Definition OwnedDefinition
	Labels     []domain.WorkLabel
	States     []domain.WorkState
	Limit      int
}

// StandaloneWorkSummary is one Standalone Work as its owner's list shows it.
type StandaloneWorkSummary struct {
	WorkID, Title string
	// Labels are its Work Labels; nil for none.
	Labels map[string]string
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

// WorkAttribution is one Standalone Work with the Work Labels it carries.
type WorkAttribution struct {
	WorkID string
	// Labels are its Work Labels; nil for none.
	Labels map[string]string
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
