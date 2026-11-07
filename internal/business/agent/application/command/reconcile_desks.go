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
	now        func() time.Time
	computers  ComputerPort
	works      domain.AgentWorkRepository
	executions domain.AgentExecutionRepository
}

func NewReconcileDesksHandler(now func() time.Time, computers ComputerPort, works domain.AgentWorkRepository, executions domain.AgentExecutionRepository) *ReconcileDesksHandler {
	return &ReconcileDesksHandler{now: now, computers: computers, works: works, executions: executions}
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
	held, err := h.held(ctx, desk)
	if err != nil || held {
		return err
	}
	return h.computers.LeaveDesk(ctx, desk.DeskID)
}

// held reports whether a run still holds the Desk: the Work's execution
// that has not left it, or the Work's committed start whose execution has
// not begun yet.
func (h *ReconcileDesksHandler) held(ctx context.Context, desk OpenDesk) (bool, error) {
	execution, err := h.executions.Latest(ctx, desk.WorkID)
	switch {
	case err == nil && execution.HoldsDesk(desk.DeskID):
		return true, nil
	case err != nil && !errors.Is(err, domain.ErrExecutionNotFound):
		return false, err
	}
	work, err := h.works.Get(ctx, desk.WorkID)
	if errors.Is(err, domain.ErrWorkNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	admitted, ok := requestOf[domain.WorkStartAdmitted](work)
	if !ok || admitted.DeskID != desk.DeskID {
		return false, nil
	}
	return execution == nil || !execution.Serves(admitted.StartAdmittedAt), nil
}
