package eventhandler

import (
	"context"
	"log/slog"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkspaceReclamationQueue accepts a Closed Work's Workspace Reclamation
// Request for durable delivery to Storage.
type WorkspaceReclamationQueue interface {
	RequestWorkspaceReclamation(ctx context.Context, workID, workspaceReference string) error
}

// reclamationAttempts bounds how often handing the request over is retried
// while the delivery queue is briefly unreachable.
const reclamationAttempts = 3

// WorkspaceReclamations requests reclamation of a Work's Session Workspace
// once the Work reaches Closed. Agent keeps no reclamation obligation: the
// request is handed to a delivery queue here and Storage owns its completion.
type WorkspaceReclamations struct {
	requests WorkspaceReclamationQueue
	logger   *slog.Logger
	backoff  time.Duration
}

var _ domainevent.Handler = (*WorkspaceReclamations)(nil)

func NewWorkspaceReclamations(requests WorkspaceReclamationQueue, logger *slog.Logger) *WorkspaceReclamations {
	return &WorkspaceReclamations{requests: requests, logger: logger, backoff: time.Second}
}

func (*WorkspaceReclamations) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindClosed}
}

func (h *WorkspaceReclamations) Handle(ctx context.Context, event domainevent.Event) {
	closed, ok := event.(domain.WorkClosedEvent)
	if !ok || closed.WorkspaceReference == "" {
		return
	}
	var err error
	for attempt := 1; attempt <= reclamationAttempts; attempt++ {
		if err = h.requests.RequestWorkspaceReclamation(ctx, closed.WorkID, closed.WorkspaceReference); err == nil {
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
