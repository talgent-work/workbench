package application

import (
	"context"
	"errors"
)

var ErrWorkCapabilitiesUnavailable = errors.New("agent: current Harness capabilities unavailable")

type WorkCommand struct{ Name, Description, ArgumentHint string }

type WorkCapabilities struct {
	Observed bool
	Commands []WorkCommand
}

type WorkCapabilitiesPort interface {
	GetWorkCapabilities(context.Context, string) (WorkCapabilities, error)
}

func (a *Application) GetWorkCapabilities(ctx context.Context, input ControlWork) (WorkCapabilities, error) {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return WorkCapabilities{}, err
	}
	work, err := a.WorkViews.GetWorkView(ctx, input.WorkID)
	if err != nil {
		return WorkCapabilities{}, err
	}
	if work.State != "working" {
		return WorkCapabilities{}, ErrWorkCapabilitiesUnavailable
	}
	return a.Capabilities.GetWorkCapabilities(ctx, input.WorkID)
}
