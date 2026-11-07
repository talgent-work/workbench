package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// SetPermissionMode changes a running Managed Work's permission mode.
type SetPermissionMode struct {
	ActorUserID, WorkID string
	Mode                domain.PermissionMode
}

// SetPermissionMode applies the mode to the running Harness at once and
// answers the mode it accepted. The Work retains a mode only as its Harness
// process reports it (ApplyExecutionFeedback): a report names the process it
// came from, so an answer from a process a Stop or Resume already replaced
// changes nothing, and a change the Harness makes itself, such as leaving
// plan mode, is retained the same way (Agent model: Managed permission mode).
func (a *Application) SetPermissionMode(ctx context.Context, input SetPermissionMode) (domain.PermissionMode, error) {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return "", err
	}
	work, err := a.Works.Get(ctx, input.WorkID)
	if err != nil {
		return "", err
	}
	if err := work.AdmitPermissionModeChange(input.ActorUserID, input.Mode); err != nil {
		return "", err
	}
	return a.Harness.SetPermissionMode(ctx, work.ID, input.Mode)
}
