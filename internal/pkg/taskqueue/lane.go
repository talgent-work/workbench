package taskqueue

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	taskasynq "github.com/go-jimu/contrib/taskqueue/asynq"
	"github.com/hibiken/asynq"
)

const (
	defaultConcurrency     = 10
	defaultShutdownTimeout = 30 * time.Second
)

// ErrRetryLater marks a task that could not run yet, such as one whose
// downstream is busy: it is retried after the queue's retry delay without
// counting as a failed attempt, so only its deadline ends it. The provider
// checks exhaustion first, so such a task must allow at least one retry.
var ErrRetryLater = errors.New("taskqueue: retry later")

// Lane is one configured queue's worker and, when it has periodic tasks, its
// scheduler: an entry point whose Start and Stop pkg.Lifecycle orders.
type Lane struct {
	name      string
	worker    *taskasynq.Worker
	scheduler *taskasynq.Scheduler
}

// NewLane registers the lane's processors, each logged and recovered, and its
// periodic tasks. Nothing starts until Start.
func NewLane(name string, option Option, logger *slog.Logger, processors []componenttaskqueue.Processor, periodic []componenttaskqueue.PeriodicTask) (*Lane, error) {
	if err := option.Validate(); err != nil {
		return nil, err
	}
	worker := taskasynq.NewRedisWorker(redisConn(option), asynq.Config{
		Concurrency:     orDefault(option.WorkerConcurrency, defaultConcurrency),
		Queues:          workerQueues(option),
		Logger:          taskasynq.NewLogger(logger),
		ShutdownTimeout: orDefault(option.ShutdownTimeout.Duration(), defaultShutdownTimeout),
		RetryDelayFunc:  retryDelay(option),
		IsFailure:       func(err error) bool { return !errors.Is(err, ErrRetryLater) },
		ErrorHandler:    exhaustionLog(logger),
		// A retry becomes runnable only when the provider next checks delayed
		// tasks; check as often as the shortest configured delay so the delay
		// is not stretched to the provider's five-second default.
		DelayedTaskCheckInterval: delayedTaskCheck(option),
		TaskCheckInterval:        option.taskCheckInterval,
	})
	for _, processor := range processors {
		wrapped := componenttaskqueue.NewProcessor(processor.TaskType(), componenttaskqueue.Chain(
			processor.Process,
			componenttaskqueue.Logging(logger),
			componenttaskqueue.Recover(),
		))
		if err := worker.Register(wrapped); err != nil {
			return nil, err
		}
	}
	lane := &Lane{name: name, worker: worker}
	if len(periodic) == 0 {
		return lane, nil
	}
	scheduler := taskasynq.NewRedisScheduler(redisConn(option), &asynq.SchedulerOpts{
		Logger:              taskasynq.NewLogger(logger),
		EnqueueErrorHandler: logRefusedEnqueue(logger),
	})
	for _, task := range periodic {
		if err := scheduler.RegisterPeriodicTask(task); err != nil {
			return nil, err
		}
		logger.Info("taskqueue periodic task registered",
			slog.String("name", task.Name()),
			slog.String("schedule_kind", string(task.Schedule().Kind())),
			slog.String("task_type", string(task.Task().Type())),
			slog.String("queue", task.Task().Queue()))
	}
	lane.scheduler = scheduler
	return lane, nil
}

// Name is the lane's name.
func (l *Lane) Name() string { return l.name }

// Start launches the worker and the scheduler and returns.
func (l *Lane) Start(ctx context.Context) error {
	if err := l.worker.Start(ctx); err != nil {
		return err
	}
	if l.scheduler == nil {
		return nil
	}
	if err := l.scheduler.Start(ctx); err != nil {
		return errors.Join(err, l.worker.Shutdown(ctx))
	}
	return nil
}

// Stop stops scheduling and lets running tasks finish within the lane's
// shutdown timeout, bounded by ctx.
func (l *Lane) Stop(ctx context.Context) error {
	var err error
	if l.scheduler != nil {
		err = l.scheduler.Shutdown(ctx)
	}
	return errors.Join(err, l.worker.Shutdown(ctx))
}

// exhaustionLog logs, as an error, every failure that ends a task: one that
// exhausted its retry ceiling, or one that asked to skip retries. The provider
// archives such a task, and nothing else shows that it gave up. Earlier
// failures are retried and logged by the processor chain.
func exhaustionLog(logger *slog.Logger) asynq.ErrorHandlerFunc {
	return func(ctx context.Context, task *asynq.Task, err error) {
		retried, _ := asynq.GetRetryCount(ctx)
		ceiling, _ := asynq.GetMaxRetry(ctx)
		skipped := errors.Is(err, asynq.SkipRetry)
		if retried < ceiling && !skipped {
			return
		}
		id, _ := asynq.GetTaskID(ctx)
		queue, _ := asynq.GetQueueName(ctx)
		logger.ErrorContext(ctx, "task retries exhausted", slog.String("task_type", task.Type()), slog.String("task_id", id), slog.String("queue", queue),
			slog.Int("retried", retried), slog.Int("max_retry", ceiling), slog.Bool("skipped_retry", skipped), slog.Any("error", err))
	}
}

// retryDelay doubles from the configured minimum up to the maximum, with up to
// a quarter of jitter so retries of many tasks spread out. Without a configured
// range it keeps the provider default.
func retryDelay(option Option) asynq.RetryDelayFunc {
	low, high := option.RetryDelayMin.Duration(), option.RetryDelayMax.Duration()
	if high <= 0 {
		return asynq.DefaultRetryDelayFunc
	}
	return func(attempt int, _ error, _ *asynq.Task) time.Duration {
		delay := low
		for range min(attempt, 32) {
			if delay >= high/2 {
				delay = high
				break
			}
			delay *= 2
		}
		delay = min(delay, high)
		return delay - time.Duration(rand.Int64N(int64(delay)/4+1))
	}
}

func delayedTaskCheck(option Option) time.Duration {
	low := option.RetryDelayMin.Duration()
	if low <= 0 {
		return 0
	}
	return max(low, 100*time.Millisecond)
}

func workerQueues(option Option) map[string]int {
	queues := make(map[string]int)
	for _, queue := range option.QueueNames {
		queue = strings.TrimSpace(queue)
		if queue != "" {
			queues[queue] = 1
		}
	}
	if len(queues) == 0 {
		queue := strings.TrimSpace(option.QueueName)
		if queue == "" {
			queue = DefaultQueueName
		}
		queues[queue] = 1
	}
	return queues
}

// logRefusedEnqueue reports a periodic enqueue the provider refused, such as a
// key still held by an earlier instance; asynq otherwise drops the error and
// the periodic task stops without a trace. The deprecated handler is the only
// hook that carries the refused task.
func logRefusedEnqueue(logger *slog.Logger) func(*asynq.Task, []asynq.Option, error) {
	return func(task *asynq.Task, opts []asynq.Option, err error) {
		queue := DefaultQueueName
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

func orDefault[T int | time.Duration](value, fallback T) T {
	if value <= 0 {
		return fallback
	}
	return value
}
