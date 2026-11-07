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

// Reclaimer releases, inside Agent, the compute of a retired allocation and
// confirms the release to its Work. The allocation records when its release
// was confirmed, so Agent's observation finds it on every realization, also
// when the confirmation is lost; a Work whose compute is not confirmed
// reclaimed stays in recovery until its observation applies it.
type Reclaimer struct {
	feedback ExecutionFeedback
}

func NewReclaimer(feedback ExecutionFeedback) (*Reclaimer, error) {
	if feedback == nil {
		return nil, errors.New("agent: execution feedback is required")
	}
	return &Reclaimer{feedback: feedback}, nil
}

// reconcileRelease advances a retired allocation's release: its environment
// authority releases whatever compute carries the allocation's ownership, the
// allocation records the release, and only then does Agent learn of it. An
// error, or compute not released yet, leaves the release outstanding.
func (r *Reclaimer) reconcileRelease(ctx context.Context, provisioning *Provisioning, record allocationRecord, authority environmentAuthority) error {
	attempt, cancel := context.WithTimeout(ctx, reconciliationAttemptTimeout)
	released, err := authority.release(attempt, record)
	cancel()
	if err != nil || !released {
		return err
	}
	if err := provisioning.recordRelease(ctx, record); err != nil {
		return err
	}
	return r.confirm(ctx, record.SessionID)
}

// confirm tells Agent the compute of the Work's retired allocation was
// released. The allocation was retired once its execution ended, so its
// release confirms compute reclamation; AgentWork ignores it before then.
func (r *Reclaimer) confirm(ctx context.Context, workID string) error {
	return r.feedback.Handle(ctx, command.ExecutionFeedback{WorkID: workID, Kind: command.ComputeReclaimed})
}
