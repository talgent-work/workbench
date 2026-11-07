package commandtest

import (
	"context"
	"fmt"
	"sync"

	integrationmessage "github.com/go-jimu/components/ddd/message"

	agentintegrationv1 "github.com/talgent-work/contracts/gen/go/agent/integration/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// InputSettlements records the Platform Input Settlements published.
type InputSettlements struct {
	mu       sync.Mutex
	Reported []command.InputSettlement
	Err      error
}

var _ integrationmessage.Publisher = (*InputSettlements)(nil)

func (s *InputSettlements) Publish(_ context.Context, msg integrationmessage.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	settled, ok := msg.Payload().(*agentintegrationv1.PlatformInputSettledV1)
	if !ok {
		return fmt.Errorf("commandtest: unexpected message %T", msg.Payload())
	}
	s.Reported = append(s.Reported, command.InputSettlement{WorkID: settled.GetWorkId(), InputID: settled.GetInputId(), Outcome: outcomes[settled.GetOutcome()], At: settled.GetOccurredAt().AsTime()})
	return nil
}

var outcomes = map[agentintegrationv1.PlatformInputOutcome]command.InputOutcome{
	agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_COMPLETED: command.InputCompleted,
	agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_ABANDONED: command.InputAbandoned,
	agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_DROPPED:   command.InputDropped,
}
