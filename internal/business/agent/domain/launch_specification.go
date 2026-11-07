package domain

import (
	"time"
)

// LaunchSpecification is what one admitted start asks its Computer's
// execution realization to run.
type LaunchSpecification struct {
	RepositorySelection WorkRepositorySelection
	// RemainingRepositories are the selected repositories this run checks
	// out: those without a checkout outcome when the start was admitted.
	RemainingRepositories  []string
	AuthorizationSelection WorkAuthorizationSelection
	ThinkingLevel          string
	// PermissionMode is a Managed Work's; empty on a Cloud, which bypasses.
	PermissionMode PermissionMode
	WorkModel      WorkModelSelection
	AgentID        string
	WorkID         string
	// ComputerID is the Work's Computer; the execution port reaches it
	// through the realization of its kind.
	ComputerID string
	// ComputerName is what Computer calls the Work's Computer when the
	// execution port realizes the start, which fills it in; the run's
	// telemetry names its Computer by it. It is not part of the admitted
	// start, so a later rename changes no repeated registration.
	ComputerName string
	// ProjectID is the Work's Project; empty for a Work without one.
	ProjectID               string
	Capabilities            CapabilitySnapshot
	HarnessSessionReference string
	ContextMode             ContextMode
	// Soul is appended to the startup Agent's prompt, never replacing it, and
	// is not part of the Capability Snapshot.
	Soul string
	// PreparationBound is when the in-environment preparation of this start
	// (Plugin installation, then repository checkout) degrades.
	PreparationBound   time.Time
	WorkspaceReference string
	InputSource        WorkInputSource
	ReadOnlyMounts     []ReadOnlyMount
	HomeBinding        HomeBinding
	TraceAttributes    map[string]string
	// StartingContext is the context the Work started from, if any; a start
	// without a conversation of its own yet takes it up.
	StartingContext *StartingContext
}

// HarnessExit is the acknowledged exit fact of the Work's current Harness
// process: its exit code and the failure Dispatcher observed, if any. It is a
// Value Object without process identity.
type HarnessExit struct {
	ExitCode int
	Reason   string
}
