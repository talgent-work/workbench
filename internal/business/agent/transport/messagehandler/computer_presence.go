package messagehandler

import (
	"context"
	"errors"
	"fmt"

	integrationmessage "github.com/go-jimu/components/ddd/message"

	computereventsv1 "github.com/talgent-work/workbench/gen/go/talgent/computer/events/v1"
)

// ComputerReach applies what a Managed Computer is now to the Works on it:
// its Presence, or its removal.
type ComputerReach interface {
	PresenceChanged(ctx context.Context, computerID string) error
	ComputerRemoved(ctx context.Context, computerID string) error
}

// ComputerReconnection applies a Managed Computer's return of reach: its
// reconciliation and the Resumes its unreachability refused.
type ComputerReconnection interface {
	Reconnected(ctx context.Context, computerID string) error
}

// ComputerPresenceHandler consumes Computer's Published Facts about a Managed
// Computer's reach: Computer Became Unreachable, Computer Became Reachable
// and Computer Removed. Each fact only names the Computer: the Works on it
// take the Presence or removal Computer holds when the fact is handled, so a
// repeated or reordered fact changes nothing.
type ComputerPresenceHandler struct {
	reach        ComputerReach
	reconnection ComputerReconnection
}

func NewComputerPresenceHandler(reach ComputerReach, reconnection ComputerReconnection) *ComputerPresenceHandler {
	return &ComputerPresenceHandler{reach: reach, reconnection: reconnection}
}

func (h *ComputerPresenceHandler) Listening() []integrationmessage.Kind {
	return []integrationmessage.Kind{
		integrationmessage.KindOf(&computereventsv1.ComputerBecameUnreachable{}),
		integrationmessage.KindOf(&computereventsv1.ComputerBecameReachable{}),
		integrationmessage.KindOf(&computereventsv1.ComputerRemoved{}),
	}
}

// Handle applies the Computer's Presence or removal; a failure is retried
// under the consumer's retry policy, and the Works' recovery applies it in
// any case.
func (h *ComputerPresenceHandler) Handle(ctx context.Context, msg integrationmessage.Message) error {
	switch fact := msg.Payload().(type) {
	case *computereventsv1.ComputerBecameUnreachable:
		return h.reach.PresenceChanged(ctx, fact.GetComputerId())
	case *computereventsv1.ComputerBecameReachable:
		// Both apply what Computer says now; each is idempotent, so a retry
		// repeats both.
		return errors.Join(h.reach.PresenceChanged(ctx, fact.GetComputerId()), h.reconnection.Reconnected(ctx, fact.GetComputerId()))
	case *computereventsv1.ComputerRemoved:
		return h.reach.ComputerRemoved(ctx, fact.GetComputerId())
	default:
		return fmt.Errorf("agent computer presence: unexpected payload %T", msg.Payload())
	}
}
