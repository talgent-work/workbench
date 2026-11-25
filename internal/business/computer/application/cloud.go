package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// Cloud is this cluster's Cloud as the platform, its owner, sets it. Its
// Capacity is not here: the cluster's execution quota supplies it at each
// admission (CloudCapacity).
type Cloud struct {
	ID   string
	Name string
	// RunDemand is the compute one run takes.
	RunDemand domain.Room
}

// EnsureCloud registers this cluster's Cloud once. A Cloud admits by
// rejecting when full, as before Clouds were Computers, and takes its
// Capacity from its cluster's quota when it admits a run.
func (a *Application) EnsureCloud(ctx context.Context) error {
	return retryConcurrent(ctx, func() error {
		_, err := a.computers.Get(ctx, a.cloud.ID)
		if !errors.Is(err, domain.ErrComputerNotFound) {
			return err
		}
		computer, err := domain.NewCloud(a.cloud.ID, a.cloud.Name, domain.Room{}, domain.AdmissionReject)
		if err != nil {
			return err
		}
		return a.computers.Save(ctx, computer)
	})
}
