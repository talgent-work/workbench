package agent

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type unavailableViews struct{}

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
