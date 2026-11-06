package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// SetRoomRules asks to set one of the person's own Managed Computers'
// Capacity, in Desks, and Admission Policy.
type SetRoomRules struct {
	OwnerID    string
	ComputerID string
	Desks      int64
	Policy     domain.AdmissionPolicy
}

// SetRoomRules sets the Capacity and Admission Policy of one of the person's
// own Managed Computers; another person's is not found. Room it adds seats
// waiting Desks in the same commit, and their seating is published after it.
func (a *Application) SetRoomRules(ctx context.Context, request SetRoomRules) (ManagedComputer, error) {
	var seated []SeatedDesk
	err := retryConcurrent(ctx, func() error {
		seated = nil
		return a.transactions.Within(ctx, func(ctx context.Context) error {
			computer, err := a.computers.Get(ctx, request.ComputerID)
			if err != nil {
				return err
			}
			if computer.Kind != domain.KindManaged || computer.OwnerID != request.OwnerID {
				return domain.ErrComputerNotFound
			}
			if err := computer.SetRoomRules(domain.Room{Desks: request.Desks}, request.Policy); err != nil {
				return err
			}
			if err := a.computers.Save(ctx, computer); err != nil {
				return err
			}
			seated, err = a.seatAssigned(ctx, computer.Events.Drain(), a.now())
			return err
		})
	})
	if err != nil {
		return ManagedComputer{}, err
	}
	a.seatings.Publish(ctx, seated)
	return a.queries.GetManagedComputer(ctx, request.OwnerID, request.ComputerID)
}
