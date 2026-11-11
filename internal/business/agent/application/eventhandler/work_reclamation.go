package eventhandler

import (
	"context"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/go-jimu/components/taskqueue"

	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkReclamations requests reclamation of a Cloud Work's Directories once
// the Work reaches Closed (Request Work reclamation). Agent keeps no
// reclamation obligation: the request is queued as a task that delivers it
// through Storage's contract until Storage accepts it, at most
// ReclamationRetries times, and Storage owns its completion. A request that
// exhausts them ends as a reclamation failure its processor logs.
//
// The request is handed to the task queue before the closure that records
// WorkClosed commits, not after it as an in-memory reaction would: a closure
// whose request could not be queued does not commit, so the Work stays
// Closing, and the Work's recovery, which the recovery sweep runs for every
// unfinished Work, confirms the closure again and repeats the hand-off until
// the request is queued. A request queued for a closure whose save then fails
// is harmless: a Closing Work whose compute is released only ever becomes
// Closed, and Storage accepts a repeated request idempotently.
type WorkReclamations struct {
	enqueuer taskqueue.Enqueuer
}

func NewWorkReclamations(enqueuer taskqueue.Enqueuer) *WorkReclamations {
	return &WorkReclamations{enqueuer: enqueuer}
}

// Request queues the Work Reclamation Request of each Cloud Work's closure
// among events, the pending events of a Work about to be saved. An error
// means a request was not queued, and the change carrying it must not
// commit.
func (h *WorkReclamations) Request(ctx context.Context, events []domainevent.Event) error {
	for _, event := range events {
		closed, ok := event.(domain.WorkClosedEvent)
		if !ok || !closed.Cloud {
			continue
		}
		task, err := agenttask.NewRequestWorkReclamation(closed.WorkID)
		if err != nil {
			return err
		}
		// The task is retried, up to its ceiling, until Storage accepts the
		// request; its key collapses a repeated hand-off into the task
		// already queued.
		if err := h.enqueuer.Enqueue(ctx, task, agenttask.Reclamation()...); err != nil {
			return err
		}
	}
	return nil
}
