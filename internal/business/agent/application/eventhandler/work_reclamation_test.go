package eventhandler

import (
	"context"
	"errors"
	"log/slog"
	"testing"

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

// A Cloud Work reaching Closed queues its one Work Reclamation Request task
// once, riding out a brief queue outage; the task is retried until Storage
// accepts the request, and Agent keeps nothing to follow up.
func TestClosedWorkRequestsReclamationOnce(t *testing.T) {
	queue := &reclamationQueue{failures: 1}
	handler := NewWorkReclamations(queue, slog.Default())
	handler.backoff = 0
	handler.Handle(context.Background(), domain.WorkClosedEvent{WorkID: "work-1", Cloud: true})
	require.Len(t, queue.queued, 1)
	require.Equal(t, agenttask.RequestWorkReclamationType, queue.queued[0].Type())
	require.Equal(t, "agent:workspace-reclamation:work-1", queue.queued[0].ID())
	retries, set := queue.options[0].MaxRetry()
	require.True(t, set)
	require.Greater(t, retries, 1_000_000, "a queued request is delivered however long Storage is away")
	require.Positive(t, queue.options[0].Timeout())

	handler.Handle(context.Background(), domain.WorkClosedEvent{WorkID: "work-2"})
	require.Len(t, queue.queued, 1, "a Managed Work mounted no Work Directory to reclaim")
}
