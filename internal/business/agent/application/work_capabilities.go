package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

var ErrWorkCapabilitiesUnavailable = errors.New("agent: current Harness capabilities unavailable")

func (a *Application) GetWorkCapabilities(ctx context.Context, input ControlWork) (command.WorkCapabilities, error) {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return command.WorkCapabilities{}, err
	}
	work, err := a.WorkQueries.GetWorkView(ctx, input.WorkID)
	if err != nil {
		return command.WorkCapabilities{}, err
	}
	if work.State != "working" {
		return command.WorkCapabilities{}, ErrWorkCapabilitiesUnavailable
	}
	return a.Harness.GetWorkCapabilities(ctx, input.WorkID)
}
