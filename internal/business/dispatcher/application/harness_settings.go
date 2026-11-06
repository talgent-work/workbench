package application

import "context"

type SetThinkingLevel struct {
	WorkID string
	Level  string
}

// SetPermissionMode asks the Work's Harness for a native permission mode.
type SetPermissionMode struct {
	WorkID string
	Mode   string
}

// HarnessSettingsPort adjusts the addressed Harness's settings without
// redefining when they take effect or coupling them to input delivery
// receipts.
type HarnessSettingsPort interface {
	SetThinkingLevel(context.Context, SetThinkingLevel) error
	GetThinkingLevel(ctx context.Context, workID string) (string, error)
	// SetPermissionMode answers the mode the Harness accepted.
	SetPermissionMode(context.Context, SetPermissionMode) (string, error)
}

func (a *Application) GetThinkingLevel(ctx context.Context, workID string) (string, error) {
	return a.settings.GetThinkingLevel(ctx, workID)
}

func (a *Application) SetThinkingLevel(ctx context.Context, input SetThinkingLevel) error {
	return a.settings.SetThinkingLevel(ctx, input)
}

func (a *Application) SetPermissionMode(ctx context.Context, input SetPermissionMode) (string, error) {
	return a.settings.SetPermissionMode(ctx, input)
}
