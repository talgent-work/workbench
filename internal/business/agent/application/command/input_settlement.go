package command

import (
	"context"
	"fmt"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"github.com/samber/oops"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentintegrationv1 "github.com/talgent-work/contracts/gen/go/agent/integration/v1"
)

// InputOutcome is how an input a caller delivered settled.
type InputOutcome string

const (
	// InputCompleted: a Turn processed it.
	InputCompleted InputOutcome = "completed"
	// InputAbandoned: the process ended before finishing it.
	InputAbandoned InputOutcome = "abandoned"
	// InputDropped: the Work closed before any Turn processed it.
	InputDropped InputOutcome = "dropped"
)

// InputSettlement is that one delivered input settled, named by the
// caller's delivery identity.
type InputSettlement struct {
	WorkID  string
	InputID string
	Outcome InputOutcome
	At      time.Time
}

const inputSettlementPublishTimeout = 5 * time.Second

var inputOutcomes = map[InputOutcome]agentintegrationv1.PlatformInputOutcome{
	InputCompleted: agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_COMPLETED,
	InputAbandoned: agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_ABANDONED,
	InputDropped:   agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_DROPPED,
}

// publishInputSettlement reports Platform Input Settlement to the caller that
// delivered the input, as a PlatformInputSettled Integration Message keyed by
// its Work, so one Work's settlements stay in order. Agent keeps no delivery
// record, so every accepted input that settles is reported and each caller
// recognizes its own; an input refused at acceptance is not, since its caller
// was refused. A report that fails is returned, so the feedback carrying it is
// retried.
func publishInputSettlement(parent context.Context, publisher integrationmessage.Publisher, settlement InputSettlement) error {
	outcome, ok := inputOutcomes[settlement.Outcome]
	if !ok {
		return fmt.Errorf("agent: unknown input outcome %q", settlement.Outcome)
	}
	payload := &agentintegrationv1.PlatformInputSettledV1{
		EventId: uuid.Must(uuid.NewV7()).String(), OccurredAt: timestamppb.New(settlement.At),
		WorkId: settlement.WorkID, InputId: settlement.InputID, Outcome: outcome,
	}
	msg, err := integrationmessage.New(integrationmessage.KindOf(payload), payload, integrationmessage.WithKey(settlement.WorkID))
	if err != nil {
		return oops.With("operation", "input_settlement.encode").Wrap(err)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), inputSettlementPublishTimeout)
	defer cancel()
	return oops.With("operation", "input_settlement.publish").With("agent_work_id", settlement.WorkID).Wrap(publisher.Publish(ctx, msg))
}
