package marketplace

import (
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/scopemaintenance"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/tokenmaintenance"
	scmtaskprocessor "github.com/talgent-work/workbench/internal/business/marketplace/transport/taskprocessor"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
)

var TaskqueueModule = fx.Module(
	"domain.marketplace.connections.taskqueue",
	fx.Provide(
		func(p *tokenmaintenance.RefreshScanProcessor) scmtaskprocessor.RefreshScanProcessor { return p },
		func(p *tokenmaintenance.RefreshAuthorizationProcessor) scmtaskprocessor.RefreshAuthorizationProcessor {
			return p
		},
		fx.Annotate(
			scmtaskprocessor.NewRefreshScanTaskProcessor,
			fx.As(new(componenttaskqueue.Processor)),
			fx.ResultTags(pkgtaskqueue.ProcessorTag(Queue)),
		),
		fx.Annotate(
			scmtaskprocessor.NewRefreshAuthorizationTaskProcessor,
			fx.As(new(componenttaskqueue.Processor)),
			fx.ResultTags(pkgtaskqueue.ProcessorTag(Queue)),
		),
		fx.Annotate(
			newRefreshScanPeriodicTasks,
			fx.ResultTags(pkgtaskqueue.PeriodicTasksTag(Queue)),
		),
		func(p *scopemaintenance.ReconcileScanProcessor) scmtaskprocessor.ScopeReconcileScanProcessor {
			return p
		},
		func(p *scopemaintenance.ObserveProcessor) scmtaskprocessor.ScopeObserveProcessor { return p },
		fx.Annotate(
			scmtaskprocessor.NewScopeReconcileScanTaskProcessor,
			fx.As(new(componenttaskqueue.Processor)),
			fx.ResultTags(pkgtaskqueue.ProcessorTag(Queue)),
		),
		fx.Annotate(
			scmtaskprocessor.NewScopeObserveTaskProcessor,
			fx.As(new(componenttaskqueue.Processor)),
			fx.ResultTags(pkgtaskqueue.ProcessorTag(Queue)),
		),
		fx.Annotate(
			newScopeReconcilePeriodicTasks,
			fx.ResultTags(pkgtaskqueue.PeriodicTasksTag(Queue)),
		),
		registerMaintenanceTaskSchemas,
	),
)

type maintenanceTaskSchemasRegistered struct{}

func registerMaintenanceTaskSchemas(
	registry *componenttaskqueue.SchemaRegistry,
	cfg tokenmaintenance.Config,
) (maintenanceTaskSchemasRegistered, error) {
	if err := scmtaskprocessor.RegisterTaskSchemas(registry, cfg); err != nil {
		return maintenanceTaskSchemasRegistered{}, err
	}
	return maintenanceTaskSchemasRegistered{}, nil
}

func newRefreshScanPeriodicTasks(
	_ maintenanceTaskSchemasRegistered,
	cfg tokenmaintenance.Config,
	registry *componenttaskqueue.SchemaRegistry,
) ([]componenttaskqueue.PeriodicTask, error) {
	normalized, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	enabled, err := normalized.EnabledValue()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	interval := normalized.ScanInterval.Duration()
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	schedule, err := componenttaskqueue.IntervalSchedule(interval)
	if err != nil {
		return nil, err
	}
	task, err := registry.NewJSONTask(&tokenmaintenance.RefreshScanPayload{})
	if err != nil {
		return nil, err
	}
	periodic, err := componenttaskqueue.NewPeriodicTask(
		"scm.github_authorization_refresh_scan",
		schedule,
		task,
		componenttaskqueue.WithUnique(tokenmaintenance.RefreshScanUniqueTTL(normalized)),
	)
	if err != nil {
		return nil, err
	}
	return []componenttaskqueue.PeriodicTask{periodic}, nil
}

// The daily reconciliation corrects scopes a lost Provider notification left
// stale; the scan runs hourly and observes scopes older than a day.
func newScopeReconcilePeriodicTasks(
	_ maintenanceTaskSchemasRegistered,
	registry *componenttaskqueue.SchemaRegistry,
) ([]componenttaskqueue.PeriodicTask, error) {
	schedule, err := componenttaskqueue.IntervalSchedule(scopemaintenance.ScanInterval)
	if err != nil {
		return nil, err
	}
	task, err := registry.NewJSONTask(&scopemaintenance.ReconcileScanPayload{})
	if err != nil {
		return nil, err
	}
	periodic, err := componenttaskqueue.NewPeriodicTask("scm.provider_access_scope_reconcile_scan", schedule, task,
		componenttaskqueue.WithUnique(scopemaintenance.ScanInterval-time.Minute))
	if err != nil {
		return nil, err
	}
	return []componenttaskqueue.PeriodicTask{periodic}, nil
}
