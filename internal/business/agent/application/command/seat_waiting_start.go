package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// SeatWaitingStartHandler begins a start that waited in its Computer's line
// once its Desk is seated: from Computer's Desk Assigned, or from the Work's
// recovery asking Computer for its waiting Desk.
type SeatWaitingStartHandler struct {
	now       func() time.Time
	sessions  domain.AgentWorkRepository
	computers ComputerPort
	execution domain.ExecutionPort
}

func NewSeatWaitingStartHandler(now func() time.Time, sessions domain.AgentWorkRepository, computers ComputerPort, execution domain.ExecutionPort) *SeatWaitingStartHandler {
	return &SeatWaitingStartHandler{now: now, sessions: sessions, computers: computers, execution: execution}
}

// ErrDeskNotHeld reports a seated Desk the Work does not hold as it is now
// visible: the Work, or its start that took the Desk, may not be committed
// yet, or the fact may name a Desk of an earlier start.
var ErrDeskNotHeld = errors.New("agent: the Work does not hold the seated Desk")

// DeskAssigned begins the Work's waiting start when the seated Desk is the
// one it waits on, its Startup Deadline counting from seatedAt, when its
// Computer seated it. A Work that does not hold the Desk is ErrDeskNotHeld;
// a repeated fact, or one for a start already withdrawn, changes nothing; a
// concurrent change of the Work fails the commit, and the retry sees the
// Work as it is now.
func (h *SeatWaitingStartHandler) DeskAssigned(ctx context.Context, workID, deskID string, seatedAt time.Time) error {
	work, err := h.sessions.Get(ctx, workID)
	if errors.Is(err, domain.ErrWorkNotFound) {
		return fmt.Errorf("%w: %w", ErrDeskNotHeld, err)
	}
	if err != nil {
		return err
	}
	if work.HarnessProcess == nil || work.HarnessProcess.DeskID != deskID {
		return ErrDeskNotHeld
	}
	return h.begin(ctx, work, seatedAt)
}

// recover asks Computer whether the Desk a waiting start holds is seated, and
// begins the start when it is.
func (h *SeatWaitingStartHandler) recover(ctx context.Context, work *domain.AgentWork) error {
	seatedAt, seated, err := h.computers.DeskSeated(ctx, work.HarnessProcess.DeskID)
	if err != nil || !seated {
		return err
	}
	return h.begin(ctx, work, seatedAt)
}

// begin starts the waiting start seated at seatedAt; a seating whose time
// Computer did not supply counts from now.
func (h *SeatWaitingStartHandler) begin(ctx context.Context, work *domain.AgentWork, seatedAt time.Time) error {
	if !work.WaitsForDesk() {
		return nil
	}
	if seatedAt.IsZero() {
		seatedAt = h.now()
	}
	requests := deferExecution(h.execution)
	if _, err := work.BeginSeatedStart(ctx, seatedAt, requests); err != nil {
		return err
	}
	if err := h.sessions.Save(ctx, work); err != nil {
		return err
	}
	return requests.send(ctx)
}
