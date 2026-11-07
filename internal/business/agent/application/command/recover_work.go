package command

import (
	"context"
	"errors"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// RecoverWorkHandler advances retained execution obligations. It never admits
// a new start or chooses a fresh execution context.
type RecoverWorkHandler struct {
	seating     *SeatWaitingStartHandler
	reach       *ComputerReachHandler
	now         func() time.Time
	sessions    domain.AgentWorkRepository
	execution   domain.ExecutionPort
	buffer      RunningHarnessPort
	settlements integrationmessage.Publisher
}

func NewRecoverWorkHandler(now func() time.Time, sessions domain.AgentWorkRepository, execution domain.ExecutionPort, buffer RunningHarnessPort, settlements integrationmessage.Publisher, seating *SeatWaitingStartHandler, reach *ComputerReachHandler) *RecoverWorkHandler {
	return &RecoverWorkHandler{seating: seating, reach: reach, now: now, sessions: sessions, execution: execution, buffer: buffer, settlements: settlements}
}

func (h *RecoverWorkHandler) Handle(ctx context.Context, sessionID string) error {
	session, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.Closed() {
		// Entering Closing empties the input buffer; a drop the event handler
		// could not make is repeated here.
		if err := h.dropBuffer(ctx, session.ID); err != nil {
			return err
		}
	}
	// A removed Managed Computer took the Work's environment with it; a
	// Computer Removed this Work missed is applied before anything is retried
	// on the gone machine. A Computer that cannot be read now holds back
	// none of the other obligations: it is read again on the next recovery,
	// and its failure is returned after them.
	lost, unreadRemoval := h.reach.recoverRemoval(ctx, session)
	if lost && unreadRemoval == nil {
		return h.sessions.Save(ctx, session)
	}
	// A start waiting in its Computer's line has nothing to advance until its
	// Desk is seated; the Desk Assigned it may have missed is asked for.
	if session.WaitsForDesk() {
		return errors.Join(h.seating.recover(ctx, session), unreadRemoval)
	}
	requests := deferExecution(h.execution)
	// A start past its Startup Deadline fails before anything else is retried
	// for it; its end obligation is all that remains.
	failed, err := session.FailAtStartupDeadline(ctx, h.now(), requests)
	if err != nil {
		return err
	}
	if failed {
		if err := h.sessions.Save(ctx, session); err != nil {
			return err
		}
		return errors.Join(requests.send(ctx), unreadRemoval)
	}
	// A Work's Disconnection follows its Computer's Presence or, on a Cloud,
	// Dispatcher's attachment; a Managed Work stopped beyond reach has
	// nothing left to retry on its machine. A Presence that cannot be read
	// now holds back none of the other obligations: it is read again on the
	// next recovery, and its failure is returned after them.
	reached, unread := h.reach.recover(ctx, session, requests)
	if unread != nil {
		reached = false
	}
	if reached && !session.ObservesEnvironment() {
		if err := h.sessions.Save(ctx, session); err != nil {
			return err
		}
		return errors.Join(requests.send(ctx), unreadRemoval)
	}
	// Advance a retained control request before considering a new policy stop.
	changed, err := session.RecoverExecution(ctx, requests)
	if err != nil {
		return err
	}
	expired, err := h.stopAfterTimeout(ctx, session, requests)
	if err != nil {
		return err
	}
	idle, err := h.reconcileIdleness(ctx, session)
	if err != nil {
		return err
	}
	if reached || changed || expired || idle {
		if err := h.sessions.Save(ctx, session); err != nil {
			return err
		}
	}
	return errors.Join(requests.send(ctx), unread, unreadRemoval)
}

// stopAfterTimeout supplies the buffered-input fact only to a Work that has
// been idle or Waiting long enough, so the sweep reads the buffer rarely.
func (h *RecoverWorkHandler) stopAfterTimeout(ctx context.Context, work *domain.AgentWork, requests *deferredExecution) (bool, error) {
	if !work.TimeoutDue(h.now()) {
		return false, nil
	}
	buffered, err := h.buffer.ReadBuffer(ctx, work.ID)
	if err != nil {
		return false, err
	}
	return work.StopAfterTimeout(ctx, h.now(), buffered.Work(), requests)
}

// reconcileIdleness restores idleness that a Turn activity report ended after
// the Turn itself had ended: a Working Work with no active Turn that is not
// idle is idle again once nothing awaits processing. Only such a
// Work's buffer is read.
func (h *RecoverWorkHandler) reconcileIdleness(ctx context.Context, work *domain.AgentWork) (bool, error) {
	if work.State != domain.WorkWorking || work.EndRequestReference != "" || work.HarnessProcess.Turn.Active || work.Idle() {
		return false, nil
	}
	buffered, err := h.buffer.ReadBuffer(ctx, work.ID)
	if err != nil {
		return false, err
	}
	if err := work.RecordIdleness(buffered.Pending(), h.now()); err != nil {
		return false, err
	}
	return work.Idle(), nil
}

// dropBuffer reports each input a closing Work's buffer holds as dropped and
// then empties it: a failed report keeps the buffer, so the retry reports
// again, and a drop already made finds nothing more to report.
func (h *RecoverWorkHandler) dropBuffer(ctx context.Context, workID string) error {
	buffered, err := h.buffer.ReadBuffer(ctx, workID)
	if err != nil {
		return err
	}
	for _, inputID := range buffered.InputIDs {
		if err := publishInputSettlement(ctx, h.settlements, InputSettlement{WorkID: workID, InputID: inputID, Outcome: InputDropped, At: h.now()}); err != nil {
			return err
		}
	}
	return h.buffer.DropBuffer(ctx, workID)
}
