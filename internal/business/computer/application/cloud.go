package application

import (
	"context"
	"errors"
	"log/slog"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// Cloud is this cluster's Cloud as the platform, its owner, sets it.
type Cloud struct {
	ID       string
	Name     string
	Capacity domain.Room
	// RunDemand is the compute one run takes.
	RunDemand domain.Room
}

// EnsureCloud registers this cluster's Cloud once and keeps its room rules
// as the platform sets them. A Cloud admits by rejecting when full, as
// before Clouds were Computers. A Capacity below the room occupied now is
// refused, and taken up by applyCloudRules once enough Desks have left.
func (a *Application) EnsureCloud(ctx context.Context, logger *slog.Logger) error {
	return retryConcurrent(ctx, func() error {
		computer, err := a.computers.Get(ctx, a.cloud.ID)
		if errors.Is(err, domain.ErrComputerNotFound) {
			computer, err = domain.NewCloud(a.cloud.ID, a.cloud.Name, a.cloud.Capacity, domain.AdmissionReject)
			if err != nil {
				return err
			}
			return a.computers.Save(ctx, computer)
		}
		if err != nil {
			return err
		}
		if err := computer.SetRoomRules(a.cloud.Capacity, domain.AdmissionReject); err != nil {
			if errors.Is(err, domain.ErrCapacityBelowOccupied) {
				logger.WarnContext(ctx, "cloud capacity kept until occupied desks leave", slog.String("computer_id", computer.ID))
				return nil
			}
			return err
		}
		return a.computers.Save(ctx, computer)
	})
}

// applyCloudRules takes up the platform's room rules for this cluster's Cloud
// once its occupied room fits them. It changes nothing on another Computer,
// or while the rules still cannot apply.
func (a *Application) applyCloudRules(computer *domain.Computer) error {
	if computer.ID != a.cloud.ID || (computer.Capacity == a.cloud.Capacity && computer.Policy == domain.AdmissionReject) {
		return nil
	}
	if err := computer.SetRoomRules(a.cloud.Capacity, domain.AdmissionReject); err != nil && !errors.Is(err, domain.ErrCapacityBelowOccupied) {
		return err
	}
	return nil
}
