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
	ProjectID string
	// Subject is the Work's Subject; empty for a Work without one.
	Subject WorkSubject
	// Purpose is the purpose of the Work's AgentDefinition, which beginning
	// the execution fills in; with the Work's other fixed facts it goes into
	// the Work's Identity Token.
	Purpose                 string
	Capabilities            CapabilitySnapshot
	HarnessSessionReference string
	ContextMode             ContextMode
	// Soul is appended to the startup Agent's prompt, never replacing it, and
	// is not part of the Capability Snapshot.
	Soul string
	// PreparationBound is when the in-environment preparation of this start
	// (Plugin installation, then repository checkout) degrades.
	PreparationBound time.Time
	// LocatedLayout is the Work's fixed layout: the Directories a Cloud run
	// mounts, its inputs and its publication target.
	LocatedLayout LocatedLayout
	// AgentHarness is the Work's Agent Harness, which the run launches as
	// it describes (Harness description).
	AgentHarness string
	// RepresentedPerson is the person the Work's Agent represents, whose
	// credentials the run uses; empty for none.
	RepresentedPerson string
	// OwningApplication is the Application that owns the Work, which its
	// Identity Token states; empty for none. Like Purpose, it is taken from
	// the definition when the run begins.
	OwningApplication string
	TraceAttributes   map[string]string
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
