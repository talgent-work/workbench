package dispatcher

import (
	"context"
	"slices"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

func (m *NativeExecutions) GetCapabilities(ctx context.Context, workID string) (application.Capabilities, error) {
	if err := ctx.Err(); err != nil {
		return application.Capabilities{}, err
	}
	entry, err := m.find(workID)
	if err != nil {
		return application.Capabilities{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.ProtocolState == nil || entry.broken || !entry.Ready || entry.Failure != "" || !entry.attached || entry.Exited {
		return application.Capabilities{}, command.ErrExecutionNotReady
	}
	value := entry.Capabilities
	value.Commands = slices.Clone(value.Commands)
	return value, nil
}
