package dispatcher

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

func (m *NativeExecutions) CancelTurn(ctx context.Context, input application.CancelTurn) error {
	if strings.TrimSpace(input.TurnID) == "" {
		return command.ErrExecutionConnection
	}
	entry, err := m.find(input.WorkID)
	if err != nil {
		return err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.ProtocolState == nil || entry.broken || !entry.Ready || !entry.attached || entry.Exited {
		return command.ErrExecutionNotReady
	}
	// Keep one cancellation receipt. Repeating an already accepted request never
	// sends another interrupt, including after the following turn has started.
	if entry.CancelTurnID == input.TurnID {
		return nil
	}
	if !entry.Active || entry.Projection.CurrentTurnID != input.TurnID {
		return command.ErrExecutionNotReady
	}
	entry.CancelTurnID, entry.CancelAck = input.TurnID, false
	if err := m.snapshot(ctx, entry); err != nil {
		return err
	}
	return m.advanceTurnCancel(ctx, entry)
}

// Caller holds entry.mu. Replay restores the turn boundary before connected
// resends it, so a recovered cancel cannot target a later turn.
func (m *NativeExecutions) advanceTurnCancel(ctx context.Context, entry *nativeExecution) error {
	if entry.CancelTurnID == "" || entry.CancelAck {
		return nil
	}
	if !entry.Active || entry.Projection.CurrentTurnID != entry.CancelTurnID {
		// The target ended even if its interrupt receipt was not observed.
		return nil
	}
	if err := entry.connection.Interrupt(ctx, "talgent-cancel-"+entry.CancelTurnID); err != nil {
		return entry.disconnect(err)
	}
	return nil
}

func (m *NativeExecutions) GetCurrentTurn(ctx context.Context, workID string) (application.CurrentTurn, error) {
	if err := ctx.Err(); err != nil {
		return application.CurrentTurn{}, err
	}
	entry, err := m.find(workID)
	if err != nil {
		return application.CurrentTurn{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.ProtocolState == nil || entry.broken || !entry.attached {
		return application.CurrentTurn{}, command.ErrExecutionNotReady
	}
	if !entry.Active || entry.Exited {
		return application.CurrentTurn{}, nil
	}
	return application.CurrentTurn{Active: true, ID: entry.Projection.CurrentTurnID, CancelRequested: entry.CancelTurnID != "" && entry.CancelTurnID == entry.Projection.CurrentTurnID}, nil
}
