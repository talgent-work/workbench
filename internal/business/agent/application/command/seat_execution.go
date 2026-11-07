package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// SeatExecutionHandler seats a Queued execution once Computer seated its Desk
// (Seat execution). The execution's reconciliation recovers a Desk Assigned
// this misses by asking Computer.
type SeatExecutionHandler struct {
	now        func() time.Time
	executions domain.AgentExecutionRepository
}

func NewSeatExecutionHandler(now func() time.Time, executions domain.AgentExecutionRepository) *SeatExecutionHandler {
	return &SeatExecutionHandler{now: now, executions: executions}
}

// ErrDeskNotHeld reports a seated Desk no execution of the Work holds as it
// is now visible: the start that took the Desk, or its execution, may not be
// committed yet, or the fact may name a Desk of an earlier start.
var ErrDeskNotHeld = errors.New("agent: the Work does not hold the seated Desk")

// DeskAssigned seats the Work's execution holding the Desk, its Startup
// Deadline counting from seatedAt, when its Computer seated it; one Computer
// did not time counts from now. A repeated fact changes nothing; a concurrent
// change of the execution fails the commit, and the retry sees it as it is
// now.
func (h *SeatExecutionHandler) DeskAssigned(ctx context.Context, workID, deskID string, seatedAt time.Time) error {
	execution, err := h.executions.Latest(ctx, workID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return errors.Join(ErrDeskNotHeld, err)
	}
	if err != nil {
		return err
	}
	if !execution.HoldsDesk(deskID) {
		return ErrDeskNotHeld
	}
	if seatedAt.IsZero() {
		seatedAt = h.now()
	}
	seated, err := execution.Seat(seatedAt)
	if err != nil || !seated {
		return err
	}
	return h.executions.Save(ctx, execution)
}
