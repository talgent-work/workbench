package infrastructure

import (
	"context"
	"fmt"

	"github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"github.com/samber/oops"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentintegrationv1 "github.com/talgent-work/contracts/gen/go/agent/integration/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// AgentPublisher publishes Agent's Published Fact Contracts as Integration
// Messages through the deployment's publisher.
type AgentPublisher struct {
	publisher message.Publisher
}

var _ command.AgentPublisherPort = (*AgentPublisher)(nil)

func NewAgentPublisher(publisher message.Publisher) *AgentPublisher {
	return &AgentPublisher{publisher: publisher}
}

var inputOutcomes = map[command.InputOutcome]agentintegrationv1.PlatformInputOutcome{
	command.InputCompleted: agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_COMPLETED,
	command.InputAbandoned: agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_ABANDONED,
	command.InputDropped:   agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_DROPPED,
}

// PublishInputSettled publishes PlatformInputSettled keyed by the Work, so
// one Work's settlements stay in order.
func (p *AgentPublisher) PublishInputSettled(ctx context.Context, settlement command.InputSettlement) error {
	outcome, ok := inputOutcomes[settlement.Outcome]
	if !ok {
		return fmt.Errorf("agent: unknown input outcome %q", settlement.Outcome)
	}
	payload := &agentintegrationv1.PlatformInputSettledV1{
		EventId: uuid.Must(uuid.NewV7()).String(), OccurredAt: timestamppb.New(settlement.At),
		WorkId: settlement.WorkID, InputId: settlement.InputID, Outcome: outcome,
	}
	msg, err := message.New(message.KindOf(payload), payload, message.WithKey(settlement.WorkID), message.WithOccurredAt(settlement.At))
	if err != nil {
		return oops.With("operation", "input_settlement.encode").Wrap(err)
	}
	return oops.With("operation", "input_settlement.publish").With("agent_work_id", settlement.WorkID).Wrap(p.publisher.Publish(ctx, msg))
}
