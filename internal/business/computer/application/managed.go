package application

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// ManagedComputer is one of a person's Managed Computers as they see it.
type ManagedComputer struct {
	ID       string
	Name     string
	Presence domain.Presence
	Versions domain.ReportedVersions
	// NeverConnected tells that its machine has not connected since it was
	// registered: every connection reports its Talgent Computer version.
	NeverConnected bool
	RegisteredAt   time.Time
	// Desks is how many Works it runs at once; Occupied how many it runs
	// now, and Waiting how many wait in its line.
	Desks, Occupied int64
	Waiting         int
	Policy          domain.AdmissionPolicy
}

// ManagedPresence is whether one Managed Computer is reachable.
type ManagedPresence struct {
	ID        string
	Reachable bool
}

// ListManagedComputers lists a person's own Managed Computers, newest first.
func (a *Application) ListManagedComputers(ctx context.Context, ownerID string) ([]ManagedComputer, error) {
	return a.queries.ListManagedComputers(ctx, ownerID)
}

// RenameComputer renames one of the person's own Managed Computers; another
// person's is not found.
func (a *Application) RenameComputer(ctx context.Context, ownerID, computerID, name string) (ManagedComputer, error) {
	err := a.changeComputer(ctx, computerID, func(computer *domain.Computer) error {
		if computer.Kind != domain.KindManaged || computer.OwnerID != ownerID {
			return domain.ErrComputerNotFound
		}
		return computer.Rename(name)
	})
	if err != nil {
		return ManagedComputer{}, err
	}
	return a.queries.GetManagedComputer(ctx, ownerID, computerID)
}
