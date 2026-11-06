package domain

import (
	"slices"
	"strings"
)

type SubAgentStatus string

const (
	SubAgentRunning   SubAgentStatus = "running"
	SubAgentCompleted SubAgentStatus = "completed"
	SubAgentFailed    SubAgentStatus = "failed"
	SubAgentStopped   SubAgentStatus = "stopped"
)

// SubAgent is one SubAgent the main Agent delegated to, by its Harness
// identity, with the state the Harness last reported. It has no identity or
// lifecycle of its own on the Work.
type SubAgent struct {
	ID     string
	Type   string
	Task   string
	Status SubAgentStatus
}

func (a SubAgent) validate() error {
	switch a.Status {
	case SubAgentRunning, SubAgentCompleted, SubAgentFailed, SubAgentStopped:
	default:
		return ErrInvalidWork
	}
	if strings.TrimSpace(a.ID) == "" {
		return ErrInvalidWork
	}
	return nil
}

// SubAgents are those of the Work's current Harness process; none once it ended.
func (s *AgentWork) SubAgents() []SubAgent {
	if s.HarnessProcess == nil {
		return nil
	}
	return s.HarnessProcess.SubAgents
}

// RecordSubAgents replaces the current process's SubAgents with the supplied
// snapshot. sequence is its position in the process stream and only makes an
// exact replay a no-op; the caller admits only reports about the current
// process (feedback ordering).
func (s *AgentWork) RecordSubAgents(sequence int64, agents []SubAgent) error {
	if sequence <= 0 {
		return ErrInvalidWork
	}
	p := s.HarnessProcess
	if !p.Held() || sequence <= p.SubAgentsSequence {
		return nil
	}
	for _, agent := range agents {
		if err := agent.validate(); err != nil {
			return err
		}
	}
	p.SubAgents = slices.Clone(agents)
	p.SubAgentsSequence = sequence
	return nil
}
