package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// deskGrace leaves a just-admitted run time to commit its start before an
// open Desk no Work holds counts as one that can no longer start. A start
// commits within its request, which the gateway and every caller bound far
// below this; a request still in flight after it has been abandoned.
const deskGrace = 10 * time.Minute

// ReconcileDesksHandler reports the departure of every open Desk no Work's
// run holds: a run whose start never committed, or whose release report was
// lost. It is Agent confirming such a run can no longer start.
type ReconcileDesksHandler struct {
	now       func() time.Time
	computers ComputerAdmissionPort
	works     domain.AgentWorkRepository
}

func NewReconcileDesksHandler(now func() time.Time, computers ComputerAdmissionPort, works domain.AgentWorkRepository) *ReconcileDesksHandler {
	return &ReconcileDesksHandler{now: now, computers: computers, works: works}
}

func (h *ReconcileDesksHandler) Handle(ctx context.Context) error {
	before := h.now().Add(-deskGrace)
	var failures []error
	after := ""
	for {
		desks, err := h.computers.ListOpenDesks(ctx, before, after)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if len(desks) == 0 {
			return errors.Join(failures...)
		}
		for _, desk := range desks {
			after = desk.DeskID
			if err := h.reconcile(ctx, desk); err != nil {
				failures = append(failures, err)
			}
		}
	}
}

func (h *ReconcileDesksHandler) reconcile(ctx context.Context, desk OpenDesk) error {
	work, err := h.works.Get(ctx, desk.WorkID)
	if err != nil && !errors.Is(err, domain.ErrWorkNotFound) {
		return err
	}
	if work != nil && work.HoldsDesk(desk.DeskID) {
		return nil
	}
	return h.computers.LeaveDesk(ctx, desk.DeskID)
}
