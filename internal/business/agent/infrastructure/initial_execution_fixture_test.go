package infrastructure_test

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// noInitialExecution is a Work store without a first-start obligation.
type noInitialExecution struct{}

func (noInitialExecution) Get(context.Context, string) (command.InitialExecutionRequest, error) {
	return command.InitialExecutionRequest{}, command.ErrInitialExecutionNotFound
}
