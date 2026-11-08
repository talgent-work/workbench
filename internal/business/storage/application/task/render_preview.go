// Package task defines Storage's internal task contracts.
package task

import (
	"context"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"google.golang.org/protobuf/proto"

	storagetaskv1 "github.com/talgent-work/workbench/gen/storage/task/v1"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Queue is the lane Storage renders Preview Copies on.
const Queue = "storage-preview"

const RenderPreviewType taskqueue.TaskType = "storage.preview.render.v1"

// RenderPreview names one content to render, as found at one File position.
type RenderPreview struct {
	Directory domain.DirectoryID
	Name      string
	Digest    string
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
	payload, err := proto.Marshal(&storagetaskv1.RenderPreviewTaskV1{
		Directory: directoryMessage(render.Directory), Name: render.Name, Digest: render.Digest,
	})
	if err != nil {
		return taskqueue.Task{}, err
	}
	return taskqueue.New(taskqueue.Definition{Type: RenderPreviewType, Queue: Queue}, payload, taskqueue.WithID(render.Digest))
}

// ParseRenderPreview reads a queued rendering.
func ParseRenderPreview(payload []byte) (RenderPreview, error) {
	message := &storagetaskv1.RenderPreviewTaskV1{}
	if err := proto.Unmarshal(payload, message); err != nil {
		return RenderPreview{}, err
	}
	directory, err := directoryID(message.GetDirectory())
	if err != nil {
		return RenderPreview{}, err
	}
	return RenderPreview{Directory: directory, Name: message.GetName(), Digest: message.GetDigest()}, nil
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

func directoryMessage(id domain.DirectoryID) *storagetaskv1.Directory {
	message := &storagetaskv1.Directory{Name: id.Name}
	for _, fact := range id.Owner.Facts() {
		message.Owner = append(message.Owner, &storagetaskv1.OwnerFact{Condition: fact.Condition, Value: fact.Value})
	}
	return message
}

func directoryID(message *storagetaskv1.Directory) (domain.DirectoryID, error) {
	facts := make([]domain.OwnerFact, 0, len(message.GetOwner()))
	for _, fact := range message.GetOwner() {
		facts = append(facts, domain.OwnerFact{Condition: fact.GetCondition(), Value: fact.GetValue()})
	}
	owner, err := domain.NewDirectoryOwner(facts)
	if err != nil {
		return domain.DirectoryID{}, err
	}
	return domain.NewDirectoryID(owner, message.GetName())
}
