package taskqueue

import (
	"errors"
	"strings"

	"github.com/hibiken/asynq"
)

// KeyedTasks tells whether the task holding a key still waits or runs. A
// completed or archived task, or none, holds nothing.
type KeyedTasks struct {
	inspector *asynq.Inspector
}

// NewKeyedTasks reads the tasks of the queues on the lane's Redis. Its
// connections stay open until the process exits, so Domain Event handlers
// still read while the process drains.
func NewKeyedTasks(option Option) *KeyedTasks {
	return &KeyedTasks{inspector: asynq.NewInspector(redisConn(option))}
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
