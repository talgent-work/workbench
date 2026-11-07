package messagepublisher

import (
	"context"
	"log/slog"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	computerintegrationv1 "github.com/talgent-work/contracts/gen/computer/integration/v1"
	"github.com/talgent-work/workbench/internal/business/computer/application"
)

// Removals publishes Computer Removed for each committed removal, best
// effort and keyed by the Computer. Agent also reads whether the Computer of
// each Work it holds a run for was removed, so a lost fact only delays
// applying the loss of their environments. Publications run for the
// application's lifetime: Stop ends them.
type Removals struct {
	publications *publications
}

var _ application.Removals = (*Removals)(nil)

func NewRemovals(bus integrationmessage.Publisher, logger *slog.Logger) *Removals {
	return &Removals{publications: newPublications(bus, logger, "computer.removals")}
}

func (p *Removals) Publish(parent context.Context, removed application.RemovedComputer) {
	if p == nil {
		return
	}
	payload := &computerintegrationv1.ComputerRemovedV1{EventId: uuid.Must(uuid.NewV7()).String(), OccurredAt: timestamppb.New(removed.At), ComputerId: removed.ComputerID}
	p.publications.publish(parent, "computer removed", []fact{{payload: payload, key: removed.ComputerID, attrs: []any{
		slog.String("computer_id", removed.ComputerID), slog.String("event_id", payload.GetEventId()),
	}}})
}

// Stop starts no further publication and waits for those under way until
// ctx ends, then cancels them.
func (p *Removals) Stop(ctx context.Context) error { return p.publications.stop(ctx) }
