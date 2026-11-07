package messagehandler

import (
	"context"
	"errors"
	"fmt"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"

	computerintegrationv1 "github.com/talgent-work/contracts/gen/go/computer/integration/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// DeskAssignments begins a Work's waiting start once its Desk is seated.
type DeskAssignments interface {
	DeskAssigned(ctx context.Context, workID, deskID string, seatedAt time.Time) error
}

// DeskAssignedHandler consumes Computer's Desk Assigned Published Fact.
type DeskAssignedHandler struct {
	assignments DeskAssignments
}

func NewDeskAssignedHandler(assignments DeskAssignments) *DeskAssignedHandler {
	return &DeskAssignedHandler{assignments: assignments}
}

func (h *DeskAssignedHandler) Listening() []integrationmessage.Kind {
	return []integrationmessage.Kind{integrationmessage.KindOf(&computerintegrationv1.DeskAssignedV1{})}
}

// concurrentAttempts bounds retries of a seating that lost a concurrent
// change of its Work, such as a stop withdrawing the start.
const concurrentAttempts = 3

// Handle begins the start seated when Computer seated it. A fact for a Desk
// the Work does not hold yet, such as one that overtook the commit of the
// start that took it, fails with command.ErrDeskNotHeld and is handled again
// under the consumer's retry policy; once its attempts run out the Work's
// recovery still finds the seated Desk.
func (h *DeskAssignedHandler) Handle(ctx context.Context, msg integrationmessage.Message) error {
	fact, ok := msg.Payload().(*computerintegrationv1.DeskAssignedV1)
	if !ok || fact == nil {
		return fmt.Errorf("agent desk assigned: unexpected payload %T", msg.Payload())
	}
	var seatedAt time.Time
	if at := fact.GetOccurredAt(); at != nil {
		seatedAt = at.AsTime()
	}
	var err error
	for range concurrentAttempts {
		err = h.assignments.DeskAssigned(ctx, fact.GetWorkId(), fact.GetDeskId(), seatedAt)
		if !errors.Is(err, domain.ErrConcurrentModification) {
			break
		}
	}
	return err
}
