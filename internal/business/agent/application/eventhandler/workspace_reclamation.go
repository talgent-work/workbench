package eventhandler

import (
	"context"
	"log/slog"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/go-jimu/components/taskqueue"

	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// reclamationAttempts bounds how often handing the request over is retried
// while the delivery queue is briefly unreachable.
const reclamationAttempts = 3

// WorkspaceReclamations requests reclamation of a Work's Session Workspace
// once the Work reaches Closed. Agent keeps no reclamation obligation: the
// request is queued here as a task that delivers it through Storage's
// contract, and Storage owns its completion.
type WorkspaceReclamations struct {
	enqueuer taskqueue.Enqueuer
	logger   *slog.Logger
	backoff  time.Duration
}

var _ domainevent.Handler = (*WorkspaceReclamations)(nil)

func NewWorkspaceReclamations(enqueuer taskqueue.Enqueuer, logger *slog.Logger) *WorkspaceReclamations {
	return &WorkspaceReclamations{enqueuer: enqueuer, logger: logger, backoff: time.Second}
}

func (*WorkspaceReclamations) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindClosed}
}

func (h *WorkspaceReclamations) Handle(ctx context.Context, event domainevent.Event) {
	closed, ok := event.(domain.WorkClosedEvent)
	if !ok || closed.WorkspaceReference == "" {
		return
	}
	task, err := agenttask.NewRequestWorkspaceReclamation(closed.WorkID, closed.WorkspaceReference)
	if err != nil {
		h.logger.WarnContext(ctx, "workspace reclamation request not queued", "work_id", closed.WorkID, "error", err)
		return
	}
	for attempt := 1; attempt <= reclamationAttempts; attempt++ {
		// The request reaches Storage across restarts and outages: its
		// delivery is retried until Storage accepts it.
		if err = h.enqueuer.Enqueue(ctx, task, agenttask.Persistent(agenttask.RecoveryAttemptTimeout)...); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			attempt = reclamationAttempts
		case <-time.After(h.backoff * time.Duration(attempt)):
		}
	}
	// The Work stays Closed; an unqueued request leaves only its working
	// files, an accepted leak.
	h.logger.WarnContext(ctx, "workspace reclamation request not queued", "work_id", closed.WorkID, "error", err)
}
