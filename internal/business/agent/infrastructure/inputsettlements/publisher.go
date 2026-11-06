// Package inputsettlements publishes Agent's Platform Input Settlement.
package inputsettlements

import (
	"context"
	"fmt"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"github.com/samber/oops"
	"google.golang.org/protobuf/types/known/timestamppb"

	agenteventsv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/events/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

const publishTimeout = 5 * time.Second

var outcomes = map[command.InputOutcome]agenteventsv1.PlatformInputOutcome{
	command.InputCompleted: agenteventsv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_COMPLETED,
	command.InputAbandoned: agenteventsv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_ABANDONED,
	command.InputDropped:   agenteventsv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_DROPPED,
}

// Publisher publishes each settlement as a PlatformInputSettled Integration
// Message keyed by its Work, so one Work's settlements stay in order.
type Publisher struct{ bus integrationmessage.Publisher }

var _ command.InputSettlements = (*Publisher)(nil)

func NewPublisher(bus integrationmessage.Publisher) *Publisher { return &Publisher{bus: bus} }

func (p *Publisher) Settled(parent context.Context, settlement command.InputSettlement) error {
	outcome, ok := outcomes[settlement.Outcome]
	if !ok {
		return fmt.Errorf("agent: unknown input outcome %q", settlement.Outcome)
	}
	payload := &agenteventsv1.PlatformInputSettled{
		EventId: uuid.Must(uuid.NewV7()).String(), OccurredAt: timestamppb.New(settlement.At),
		WorkId: settlement.WorkID, InputId: settlement.InputID, Outcome: outcome,
	}
	msg, err := integrationmessage.New(integrationmessage.KindOf(payload), payload, integrationmessage.WithKey(settlement.WorkID))
	if err != nil {
		return oops.With("operation", "input_settlement.encode").Wrap(err)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), publishTimeout)
	defer cancel()
	return oops.With("operation", "input_settlement.publish").With("agent_work_id", settlement.WorkID).Wrap(p.bus.Publish(ctx, msg))
}
