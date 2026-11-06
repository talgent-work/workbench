package domain

import (
	"context"
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

// process is the Harness process an admitted start begins: seated at
// admission, or waiting for its Desk.
func (a StartAdmission) process(mode ContextMode, waitingSince time.Time) *HarnessProcess {
	process := &HarnessProcess{ContextMode: mode, StartingSince: a.AdmittedAt.UTC(), DeskID: a.Seat.DeskID, DeskWaiting: a.Seat.Waiting, WaitingSince: waitingSince, Soul: a.Soul}
	if !a.Seat.Waiting {
		process.SeatedAt = process.StartingSince
	}
	return process
}

// holdsRoom reports whether the run still holds its Desk's room: until its
// process ended or its environment was lost and its compute is released, or
// its start was rejected before any compute existed.
func (p *HarnessProcess) holdsRoom() bool {
	if p == nil || p.StartRejected {
		return false
	}
	return !((p.ended() || p.EnvironmentLost) && p.ComputeReclaimed)
}

// MayHoldEnvironment reports whether the Work's current run may still be in
// its execution environment, or wait for one: its compute is not released.
func (s *AgentWork) MayHoldEnvironment() bool {
	return s.HarnessProcess != nil && !s.HarnessProcess.ComputeReclaimed && s.State != WorkClosed
}

// ComputerRemovedReason explains the failure of a run whose Managed Computer
// its owner removed.
const ComputerRemovedReason = "The Work's Computer was removed."

// ApplyComputerRemoval applies the removal of the Work's Managed Computer,
// which confirms the permanent loss of its execution environment there
// (Agent model: Computer removal), through Reconcile process: a Starting,
// Working or Stopping Work becomes Failed, a start still waiting for its
// Desk included, and a Closing one Closed. A Work whose run already left its
// environment is unchanged; a Stopped or Failed Work can then only be
// closed, since its removed Computer rejects every run. The result reports
// whether the Work changed.
func (s *AgentWork) ApplyComputerRemoval(ctx context.Context) (bool, error) {
	if !s.MayHoldEnvironment() {
		return false, nil
	}
	err := s.ReconcileProcess(ctx, ProcessObservation{State: ProcessEnvironmentLost, ComputeReclaimed: true, LossReason: ComputerRemovedReason}, nil)
	return true, err
}
