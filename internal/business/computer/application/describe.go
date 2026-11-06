package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// ComputerDescription is what a Computer is: its kind, its name, its owner
// and whether it was removed.
type ComputerDescription struct {
	ID      string
	Kind    domain.Kind
	Name    string
	OwnerID string
	Removed bool
	// Presence is whether the platform reaches it now, and since when it
	// does not.
	Presence domain.Presence
}

// DescribeComputer says what a Computer is, removed or not, so Agent reaches
// it through the matching realization of its execution port. An unknown
// Computer fails with domain.ErrComputerNotFound.
func (a *Application) DescribeComputer(ctx context.Context, computerID string) (ComputerDescription, error) {
	computer, err := a.computers.Get(ctx, computerID)
	if err != nil {
		return ComputerDescription{}, err
	}
	return ComputerDescription{ID: computer.ID, Kind: computer.Kind, Name: computer.Name, OwnerID: computer.OwnerID, Removed: computer.State == domain.StateRemoved, Presence: computer.Presence}, nil
}
