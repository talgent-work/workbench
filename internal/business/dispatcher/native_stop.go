package dispatcher

// A process is stopped only between Turns: stopping is one exit
// signal, refused retryably while a Turn is active. Dispatcher keeps no stop
// record; Agent redelivers the stop under its original identity until the
// process exits.

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

func (m *NativeExecutions) Stop(ctx context.Context, input command.StopExecution) error {
	if strings.TrimSpace(input.StopRequestID) == "" {
		return command.ErrExecutionConnection
	}
	entry, err := m.find(input.WorkID)
	if err != nil {
		return err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.ProtocolState == nil || entry.broken || !entry.attached || entry.Exited {
		return command.ErrExecutionNotReady
	}
	if entry.Active {
		return command.ErrTurnActive
	}
	entry.exitSignalled = true
	if err := entry.connection.Exit(ctx); err != nil {
		return entry.disconnect(err)
	}
	return nil
}
