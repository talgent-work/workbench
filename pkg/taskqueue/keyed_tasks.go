package taskqueue

import (
	"context"
	"errors"
	"strings"

	"github.com/hibiken/asynq"
	"go.uber.org/fx"
)

// KeyedTasks tells whether the task holding a key still waits or runs. A
// completed or archived task, or none, holds nothing.
type KeyedTasks struct {
	inspector *asynq.Inspector
}

// NewKeyedTasks reads the tasks of the queues on the configured Redis.
func NewKeyedTasks(lc fx.Lifecycle, cfg Config) *KeyedTasks {
	inspector := asynq.NewInspector(redisConn(cfg))
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return inspector.Close() }})
	return &KeyedTasks{inspector: inspector}
}

// Pending says the task keyed key in queue, the lane its definition names,
// is pending, scheduled, retrying or active.
func (k *KeyedTasks) Pending(queue, key string) (bool, error) {
	if strings.TrimSpace(queue) == "" {
		queue = DefaultQueueName
	}
	info, err := k.inspector.GetTaskInfo(queue, key)
	if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch info.State {
	case asynq.TaskStatePending, asynq.TaskStateScheduled, asynq.TaskStateRetry, asynq.TaskStateActive, asynq.TaskStateAggregating:
		return true, nil
	}
	return false, nil
}
