package taskqueue

import (
	"context"
	"errors"
	"fmt"
	"testing"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
)

// The default queue is a generic fallback for services that have not supplied
// a queue name; semantic task definitions should still set Queue.
func TestDefaultQueueName_Stable(t *testing.T) {
	if DefaultQueueName != "default" {
		t.Errorf("DefaultQueueName drifted: %q", DefaultQueueName)
	}
}

type refusingEnqueuer struct{ err error }

func (r refusingEnqueuer) Enqueue(context.Context, componenttaskqueue.Task, ...componenttaskqueue.EnqueueOption) error {
	return r.err
}

// A unique task already waiting covers a second enqueue, so producers do not
// see the provider's duplicate refusal; any other refusal still reaches them.
func TestDuplicateRefusalCoversTheEnqueue(t *testing.T) {
	task, err := componenttaskqueue.New(componenttaskqueue.Definition{Type: "taskqueue.test.unit", Queue: DefaultQueueName}, []byte(`{}`))
	if err != nil {
		t.Fatalf("new task: %v", err)
	}
	if err := (keyedEnqueuer{next: refusingEnqueuer{err: fmt.Errorf("%w: held", componenttaskqueue.ErrDuplicateTask)}}).Enqueue(context.Background(), task); err != nil {
		t.Fatalf("duplicate refusal surfaced: %v", err)
	}
	broken := errors.New("redis unavailable")
	if err := (keyedEnqueuer{next: refusingEnqueuer{err: broken}}).Enqueue(context.Background(), task); !errors.Is(err, broken) {
		t.Fatalf("other refusal = %v, want %v", err, broken)
	}
}
