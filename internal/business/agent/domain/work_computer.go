package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The Work's Computer refused the run for a reason other than room.
var (
	ErrComputerNotUsable   = errors.New("agent: the Computer cannot be used for this Work")
	ErrComputerUnreachable = errors.New("agent: the Computer is unreachable")
	ErrComputerRemoved     = errors.New("agent: the Computer was removed")
	ErrComputerOutdated    = errors.New("agent: the Computer's Talgent Computer is below the minimum version")
	// ErrComputerNoSandbox refuses a run that asks for Claude Code's sandbox
	// on a machine that cannot provide it, such as native Windows; the
	// platform never runs such a Work without it (Agent model: Managed
	// sandbox).
	ErrComputerNoSandbox = errors.New("agent: the Computer cannot provide Claude Code's sandbox")
)

// ErrComputerFull refuses a run on a full Managed Computer that rejects runs
// while full; its owner may raise its Capacity or let it queue runs.
var ErrComputerFull = fmt.Errorf("%w: the Computer already runs as many Works as its Capacity allows", ErrCapacityUnavailable)

// ErrCloudOnlyAgent refuses a Managed Computer for a Work of an Agent that
// represents no person, such as a project-scoped one: its Works run on a
// Cloud, so they never depend on someone's machine.
var ErrCloudOnlyAgent = fmt.Errorf("%w: an Agent that represents no person runs only on a Cloud", ErrComputerNotUsable)

// AdmitComputer admits the kind of Computer a new Work of this definition
// runs on: a person's Agent runs on any Computer usable to that person; an
// Agent that represents no person only on a Cloud.
func (a *AgentDefinition) AdmitComputer(managed bool) error {
	if managed && !a.RepresentsHuman() {
		return ErrCloudOnlyAgent
	}
	return nil
}

// RefusedByComputer reports a run its Computer refused for a reason other
// than room.
func RefusedByComputer(err error) bool {
	return errors.Is(err, ErrComputerNotUsable) || errors.Is(err, ErrComputerUnreachable) || errors.Is(err, ErrComputerRemoved) || errors.Is(err, ErrComputerOutdated) || errors.Is(err, ErrComputerNoSandbox)
}

// ComputerSeat is a Computer's answer that admits one run: the Computer and
// the Desk the run holds there, seated or, when Waiting, in its line.
type ComputerSeat struct {
	ComputerID string
	DeskID     string
	Waiting    bool
}

func (s ComputerSeat) admitted() bool {
	return strings.TrimSpace(s.ComputerID) != "" && strings.TrimSpace(s.DeskID) != ""
}

// process is the Harness process an admitted start begins, with the Desk its
// Computer admitted it to, seated or waiting in its line.
func (a StartAdmission) process(mode ContextMode, waitingSince time.Time) *HarnessProcess {
	return &HarnessProcess{ContextMode: mode, StartingSince: a.AdmittedAt.UTC(), DeskID: a.Seat.DeskID, DeskWaiting: a.Seat.Waiting, WaitingSince: waitingSince, Soul: a.Soul}
}

// MayHoldEnvironment reports whether the Work's current run may still be in
// its execution environment, or wait for one: its compute is not released.
func (s *AgentWork) MayHoldEnvironment() bool {
	return s.HarnessProcess != nil && !s.HarnessProcess.ComputeReclaimed && s.State != WorkClosed
}

// ComputerRemovedReason explains the failure of a run whose Managed Computer
// its owner removed.
const ComputerRemovedReason = "The Work's Computer was removed."
