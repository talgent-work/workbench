package account

import (
	"context"
	"fmt"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/account/application"
	applicationtask "github.com/talgent-work/workbench/internal/business/account/application/task"
	"github.com/talgent-work/workbench/internal/business/account/transport/taskprocessor"
)

// MaintenanceQueue is the taskqueue module Account rotates signing keys and
// forgets replaced refresh secrets on.
const MaintenanceQueue = applicationtask.MaintenanceQueue

// provisionSigningKey provisions the first signing key before the process
// serves, so the first sign-in finds one; the hourly task keeps them rotated.
// Every replica runs it; it is idempotent across replicas.
func provisionSigningKey(lc fx.Lifecycle, app *application.Application) {
	lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		if err := app.RotateSigningKeys(ctx); err != nil {
			return fmt.Errorf("account signing keys: %w", err)
		}
		return nil
	}})
}

func newMaintenanceProcessors(app *application.Application) []componenttaskqueue.Processor {
	return taskprocessor.NewMaintenanceProcessors(app)
}

func newMaintenanceTasks() ([]componenttaskqueue.PeriodicTask, error) {
	rotate, err := applicationtask.NewRotateSigningKeys()
	if err != nil {
		return nil, err
	}
	forget, err := applicationtask.NewForgetReplacedRefreshSecrets()
	if err != nil {
		return nil, err
	}
	return []componenttaskqueue.PeriodicTask{rotate, forget}, nil
}
