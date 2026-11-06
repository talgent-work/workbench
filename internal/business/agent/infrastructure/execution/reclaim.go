package execution

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// ExecutionFeedback applies a supplied fact through the Agent application boundary.
type ExecutionFeedback interface {
	Handle(context.Context, command.ExecutionFeedback) error
}

// Reclaimer confirms, inside Agent, the compute a retired allocation's
// environment authority released. The completed removal obligation is the
// durable record of that release, so Agent's observation finds it on every
// realization, also when the confirmation is lost; a Work whose compute is
// not confirmed reclaimed stays in recovery until its observation applies it.
type Reclaimer struct {
	feedback ExecutionFeedback
}

func NewReclaimer(feedback ExecutionFeedback) (*Reclaimer, error) {
	if feedback == nil {
		return nil, errors.New("agent: execution feedback is required")
	}
	return &Reclaimer{feedback: feedback}, nil
}

// confirm tells Agent the compute of the Work's retired allocation was
// released. The removed allocation was retired when its process exited, so
// its release confirms compute reclamation; AgentWork ignores it before exit.
func (r *Reclaimer) confirm(ctx context.Context, workID string) error {
	return r.feedback.Handle(ctx, command.ExecutionFeedback{WorkID: workID, Kind: command.ComputeReclaimed})
}
