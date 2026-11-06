package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// Connections tells whether the Computer Gateway holds a connection of a
// Managed Computer now.
type Connections interface {
	Connected(ctx context.Context, computerIDs []string) (map[string]bool, error)
}

// MachineReport is what a Managed Computer's machine reports on each
// connection.
type MachineReport struct {
	Versions domain.ReportedVersions
	// Sandbox is whether it can provide Claude Code's sandbox.
	Sandbox bool
}

// ReportReachable records that a Managed Computer is reachable through a
// connection, with what its machine reports.
func (a *Application) ReportReachable(ctx context.Context, computerID string, report MachineReport) error {
	return a.changeComputer(ctx, computerID, func(computer *domain.Computer) error {
		if err := computer.ReportVersions(report.Versions); err != nil {
			return err
		}
		if err := computer.ReportSandbox(report.Sandbox); err != nil {
			return err
		}
		return computer.RegainReach()
	})
}

// ReportUnreachable records that the platform lost reach of a Managed
// Computer, unless a newer connection of it is open by the time the change
// commits.
func (a *Application) ReportUnreachable(ctx context.Context, computerID string, since time.Time) error {
	return a.changeComputer(ctx, computerID, func(computer *domain.Computer) error {
		connected, err := a.connections.Connected(ctx, []string{computerID})
		if err != nil {
			return err
		}
		if connected[computerID] {
			return errUnchanged
		}
		return computer.LoseReach(since)
	})
}

// presencePage bounds one page of the presence sweep.
const presencePage = 200

// SweepPresence brings each active Managed Computer's Presence in line with
// its connection record: one the Gateway lost without a report, as when a
// replica stopped abruptly, loses reach, and one recorded unreachable while
// its connection is open regains it, as when a late loss report raced its
// reconnection. A Computer that cannot be changed now is left to the next
// sweep; it returns how many it changed, and the failures.
func (a *Application) SweepPresence(ctx context.Context) (int, error) {
	changed, after := 0, ""
	var failures []error
	for {
		page, err := a.queries.ListManagedPresence(ctx, after, presencePage)
		if err != nil || len(page) == 0 {
			return changed, errors.Join(append(failures, err)...)
		}
		ids := make([]string, len(page))
		for i, presence := range page {
			ids[i] = presence.ID
		}
		connected, err := a.connections.Connected(ctx, ids)
		if err != nil {
			return changed, errors.Join(append(failures, err)...)
		}
		for _, presence := range page {
			if connected[presence.ID] == presence.Reachable {
				continue
			}
			if err := a.alignPresence(ctx, presence.ID); err != nil {
				failures = append(failures, fmt.Errorf("computer %s: %w", presence.ID, err))
				continue
			}
			changed++
		}
		after = page[len(page)-1].ID
	}
}

func (a *Application) alignPresence(ctx context.Context, computerID string) error {
	since := a.now()
	return a.changeComputer(ctx, computerID, func(computer *domain.Computer) error {
		connected, err := a.connections.Connected(ctx, []string{computerID})
		if err != nil {
			return err
		}
		if connected[computerID] {
			return computer.RegainReach()
		}
		return computer.LoseReach(since)
	})
}

// errUnchanged ends a change that has nothing to save.
var errUnchanged = errors.New("computer: unchanged")

// changeComputer saves one change of a Computer, retried on a concurrent
// one, and then publishes the Presence changes it committed.
func (a *Application) changeComputer(ctx context.Context, computerID string, change func(*domain.Computer) error) error {
	var committed []domain.Event
	err := retryConcurrent(ctx, func() error {
		committed = nil
		computer, err := a.computers.Get(ctx, computerID)
		if err != nil {
			return err
		}
		if err := change(computer); err != nil {
			if errors.Is(err, errUnchanged) {
				return nil
			}
			return err
		}
		if err := a.computers.Save(ctx, computer); err != nil {
			return err
		}
		committed = computer.Events.Drain()
		return nil
	})
	if err == nil {
		a.presences.Publish(ctx, presenceChanges(committed, a.now()))
	}
	return err
}

// PresenceChange is a committed change of a Managed Computer's Presence:
// Computer Became Unreachable or Computer Became Reachable, as Agent
// consumes it.
type PresenceChange struct {
	ComputerID string
	Reachable  bool
	// Since is when the platform lost reach, for a loss.
	Since time.Time
	// At is when the change committed.
	At time.Time
}

// Presences publishes Presence changes after their commit, best effort:
// Agent also reads the Presence of each Computer its Works run on, so a lost
// publication only delays their Disconnection record.
type Presences interface {
	Publish(ctx context.Context, changes []PresenceChange)
}

type noPresences struct{}

func (noPresences) Publish(context.Context, []PresenceChange) {}

func presenceChanges(events []domain.Event, at time.Time) []PresenceChange {
	var changes []PresenceChange
	for _, event := range events {
		switch e := event.(type) {
		case domain.ComputerBecameUnreachable:
			changes = append(changes, PresenceChange{ComputerID: e.ComputerID, Since: e.Since, At: at})
		case domain.ComputerBecameReachable:
			changes = append(changes, PresenceChange{ComputerID: e.ComputerID, Reachable: true, At: at})
		}
	}
	return changes
}
