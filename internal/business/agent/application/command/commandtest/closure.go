package commandtest

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// NoClosureObligations reports a Work with no startup, initial input or continuation
// outstanding, for tests whose close is not about those obligations.
type NoClosureObligations struct{}

var (
	_ command.InitialExecutionReader = NoClosureObligations{}
	_ query.WorkStartupReader        = NoClosureObligations{}
	_ query.ContinuationReader       = NoClosureObligations{}
)

func (NoClosureObligations) Get(context.Context, string) (command.InitialExecutionRequest, error) {
	return command.InitialExecutionRequest{}, command.ErrInitialExecutionNotFound
}

func (NoClosureObligations) GetWorkStartup(context.Context, string) (query.WorkStartup, error) {
	return query.WorkStartup{}, nil
}

func (NoClosureObligations) GetWorkContinuation(context.Context, string) (query.WorkContinuation, error) {
	return query.WorkContinuation{}, nil
}

func (NoClosureObligations) ListPendingContinuations(context.Context, string, int) ([]string, error) {
	return nil, nil
}
