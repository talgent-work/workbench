package command

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// inputChangeNoticeSpace derives a notice's input identity from its change
// and Work, so a redelivered change hands Dispatcher the identity it already
// holds (Repeated input).
var inputChangeNoticeSpace = uuid.MustParse("0193a7c2-6f1e-7c2a-9b3d-6e6f74696365")

// DeliverInputChangeNoticeHandler delivers a Work the notice of one change in
// its located inputs (Notify changed inputs). Agent itself is the requester,
// so no Agent Owner is authorized here: the Work admits the notice itself.
type DeliverInputChangeNoticeHandler struct {
	sessions domain.AgentWorkRepository
	inputs   WorkInputs
}

func NewDeliverInputChangeNoticeHandler(sessions domain.AgentWorkRepository, inputs WorkInputs) *DeliverInputChangeNoticeHandler {
	return &DeliverInputChangeNoticeHandler{sessions: sessions, inputs: inputs}
}

// Deliver hands the admitted notice to the Work's input buffer like any
// other input: Steer during a Turn, a new Turn when idle, buffered while
// stopped, and never a start. A Work that admits no notice, or that closes
// before the hand-off, has nothing to deliver.
func (h *DeliverInputChangeNoticeHandler) Deliver(ctx context.Context, workID string, change domain.InputChange) error {
	work, err := h.sessions.Get(ctx, workID)
	if err != nil {
		return err
	}
	notice, err := work.AdmitInputChangeNotice(change)
	if err != nil {
		return refusedNotice(err)
	}
	input := ExecutionInput{ID: uuid.NewSHA1(inputChangeNoticeSpace, []byte(workID+"\x00"+notice.Change)).String(), Text: notice.Text()}
	_, err = h.inputs.AcceptInput(ctx, workID, input)
	return refusedNotice(err)
}

// refusedNotice ends a delivery the Work refuses for good; any other failure
// is returned for the delivery to be repeated.
func refusedNotice(err error) error {
	if errors.Is(err, domain.ErrInputChangeNoticeNotAdmitted) || errors.Is(err, domain.ErrWorkTransition) {
		return nil
	}
	return err
}
