package eventhandler

import (
	"context"
	"errors"
	"testing"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"

	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type reclamationQueue struct {
	failures int
	queued   []taskqueue.Task
	options  []taskqueue.EnqueueOptions
}

func (q *reclamationQueue) Enqueue(_ context.Context, task taskqueue.Task, opts ...taskqueue.EnqueueOption) error {
	if q.failures > 0 {
		q.failures--
		return errors.New("queue unavailable")
	}
	q.queued = append(q.queued, task)
	q.options = append(q.options, taskqueue.NewEnqueueOptions(opts...))
	return nil
}

// A Cloud Work reaching Closed queues its one Work Reclamation Request task,
// retried a bounded number of times until Storage accepts the request; a
// Managed Work mounted no
// Directory to reclaim, and other events request nothing.
func TestClosedWorkRequestsReclamation(t *testing.T) {
	queue := &reclamationQueue{}
	handler := NewWorkReclamations(queue)
	require.NoError(t, handler.Request(context.Background(), []domainevent.Event{
		domain.WorkClosingEntered{WorkID: "work-1"},
		domain.WorkClosedEvent{WorkID: "work-1", Cloud: true},
		domain.WorkClosedEvent{WorkID: "work-2"},
	}))
	require.Len(t, queue.queued, 1)
	require.Equal(t, agenttask.RequestWorkReclamationType, queue.queued[0].Type())
	require.Equal(t, "agent:workspace-reclamation:work-1", queue.queued[0].ID())
	retries, set := queue.options[0].MaxRetry()
	require.True(t, set)
	require.Equal(t, agenttask.ReclamationRetries, retries, "a request Storage keeps refusing ends at its ceiling")
	require.Positive(t, queue.options[0].Timeout())
}

// A request the queue does not take is reported, never dropped, so the
// closure carrying it does not commit and is confirmed again later.
func TestAnUnqueuedReclamationRequestIsReported(t *testing.T) {
	queue := &reclamationQueue{failures: 1}
	handler := NewWorkReclamations(queue)
	closed := []domainevent.Event{domain.WorkClosedEvent{WorkID: "work-1", Cloud: true}}
	require.Error(t, handler.Request(context.Background(), closed))
	require.Empty(t, queue.queued)
	require.NoError(t, handler.Request(context.Background(), closed))
	require.Len(t, queue.queued, 1)
}
