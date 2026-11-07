package messagepublisher

import (
	"context"
	"log/slog"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	computerintegrationv1 "github.com/talgent-work/contracts/gen/computer/integration/v1"
	"github.com/talgent-work/workbench/internal/business/computer/application"
)

// Presences publishes each committed Presence change of a Managed Computer,
// Computer Became Unreachable or Computer Became Reachable, best effort and
// keyed by the Computer. Agent records its Works' Disconnection from them
// and also reads each Computer's Presence while its Works run, so a lost or
// reordered fact only delays the record. Publications run for the
// application's lifetime: Stop ends them.
type Presences struct {
	publications *publications
}

var _ application.Presences = (*Presences)(nil)

func NewPresences(bus integrationmessage.Publisher, logger *slog.Logger) *Presences {
	return &Presences{publications: newPublications(bus, logger, "computer.presence")}
}

func (p *Presences) Publish(parent context.Context, changes []application.PresenceChange) {
	if p == nil {
		return
	}
	facts := make([]fact, 0, len(changes))
	for _, change := range changes {
		payload, eventID := presenceChanged(change)
		facts = append(facts, fact{payload: payload, key: change.ComputerID, attrs: []any{
			slog.String("computer_id", change.ComputerID), slog.Bool("reachable", change.Reachable), slog.String("event_id", eventID),
		}})
	}
	p.publications.publish(parent, "presence change", facts)
}

// Stop starts no further publication and waits for those under way until
// ctx ends, then cancels them.
func (p *Presences) Stop(ctx context.Context) error { return p.publications.stop(ctx) }

// presenceChanged maps a committed Presence change to its published fact.
func presenceChanged(change application.PresenceChange) (proto.Message, string) {
	id := uuid.Must(uuid.NewV7()).String()
	if change.Reachable {
		return &computerintegrationv1.ComputerBecameReachableV1{EventId: id, OccurredAt: timestamppb.New(change.At), ComputerId: change.ComputerID}, id
	}
	return &computerintegrationv1.ComputerBecameUnreachableV1{
		EventId: id, OccurredAt: timestamppb.New(change.At), ComputerId: change.ComputerID, Since: timestamppb.New(change.Since),
	}, id
}
