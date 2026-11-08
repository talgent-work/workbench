package commandtest

import (
	"context"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// InputSettlements records the Platform Input Settlements published.
type InputSettlements struct {
	mu       sync.Mutex
	Reported []command.InputSettlement
	Err      error
}

var _ command.AgentPublisherPort = (*InputSettlements)(nil)

func (s *InputSettlements) PublishInputSettled(_ context.Context, settlement command.InputSettlement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	s.Reported = append(s.Reported, settlement)
	return nil
}
