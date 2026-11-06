package infrastructure

import (
	"context"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// TaskStates reads whether the task holding a key still waits or runs in a
// queue.
type TaskStates interface {
	Pending(queue, key string) (bool, error)
}

// RenderingTasks tells a content's rendering by its task, which is keyed by
// the content digest on Storage's preview lane, the queue the task names.
type RenderingTasks struct {
	Tasks TaskStates
	Queue string
}

var _ application.RenderingTasksPort = RenderingTasks{}

func (r RenderingTasks) Rendering(ctx context.Context, key domain.PreviewKey) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	pending, err := r.Tasks.Pending(r.Queue, key.Digest)
	return pending, oops.With("operation", "storage.rendering_state").Wrap(err)
}
