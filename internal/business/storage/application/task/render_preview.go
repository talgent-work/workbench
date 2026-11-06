// Package task defines Storage's internal task contracts.
package task

import (
	"context"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"google.golang.org/protobuf/proto"

	storagetaskv1 "github.com/talgent-work/workbench/gen/go/talgent/storage/task/v1"
)

// Queue is the one lane Storage renders Preview Copies on.
const Queue = "storage-preview"

const RenderPreviewType taskqueue.TaskType = "storage.preview.render.v1"

// RenderPreview names one content to render, as found at one File position.
type RenderPreview struct {
	Owner, Kind, Name, Digest string
}

// RenderingPolicy bounds one rendering task.
type RenderingPolicy struct {
	// Attempt bounds one attempt, the converter's own limit plus its client's
	// margin.
	Attempt time.Duration
	// Deadline bounds the task from its request, busy retries included.
	Deadline time.Duration
}

// busyRetries bounds retries while the converter is busy; the task's
// deadline is what actually ends them.
const busyRetries = 100

// NewRenderPreview builds the task, keyed by the content digest so a request
// for content already queued or rendering is absorbed by that task.
func NewRenderPreview(render RenderPreview) (taskqueue.Task, error) {
	payload, err := proto.Marshal(&storagetaskv1.RenderPreviewTask{
		Owner: render.Owner, Kind: render.Kind, Name: render.Name, Digest: render.Digest,
	})
	if err != nil {
		return taskqueue.Task{}, err
	}
	return taskqueue.New(taskqueue.Definition{Type: RenderPreviewType, Queue: Queue}, payload, taskqueue.WithID(render.Digest))
}

// RequestRendering enqueues the rendering. A busy converter retries it after
// the queue's retry delay without it counting as a failure; the deadline from
// now ends every attempt.
func RequestRendering(ctx context.Context, enqueuer taskqueue.Enqueuer, render RenderPreview, policy RenderingPolicy, now time.Time) error {
	queued, err := NewRenderPreview(render)
	if err != nil {
		return err
	}
	options := []taskqueue.EnqueueOption{
		taskqueue.WithTimeout(policy.Attempt), taskqueue.WithDeadline(now.Add(policy.Deadline)), taskqueue.WithMaxRetry(busyRetries),
	}
	if err := taskqueue.NewEnqueueOptions(options...).Validate(); err != nil {
		return err
	}
	return enqueuer.Enqueue(ctx, queued, options...)
}
