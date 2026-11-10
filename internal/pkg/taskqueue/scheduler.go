package taskqueue

import (
	"errors"
	"log/slog"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	taskasynq "github.com/go-jimu/contrib/taskqueue/asynq"
	"github.com/hibiken/asynq"
	"go.uber.org/fx"
)

type PeriodicTaskIn struct {
	fx.In

	PeriodicTasks []componenttaskqueue.PeriodicTask `group:"taskqueue.periodic_tasks"`
}

func NewPeriodicTaskScheduler(
	lc fx.Lifecycle,
	cfg Config,
	logger *slog.Logger,
	in PeriodicTaskIn,
) (componenttaskqueue.PeriodicTaskScheduler, error) {
	scheduler := taskasynq.NewRedisScheduler(redisConn(cfg), schedulerOpts(logger))
	registered := 0
	for _, periodic := range in.PeriodicTasks {
		if err := scheduler.RegisterPeriodicTask(periodic); err != nil {
			return nil, err
		}
		registered++
		logPeriodicTaskRegistered(logger, periodic)
	}
	if registered == 0 {
		return scheduler, nil
	}

	appendRunnerLifecycle(lc, scheduler)
	return scheduler, nil
}

func schedulerOpts(logger *slog.Logger) *asynq.SchedulerOpts {
	return &asynq.SchedulerOpts{
		Logger:              taskasynq.NewLogger(logger),
		EnqueueErrorHandler: logRefusedEnqueue(logger),
	}
}

func logPeriodicTaskRegistered(logger *slog.Logger, periodic componenttaskqueue.PeriodicTask) {
	if logger == nil {
		return
	}
	task := periodic.Task()
	schedule := periodic.Schedule()
	logger.Info("taskqueue periodic task registered",
		slog.String("name", periodic.Name()),
		slog.String("schedule_kind", string(schedule.Kind())),
		slog.String("task_type", string(task.Type())),
		slog.String("queue", task.Queue()),
	)
}

// logRefusedEnqueue reports a periodic enqueue the provider refused, such as a
// key still held by an earlier instance; asynq otherwise drops the error and the
// periodic task stops without a trace. The deprecated handler is the only hook
// that carries the refused task.
func logRefusedEnqueue(logger *slog.Logger) func(*asynq.Task, []asynq.Option, error) {
	return func(task *asynq.Task, opts []asynq.Option, err error) {
		queue := "default"
		for _, opt := range opts {
			if opt.Type() == asynq.QueueOpt {
				if name, ok := opt.Value().(string); ok {
					queue = name
				}
			}
		}
		attrs := []any{slog.String("task_type", task.Type()), slog.String("queue", queue), slog.Any("error", err)}
		// Every replica runs the scheduler, so on each tick all but one fire
		// meet the winner's uniqueness lock: expected, kept as a trace for a
		// lock an earlier fire never released, but not an error.
		if errors.Is(err, asynq.ErrDuplicateTask) || errors.Is(err, asynq.ErrTaskIDConflict) {
			logger.Info("periodic task enqueue skipped: its uniqueness lock is held", attrs...)
			return
		}
		logger.Error("periodic task enqueue failed", attrs...)
	}
}
