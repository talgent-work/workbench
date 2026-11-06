package application

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// RemovedComputer is a committed removal of a Managed Computer: Computer
// Removed, as Agent consumes it.
type RemovedComputer struct {
	ComputerID string
	At         time.Time
}

// Removals publishes Computer Removed after its commit, best effort: Agent
// also reads whether the Computer of each Work it holds a run for was
// removed, so a lost publication only delays the loss of those Works'
// environments.
type Removals interface {
	Publish(ctx context.Context, removed RemovedComputer)
}

type noRemovals struct{}

func (noRemovals) Publish(context.Context, RemovedComputer) {}

// RemoveComputer removes one of the person's own Managed Computers for good;
// another person's is not found. Its credential is revoked, and every Desk
// on it leaves in the same commit, so none of its runs starts or waits on
// it. Its machine's connection is ended by the next check of it, and Agent
// takes the removal as the loss of every Work's execution environment on it.
// Removing it again changes nothing.
func (a *Application) RemoveComputer(ctx context.Context, ownerID, computerID string) error {
	var removed []domain.Event
	err := retryConcurrent(ctx, func() error {
		removed = nil
		return a.transactions.Within(ctx, func(ctx context.Context) error {
			computer, err := a.computers.Get(ctx, computerID)
			if err != nil {
				return err
			}
			if computer.Kind != domain.KindManaged || computer.OwnerID != ownerID {
				return domain.ErrComputerNotFound
			}
			if err := computer.Remove(a.now()); err != nil {
				return err
			}
			events := computer.Events.Drain()
			if len(events) == 0 {
				return nil
			}
			if err := a.vacateDesks(ctx, computer); err != nil {
				return err
			}
			if err := a.computers.Save(ctx, computer); err != nil {
				return err
			}
			removed = events
			return nil
		})
	})
	if err != nil {
		return err
	}
	for _, event := range removed {
		if e, ok := event.(domain.ComputerRemoved); ok {
			a.logger.InfoContext(ctx, "computer: removed", "computer_id", e.ComputerID)
			a.removals.Publish(ctx, RemovedComputer{ComputerID: e.ComputerID, At: e.At})
		}
	}
	return nil
}

// vacateDesks makes every Desk on a removed Computer leave, its room or its
// place in line returning to the Computer (Vacate Removed Desks).
func (a *Application) vacateDesks(ctx context.Context, computer *domain.Computer) error {
	desks, err := a.desks.ListOpen(ctx, computer.ID)
	if err != nil {
		return err
	}
	for _, desk := range desks {
		desk.Leave()
		vacated, ok := lastVacated(desk.Events.Drain())
		if !ok {
			continue
		}
		if err := computer.FreeDesk(vacated); err != nil {
			return err
		}
		if err := a.desks.Save(ctx, desk); err != nil {
			return err
		}
	}
	return nil
}
