package messagepublisher

import (
	"context"
	"log/slog"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	computerintegrationv1 "github.com/talgent-work/contracts/gen/go/computer/integration/v1"
	"github.com/talgent-work/workbench/internal/business/computer/application"
)

// Seatings publishes the Desk Assigned Published Fact for each seated waiting
// Desk, best effort and keyed by its Work. Publishing never delays or fails
// the seating: it runs after the commit, and Agent also asks for the Desks
// its waiting starts hold, so a lost fact only delays a start. Publications
// run for the application's lifetime: Stop ends them.
type Seatings struct {
	publications *publications
}

var _ application.Seatings = (*Seatings)(nil)

func NewSeatings(bus integrationmessage.Publisher, logger *slog.Logger) *Seatings {
	return &Seatings{publications: newPublications(bus, logger, "computer.seatings")}
}

func (p *Seatings) Publish(parent context.Context, seated []application.SeatedDesk) {
	if p == nil {
		return
	}
	facts := make([]fact, 0, len(seated))
	for _, desk := range seated {
		payload := deskAssigned(desk)
		facts = append(facts, fact{payload: payload, key: desk.WorkID, attrs: []any{
			slog.String("desk_id", desk.DeskID), slog.String("work_id", desk.WorkID), slog.String("event_id", payload.GetEventId()),
		}})
	}
	p.publications.publish(parent, "desk assigned", facts)
}

// Stop starts no further publication and waits for those under way until
// ctx ends, then cancels them.
func (p *Seatings) Stop(ctx context.Context) error { return p.publications.stop(ctx) }

// deskAssigned maps a committed seating to its published fact.
func deskAssigned(desk application.SeatedDesk) *computerintegrationv1.DeskAssignedV1 {
	return &computerintegrationv1.DeskAssignedV1{
		EventId: uuid.Must(uuid.NewV7()).String(), OccurredAt: timestamppb.New(desk.SeatedAt),
		ComputerId: desk.ComputerID, DeskId: desk.DeskID, WorkId: desk.WorkID,
	}
}
