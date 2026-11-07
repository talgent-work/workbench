package command

import (
	"context"
	"errors"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// RecoverWorkHandler advances retained execution obligations and hands off a
// pending initial task input. It never admits a new start or chooses a fresh
// execution context.
type RecoverWorkHandler struct {
	reach       *ComputerReachHandler
	now         func() time.Time
	sessions    domain.AgentWorkRepository
	buffer      RunningHarnessPort
	inputs      WorkInputs
	settlements integrationmessage.Publisher
}

func NewRecoverWorkHandler(now func() time.Time, sessions domain.AgentWorkRepository, buffer RunningHarnessPort, inputs WorkInputs, settlements integrationmessage.Publisher, reach *ComputerReachHandler) *RecoverWorkHandler {
	return &RecoverWorkHandler{reach: reach, now: now, sessions: sessions, buffer: buffer, inputs: inputs, settlements: settlements}
}

// Handle hands off the Work's pending initial task input, whose commit may
// have outlived its creation's answer, and advances its execution
// obligations; neither failure holds back the other.
func (h *RecoverWorkHandler) Handle(ctx context.Context, sessionID string) error {
	handOff := h.inputs.HandOffInitialTaskInput(ctx, sessionID)
	return errors.Join(handOff, h.recover(ctx, sessionID))
}

func (h *RecoverWorkHandler) recover(ctx context.Context, sessionID string) error {
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
	// Computer Removed this Work missed is applied before anything else. A
	// Computer that cannot be read now holds back none of the other
	// obligations: it is read again on the next recovery, and its failure is
	// returned after them.
	lost, unreadRemoval := h.reach.recoverRemoval(ctx, session)
	if lost && unreadRemoval == nil {
		return nil
	}
	// A Work's Disconnection follows its Computer's Presence or, on a Cloud,
	// Dispatcher's attachment; a Managed Work stopped beyond reach has
	// nothing left to retry on its machine. A Presence that cannot be read
	// now holds back none of the other obligations: it is read again on the
	// next recovery, and its failure is returned after them.
	reached, unread := h.reach.recover(ctx, session)
	if unread != nil {
		reached = false
	}
	if reached {
		queued, err := h.reach.startQueued(ctx, session)
		if err != nil {
			return errors.Join(err, unreadRemoval)
		}
		if !session.ObservesEnvironment(queued) {
			return errors.Join(h.sessions.Save(ctx, session), unreadRemoval)
		}
	}
	// Advance a retained end before considering a new policy stop.
	changed, err := session.RecoverExecution()
	if err != nil {
		return err
	}
	expired, err := h.stopAfterTimeout(ctx, session)
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
	return errors.Join(unread, unreadRemoval)
}

// stopAfterTimeout supplies the buffered-input fact only to a Work that has
// been idle or Waiting long enough, so the sweep reads the buffer rarely.
func (h *RecoverWorkHandler) stopAfterTimeout(ctx context.Context, work *domain.AgentWork) (bool, error) {
	if !work.TimeoutDue(h.now()) {
		return false, nil
	}
	buffered, err := h.buffer.ReadBuffer(ctx, work.ID)
	if err != nil {
		return false, err
	}
	return work.StopAfterTimeout(h.now(), buffered.Work())
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
