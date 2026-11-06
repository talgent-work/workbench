package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StartupDeadline bounds one start of a Work, counted from its run being
// seated on its Computer; waiting in its Computer's line comes before it.
// Required Startup Steps fail the Work when they cannot complete by then;
// other steps degrade. Repository checkout and Plugin preparation have no
// limit of their own.
const StartupDeadline = 15 * time.Minute

// Degradable Startup Steps end early enough for the Required Startup Steps
// that follow them. Plugin preparation and then repository checkout run inside
// the Work's environment before the Harness starts, so both are
// followed by the Harness start.
const preparationFollowingSteps = 2 * time.Minute

// Failure reasons name the earliest Required Startup Step still incomplete.
const (
	startupEnvironmentIncomplete = "Startup Deadline reached before the execution environment was ready."
	startupHarnessIncomplete     = "Startup Deadline reached before the Harness started."
)

// FailAtStartupDeadline fails the current start once its Startup Deadline has
// passed with a Required Startup Step incomplete: the environment or the
// Harness of a Work still Starting. The held process is asked to end. The
// result reports a state change.
func (s *AgentWork) FailAtStartupDeadline(ctx context.Context, now time.Time, port ExecutionPort) (bool, error) {
	p := s.HarnessProcess
	if p == nil || p.DeskWaiting || now.Before(p.StartupDeadlineAt()) {
		return false, nil
	}
	var reason string
	switch {
	case s.State == WorkStarting && !p.Prepared:
		reason = startupEnvironmentIncomplete
	case s.State == WorkStarting:
		reason = startupHarnessIncomplete
	default:
		return false, nil
	}
	s.recordFailure(reason)
	return true, s.RequestExecutionEnd(ctx, uuid.Must(uuid.NewV7()).String(), port)
}

// StartupDeadlineAt is when the current start's Startup Deadline passes,
// counted from when it was seated; a start waiting for its Desk has none yet.
func (p *HarnessProcess) StartupDeadlineAt() time.Time {
	return p.SeatedAt.Add(StartupDeadline)
}

func (p *HarnessProcess) degradableStepBound(following time.Duration) time.Time {
	return p.SeatedAt.Add(StartupDeadline - following)
}
