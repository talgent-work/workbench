package agent

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type unavailableViews struct{ query.WorkQueryRepository }

func (unavailableViews) GetWorkView(context.Context, string) (query.WorkView, error) {
	return query.WorkView{}, errors.New("view unavailable")
}

type recordedEvents struct{ dispatched []domainevent.Event }

func (r *recordedEvents) Dispatch(ctx context.Context, e domainevent.Event) error {
	return r.DispatchAll(ctx, []domainevent.Event{e})
}
func (r *recordedEvents) DispatchAll(_ context.Context, events []domainevent.Event) error {
	r.dispatched = append(r.dispatched, events...)
	return nil
}
func (r *recordedEvents) Close(context.Context) error { return nil }

// A committed Work change dispatches the events the Work recorded, once.
func TestCommittedWorkDispatchesItsEventsOnce(t *testing.T) {
	events := &recordedEvents{}
	works := &committedWorks{workStateUpdates: &workStateUpdates{reader: unavailableViews{}, logger: slog.New(slog.DiscardHandler)}, events: events}
	work := &domain.AgentWork{ID: "work", Events: domain.NewEventCollection()}
	work.Events.Add(domain.WorkExecutionEnded{WorkID: "work"})
	works.committed(context.Background(), work)
	require.Equal(t, []domainevent.Event{domain.WorkExecutionEnded{WorkID: "work"}}, events.dispatched)
	works.committed(context.Background(), work)
	require.Len(t, events.dispatched, 1, "drained events are not dispatched again")
}

type flakyQueue struct {
	down   bool
	queued []string
}

func (q *flakyQueue) Enqueue(_ context.Context, task taskqueue.Task, _ ...taskqueue.EnqueueOption) error {
	if q.down {
		return errors.New("queue unavailable")
	}
	q.queued = append(q.queued, task.ID())
	return nil
}

// A closure's Work Reclamation Request is queued before the closure commits:
// a request the queue does not take fails the save, so the closure is
// confirmed again later, and the Work keeps its pending events for their
// dispatch once a save commits.
func TestAClosureCommitsOnlyOnceItsReclamationRequestIsQueued(t *testing.T) {
	queue := &flakyQueue{down: true}
	works := &committedWorks{reclamations: eventhandler.NewWorkReclamations(queue)}
	work := &domain.AgentWork{ID: "work", Events: domain.NewEventCollection()}
	work.Events.Add(domain.WorkClosedEvent{WorkID: "work", Cloud: true})
	require.Error(t, works.requestReclamation(context.Background(), work))
	require.Equal(t, 1, work.Events.Len(), "the closure keeps its event")

	queue.down = false
	require.NoError(t, works.requestReclamation(context.Background(), work))
	require.Equal(t, []string{"agent:workspace-reclamation:work"}, queue.queued)
	require.Equal(t, []domainevent.Event{domain.WorkClosedEvent{WorkID: "work", Cloud: true}}, work.Events.Drain(), "the events stay for their dispatch after the commit")
}
