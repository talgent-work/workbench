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
	"go.uber.org/fx"
)

const defaultShutdownTimeout = 30 * time.Second

// ErrRetryLater marks a task that could not run yet, such as one whose
// downstream is busy: it is retried after the queue's retry delay without
// counting as a failed attempt, so only its deadline ends it. The provider
// checks exhaustion first, so such a task must allow at least one retry.
var ErrRetryLater = errors.New("taskqueue: retry later")

type ProcessorIn struct {
	fx.In

	Processors []componenttaskqueue.Processor `group:"taskqueue.processors"`
}

// Worker and scheduler Start launch their background work and return. Fx must
// observe that return before publishing readiness or invoking Shutdown.
func appendRunnerLifecycle(lc fx.Lifecycle, runner componenttaskqueue.Worker) {
	lc.Append(fx.Hook{OnStart: runner.Start, OnStop: runner.Shutdown})
}

func NewSchemaRegistry() *componenttaskqueue.SchemaRegistry {
	return componenttaskqueue.NewSchemaRegistry()
}

func NewEnqueuer(lc fx.Lifecycle, cfg Config, logger *slog.Logger) componenttaskqueue.Enqueuer {
	client := taskasynq.NewRedisClient(redisConn(cfg))
	inspector := asynq.NewInspector(redisConn(cfg))
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			return errors.Join(client.Close(), inspector.Close())
		},
	})
	return duplicateTolerantEnqueuer{next: client, tasks: inspector, logger: logger}
}

// keyedTasks reads and removes the task holding a key.
type keyedTasks interface {
	GetTaskInfo(queue, id string) (*asynq.TaskInfo, error)
	DeleteTask(queue, id string) error
}

type duplicateTolerantEnqueuer struct {
	next   componenttaskqueue.Enqueuer
	tasks  keyedTasks
	logger *slog.Logger
}

func (e duplicateTolerantEnqueuer) Enqueue(ctx context.Context, task componenttaskqueue.Task, opts ...componenttaskqueue.EnqueueOption) error {
	if e.next == nil {
		return taskasynq.ErrNilClient
	}
	err := e.next.Enqueue(ctx, task, opts...)
	// A keyed task holds both its ID and, when unique, its uniqueness lock, so
	// either refusal may come from an archived holder.
	if task.ID() != "" && (errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask)) && e.replaceArchived(task) {
		err = e.next.Enqueue(ctx, task, opts...)
	}
	// A keyed task that already exists covers this enqueue: it has not run yet,
	// is waiting to retry, or is running and will read the current state.
	if errors.Is(err, asynq.ErrDuplicateTask) || errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// replaceArchived deletes the archived task holding the key, if that is what
// holds it. An archived task keeps its ID yet never runs again, so it cannot
// cover a new enqueue; leaving it would drop every later enqueue of the key.
func (e duplicateTolerantEnqueuer) replaceArchived(task componenttaskqueue.Task) bool {
	queue := task.Queue()
	if queue == "" {
		queue = "default"
	}
	info, err := e.tasks.GetTaskInfo(queue, task.ID())
	if err != nil || info.State != asynq.TaskStateArchived {
		return false
	}
	if err := e.tasks.DeleteTask(queue, task.ID()); err != nil && !errors.Is(err, asynq.ErrTaskNotFound) {
		e.logger.Error("archived task holding a key could not be replaced", slog.String("task_type", string(task.Type())), slog.String("queue", queue), slog.String("task_id", task.ID()), slog.Any("error", err))
		return false
	}
	e.logger.Warn("archived task replaced by a new enqueue of its key", slog.String("task_type", string(task.Type())), slog.String("queue", queue), slog.String("task_id", task.ID()), slog.String("last_error", info.LastErr))
	return true
}

func NewWorker(
	lc fx.Lifecycle,
	cfg Config,
	logger *slog.Logger,
	in ProcessorIn,
) (componenttaskqueue.Worker, error) {
	concurrency := cfg.WorkerConcurrency
	if concurrency <= 0 {
		concurrency = 10
	}
	shutdown := cfg.ShutdownTimeout.Duration()
	if shutdown <= 0 {
		shutdown = defaultShutdownTimeout
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	worker := taskasynq.NewRedisWorker(redisConn(cfg), asynq.Config{
		Concurrency:     concurrency,
		Queues:          workerQueues(cfg),
		Logger:          taskasynq.NewLogger(logger),
		ShutdownTimeout: shutdown,
		RetryDelayFunc:  retryDelay(cfg),
		IsFailure:       func(err error) bool { return !errors.Is(err, ErrRetryLater) },
		// A retry becomes runnable only when the provider next checks delayed
		// tasks; check as often as the shortest configured delay so the delay
		// is not stretched to the provider's five-second default.
		DelayedTaskCheckInterval: delayedTaskCheck(cfg),
	})

	for _, processor := range in.Processors {
		wrapped := componenttaskqueue.NewProcessor(
			processor.TaskType(),
			componenttaskqueue.Chain(
				processor.Process,
				componenttaskqueue.Logging(logger),
				componenttaskqueue.Recover(),
			),
		)
		if err := worker.Register(wrapped); err != nil {
			return nil, err
		}
	}

	appendRunnerLifecycle(lc, worker)
	return worker, nil
}

// retryDelay doubles from the configured minimum up to the maximum, with up to
// a quarter of jitter so retries of many tasks spread out. Without a configured
// range it keeps the provider default.
func retryDelay(cfg Config) asynq.RetryDelayFunc {
	low, high := cfg.RetryDelayMin.Duration(), cfg.RetryDelayMax.Duration()
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

func delayedTaskCheck(cfg Config) time.Duration {
	low := cfg.RetryDelayMin.Duration()
	if low <= 0 {
		return 0
	}
	return max(low, 100*time.Millisecond)
}

func workerQueues(cfg Config) map[string]int {
	queues := make(map[string]int)
	for _, queue := range cfg.QueueNames {
		queue = strings.TrimSpace(queue)
		if queue != "" {
			queues[queue] = 1
		}
	}
	if len(queues) == 0 {
		queue := strings.TrimSpace(cfg.QueueName)
		if queue == "" {
			queue = DefaultQueueName
		}
		queues[queue] = 1
	}
	return queues
}

func redisConn(cfg Config) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}
}
