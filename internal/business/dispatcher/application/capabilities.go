package application

import "context"

type NativeCommand struct{ Name, Description, ArgumentHint string }

type Capabilities struct {
	Observed bool
	Commands []NativeCommand
}

type CapabilityReaderPort interface {
	GetCapabilities(context.Context, string) (Capabilities, error)
}

func (a *Application) GetCapabilities(ctx context.Context, workID string) (Capabilities, error) {
	return a.capabilities.GetCapabilities(ctx, workID)
}
