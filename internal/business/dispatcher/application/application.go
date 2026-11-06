package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

type Commands struct {
	ConnectExecution *command.ConnectExecutionHandler
	EnqueueInput     *command.EnqueueInputHandler
	DropInput        *command.DropInputHandler
	StopExecution    *command.StopExecutionHandler
}

type Queries struct{}

type Application struct {
	capabilities CapabilityReaderPort
	Commands     Commands
	Queries      Queries
	turns        TurnControlPort
	settings     HarnessSettingsPort
	interactions PendingInteractionsPort
	processes    ProcessObservationReader
	buffer       command.InputBuffer
}

func NewApplication(capabilities CapabilityReaderPort, connect *command.ConnectExecutionHandler, enqueue *command.EnqueueInputHandler, drop *command.DropInputHandler, stop *command.StopExecutionHandler, settings HarnessSettingsPort, interactions PendingInteractionsPort, turns TurnControlPort, processes ProcessObservationReader, buffer command.InputBuffer) *Application {
	return &Application{capabilities: capabilities, Commands: Commands{ConnectExecution: connect, EnqueueInput: enqueue, DropInput: drop, StopExecution: stop}, settings: settings, interactions: interactions, turns: turns, processes: processes, buffer: buffer}
}

// ListBufferedInput reads a Work's unsettled buffered entries in order.
func (a *Application) ListBufferedInput(ctx context.Context, workID string) ([]command.BufferedInput, error) {
	if workID == "" {
		return nil, command.ErrExecutionConnection
	}
	return a.buffer.List(ctx, workID)
}
