package commandtest

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// NoClosureObligations reports a Work with no continuation outstanding, for
// tests whose close is not about it.
type NoClosureObligations struct{}

var _ query.ContinuationReader = NoClosureObligations{}

func (NoClosureObligations) GetWorkContinuation(context.Context, string) (query.WorkContinuation, error) {
	return query.WorkContinuation{}, nil
}

func (NoClosureObligations) ListPendingContinuations(context.Context, string, int) ([]string, error) {
	return nil, nil
}
