package application

import (
	"context"
	"log/slog"

	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// InputsChanged follows Storage's fact that a file in a Directory changed: it
// queues one notice task for each unclosed Work whose located inputs hold the
// changed Directory (Notify changed inputs). Each Work admits or refuses its
// notice when the task runs, so a Work with a File Home is listed yet told
// nothing. A task is keyed by the Work and the change, so a redelivered change
// queues nothing twice.
func (r *Recovery) InputsChanged(ctx context.Context, change domain.InputChange) error {
	if !change.Directory.Present() {
		return domain.ErrInvalidWork
	}
	list := func(ctx context.Context, after string, limit int) ([]string, error) {
		return r.app.WorkQueries.ListUnclosedWorksReadingInputs(ctx, change.Directory, after, limit)
	}
	notify := func(ctx context.Context, workID string) error {
		task, err := agenttask.NewDeliverInputChangeNotice(workID, change)
		if err == nil {
			err = r.enqueuer.Enqueue(ctx, task, agenttask.InputChangeNotice()...)
		}
		if err != nil && r.app.Logger != nil {
			// The fact is handled again a bounded number of times; this
			// names the Work whose notice it has not queued yet.
			r.app.Logger.WarnContext(ctx, "input change notice not queued", slog.String("work_id", workID), slog.String("change_id", change.ID), slog.Any("error", err))
		}
		return err
	}
	return sweep(ctx, list, notify, identity)
}

// DeliverInputChangeNotice delivers one Work the notice of one change, as
// its queued task asks.
func (r *Recovery) DeliverInputChangeNotice(ctx context.Context, workID string, change domain.InputChange) error {
	return r.app.Commands.DeliverInputChangeNotice.Deliver(ctx, workID, change)
}
