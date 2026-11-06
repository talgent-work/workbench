package eventhandler

import (
	"context"
	"log/slog"

	domainevent "github.com/go-jimu/components/ddd/event"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// DeskReleases reports to a Work's Computer that its run's Desk leaves once
// the run no longer holds its room. A lost report is caught by the open-Desk
// reconciliation of the recovery sweep.
type DeskReleases struct {
	computers command.ComputerAdmissionPort
	logger    *slog.Logger
}

var _ domainevent.Handler = (*DeskReleases)(nil)

func NewDeskReleases(computers command.ComputerAdmissionPort, logger *slog.Logger) *DeskReleases {
	return &DeskReleases{computers: computers, logger: logger}
}

func (*DeskReleases) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindDeskReleased}
}

func (h *DeskReleases) Handle(ctx context.Context, event domainevent.Event) {
	released, ok := event.(domain.WorkDeskReleased)
	if !ok {
		return
	}
	if err := h.computers.LeaveDesk(ctx, released.DeskID); err != nil {
		h.logger.WarnContext(ctx, "desk departure left to reconciliation", slog.String("work_id", released.WorkID), slog.String("desk_id", released.DeskID), slog.Any("error", err))
	}
}
