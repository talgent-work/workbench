package taskqueue

import (
	"context"
	"errors"
	"log/slog"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	taskasynq "github.com/go-jimu/contrib/taskqueue/asynq"
	"github.com/hibiken/asynq"
)

// keyedEnqueuer makes a keyed task idempotent per key: the task already
// holding the key covers the enqueue while it waits, retries or runs. The
// adapter replaces an archived holder, which never runs again.
type keyedEnqueuer struct {
	next componenttaskqueue.Enqueuer
}

// NewEnqueuer enqueues on the lane's Redis. Its connections stay open until
// the process exits, so Domain Event handlers still enqueue while the process
// drains.
func NewEnqueuer(option Option, logger *slog.Logger) componenttaskqueue.Enqueuer {
	return keyedEnqueuer{
		next: taskasynq.NewRedisClient(redisConn(option),
			taskasynq.WithArchivedReplacement(asynq.NewInspector(redisConn(option)), logger)),
	}
}

func (e keyedEnqueuer) Enqueue(ctx context.Context, task componenttaskqueue.Task, opts ...componenttaskqueue.EnqueueOption) error {
	if err := e.next.Enqueue(ctx, task, opts...); !errors.Is(err, componenttaskqueue.ErrDuplicateTask) {
		return err
	}
	return nil
}

func redisConn(option Option) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: option.RedisAddr, Password: option.RedisPassword, DB: option.RedisDB}
}
