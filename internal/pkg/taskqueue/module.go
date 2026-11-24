// Package taskqueue runs the task lanes on Asynq: one named lane per kind of
// deferred work, each with its own Redis settings, worker, scheduler and keyed
// enqueuer. Bounded contexts contribute processors and periodic tasks under
// their lane's tags; the lane registers them before it starts, and
// pkg.Lifecycle starts and stops every lane.
package taskqueue

import (
	"fmt"
	"log/slog"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"
)

// LanesTag collects every lane, for pkg.Lifecycle to start and stop.
const LanesTag = `group:"taskqueue.lanes"`

// Module owns the lane named name; process composition supplies its Option
// under the same name. Its enqueuer, schema registry and keyed-task reader
// are named; Bind selects them for one bounded context. Processors and
// periodic tasks tagged for another lane are not registered with this one.
func Module(name string) fx.Option {
	tag := fmt.Sprintf(`name:%q`, name)
	return fx.Module("internal.pkg.taskqueue."+name,
		fx.Provide(
			fx.Annotate(componenttaskqueue.NewSchemaRegistry, fx.ResultTags(tag)),
			fx.Annotate(NewEnqueuer, fx.ParamTags(tag, ""), fx.ResultTags(tag)),
			fx.Annotate(NewKeyedTasks, fx.ParamTags(tag), fx.ResultTags(tag)),
			fx.Annotate(
				func(option Option, logger *slog.Logger, processors []componenttaskqueue.Processor, periodic []componenttaskqueue.PeriodicTask) (*Lane, error) {
					return NewLane(name, option, logger, processors, periodic)
				},
				fx.ParamTags(tag, "", groupTag(processorGroup(name), false), groupTag(periodicTaskGroup(name), false)),
				fx.ResultTags(LanesTag),
			),
		),
	)
}

// Bind gives a business module access to its selected lane's enqueuer,
// schemas and keyed tasks without exporting an ambiguous process-wide
// default.
func Bind(name string) fx.Option {
	tag := fmt.Sprintf(`name:%q`, name)
	return fx.Provide(
		fx.Annotate(func(client componenttaskqueue.Enqueuer) componenttaskqueue.Enqueuer { return client }, fx.ParamTags(tag)),
		fx.Annotate(func(registry *componenttaskqueue.SchemaRegistry) *componenttaskqueue.SchemaRegistry { return registry }, fx.ParamTags(tag)),
		fx.Annotate(func(tasks *KeyedTasks) *KeyedTasks { return tasks }, fx.ParamTags(tag)),
		fx.Private,
	)
}
