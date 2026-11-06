package marketplace

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/go-jimu/components/taskqueue"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	catalogtask "github.com/talgent-work/workbench/internal/business/marketplace/application/task"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/marketplace/transport/taskprocessor"
	"github.com/talgent-work/workbench/pkg/connectlog"
	pkgtaskqueue "github.com/talgent-work/workbench/pkg/taskqueue"
	"go.uber.org/fx"
)

// Serving and acquisition share one CatalogStore inside the controlplane.
type SynchronizationConfig struct {
	Enabled         bool                                    `json:"enabled" yaml:"enabled" toml:"enabled"`
	IntervalSeconds int                                     `json:"interval-seconds" yaml:"interval-seconds" toml:"interval-seconds"`
	TimeoutSeconds  int                                     `json:"timeout-seconds" yaml:"timeout-seconds" toml:"timeout-seconds"`
	Acquisition     infrastructure.CatalogAcquisitionConfig `json:"acquisition" yaml:"acquisition" toml:"acquisition"`
}

func (c SynchronizationConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.IntervalSeconds <= 0 || c.TimeoutSeconds <= 0 || c.TimeoutSeconds >= c.IntervalSeconds {
		return errors.New("marketplace: positive sync timeout shorter than interval required")
	}
	return c.Acquisition.Validate()
}

var SynchronizationModule = fx.Module("marketplace.synchronization",
	fx.Provide(func(config Config) SynchronizationConfig { return config.Synchronization }),
	fx.Provide(infrastructure.NewSynchronizationRuns),
	fx.Provide(func(config SynchronizationConfig, store *infrastructure.CatalogStore) (application.CatalogSynchronizationPort, error) {
		if !config.Enabled {
			return nil, nil
		}
		return infrastructure.NewCatalogSynchronizer(config.Acquisition, store)
	}),
	fx.Provide(func(config SynchronizationConfig, runs *infrastructure.SynchronizationRuns, synchronizer application.CatalogSynchronizationPort, enqueuer taskqueue.Enqueuer, logger *slog.Logger) *application.CatalogSynchronization {
		return application.NewCatalogSynchronization(runs, synchronizer, enqueuer, time.Duration(config.TimeoutSeconds)*time.Second, config.Enabled, logger)
	}),
	fx.Provide(fx.Annotate(taskprocessor.NewSynchronizationProcessor, fx.ResultTags(pkgtaskqueue.ProcessorTag(Queue)))),
	fx.Provide(fx.Annotate(newSynchronizationPeriodicTasks, fx.ResultTags(pkgtaskqueue.PeriodicTasksTag(Queue)))),
	fx.Invoke(func(config Config, app *application.CatalogSynchronization, mux *http.ServeMux, logger *slog.Logger) {
		// This private service authenticates the platform token itself; it does not
		// require a delegated human or expose its methods through the public gateway.
		path, handler := marketplaceprivatev1connect.NewSynchronizationServiceHandler(transport.NewSynchronizationHandler(app, config.Connections.CredentialToken), connect.WithInterceptors(connectlog.NewInterceptor(logger)))
		mux.Handle(path, handler)
	}),
)

func newSynchronizationPeriodicTasks(config SynchronizationConfig) ([]taskqueue.PeriodicTask, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}
	schedule, err := taskqueue.IntervalSchedule(time.Duration(config.IntervalSeconds) * time.Second)
	if err != nil {
		return nil, err
	}
	task, err := catalogtask.NewSynchronization("")
	if err != nil {
		return nil, err
	}
	periodic, err := taskqueue.NewPeriodicTask("marketplace.synchronize", schedule, task, taskqueue.WithUnique(time.Duration(config.IntervalSeconds)*time.Second), taskqueue.WithMaxRetry(2), taskqueue.WithTimeout(30*time.Second))
	if err != nil {
		return nil, err
	}
	return []taskqueue.PeriodicTask{periodic}, nil
}
