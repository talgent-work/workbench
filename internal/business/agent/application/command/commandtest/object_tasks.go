package commandtest

import (
	"context"
	"sync"

	"github.com/go-jimu/components/taskqueue"
)

// ObjectTasks stands in for the coalescing of Agent's recovery tasks: it
// records every wake with its object, task and options, and runs each attempt
// at once, never handing a wake over.
type ObjectTasks struct {
	mu sync.Mutex
	// Objects are the objects woken, in order.
	Objects []string
	// Tasks are the tasks woken, in order.
	Tasks []taskqueue.Task
	// Options are each wake's enqueue options, in order.
	Options []taskqueue.EnqueueOptions
}

func (o *ObjectTasks) Wake(_ context.Context, object string, task taskqueue.Task, opts ...taskqueue.EnqueueOption) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Objects = append(o.Objects, object)
	o.Tasks = append(o.Tasks, task)
	o.Options = append(o.Options, taskqueue.NewEnqueueOptions(opts...))
	return nil
}

func (o *ObjectTasks) Run(ctx context.Context, _ string, run, _ func(context.Context) error) error {
	return run(ctx)
}

// Woken returns the objects woken so far.
func (o *ObjectTasks) Woken() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.Objects...)
}
