package command

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// RunningHarnessPort reaches the Work's running Harness process through
// Dispatcher: its input buffer, its Turn and that Turn's cancellation, its
// settings and what it currently offers. A call never changes the Work's
// lifecycle; the Harness reports the facts that do separately. The Work has at
// most one Harness process, so the Work addresses it.
type RunningHarnessPort interface {
	// BufferInput hands input to the Work's input buffer; a hand-off is
	// accepted once buffered, and Dispatcher writes it to the Harness and
	// reports how it settled.
	BufferInput(ctx context.Context, workID string, input ExecutionInput) (InputHandOff, error)
	BufferResponse(ctx context.Context, workID string, response InteractionResponse) (InputHandOff, error)
	ReadBuffer(ctx context.Context, workID string) (BufferedInputs, error)
	// DropBuffer empties the buffer of a Work entering Closing.
	DropBuffer(ctx context.Context, workID string) error
	// GetCurrentTurn reads the Turn the Harness is running, if any.
	GetCurrentTurn(ctx context.Context, workID string) (CurrentTurn, error)
	// CancelTurn ends the named Turn; the Work and its process keep running.
	CancelTurn(ctx context.Context, workID, turnID string) error
	// SetThinkingLevel forwards the setting to the Harness, whose effect timing
	// is its own; AgentWork retains accepted settings.
	SetThinkingLevel(ctx context.Context, workID, level string) error
	// SetPermissionMode asks the Harness to change its permission mode and
	// answers the mode it accepted.
	SetPermissionMode(ctx context.Context, workID string, mode domain.PermissionMode) (domain.PermissionMode, error)
	// ListPendingInteractions lists the Harness's live requests for a user
	// answer; Dispatcher answers ReplySubmitted from the Work's input buffer.
	ListPendingInteractions(ctx context.Context, workID string) ([]PendingInteraction, error)
	// GetWorkCapabilities reads the capabilities the Harness loaded.
	GetWorkCapabilities(ctx context.Context, workID string) (WorkCapabilities, error)
}

// CurrentTurn is the Turn the Harness is running, if Active.
type CurrentTurn struct {
	ID              string
	CancelRequested bool
	Active          bool
}

// PendingInteraction is one of the Harness's live requests for a user answer.
type PendingInteraction struct {
	ID, ToolCallID, ToolName, Prompt string
	CanAlwaysAllow, ReplySubmitted   bool
	QuestionInputJSON, TurnID        string
	Agent                            *string
}

// WorkCommand is a command the Harness loaded.
type WorkCommand struct{ Name, Description, ArgumentHint string }

// WorkCapabilities are the capabilities the Harness loaded, once Observed.
type WorkCapabilities struct {
	Observed bool
	Commands []WorkCommand
}
