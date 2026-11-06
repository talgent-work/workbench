package taskqueue

import (
	"fmt"
	"log/slog"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"
)

// NamedModule owns a worker, scheduler and client for one configured taskqueue.
// Processor groups are scoped as well: combining processes must not register
// another owner's processors or periodic schedules with this worker.
func NamedModule(name string) fx.Option {
	tag := fmt.Sprintf(`name:%q`, name)
	return fx.Module("taskqueue."+name,
		fx.Provide(
			fx.Annotate(NewSchemaRegistry, fx.ResultTags(tag)),
			fx.Annotate(NewEnqueuer, fx.ParamTags("", tag, ""), fx.ResultTags(tag)),
			fx.Annotate(NewKeyedTasks, fx.ParamTags("", tag), fx.ResultTags(tag)),
			fx.Annotate(newNamedWorker,
				fx.ParamTags("", tag, "", groupTag(processorGroup(name), false)),
				fx.ResultTags(tag)),
			fx.Annotate(newNamedScheduler,
				fx.ParamTags("", tag, "", groupTag(periodicTaskGroup(name), false)),
				fx.ResultTags(tag)),
		),
		fx.Invoke(fx.Annotate(
			func(componenttaskqueue.Worker, componenttaskqueue.PeriodicTaskScheduler) {},
			fx.ParamTags(tag, tag),
		)),
	)
}

// Bind gives a business module access to its selected queue's client and
// schemas without exporting an ambiguous process-wide default.
func Bind(name string) fx.Option {
	tag := fmt.Sprintf(`name:%q`, name)
	return fx.Provide(
		fx.Annotate(func(client componenttaskqueue.Enqueuer) componenttaskqueue.Enqueuer { return client }, fx.ParamTags(tag)),
		fx.Annotate(func(registry *componenttaskqueue.SchemaRegistry) *componenttaskqueue.SchemaRegistry { return registry }, fx.ParamTags(tag)),
		fx.Annotate(func(tasks *KeyedTasks) *KeyedTasks { return tasks }, fx.ParamTags(tag)),
		fx.Private,
	)
}

func newNamedWorker(lc fx.Lifecycle, cfg Config, logger *slog.Logger, processors []componenttaskqueue.Processor) (componenttaskqueue.Worker, error) {
	return NewWorker(lc, cfg, logger, ProcessorIn{Processors: processors})
}

func newNamedScheduler(lc fx.Lifecycle, cfg Config, logger *slog.Logger, tasks []componenttaskqueue.PeriodicTask) (componenttaskqueue.PeriodicTaskScheduler, error) {
	return NewPeriodicTaskScheduler(lc, cfg, logger, PeriodicTaskIn{PeriodicTasks: tasks})
}
