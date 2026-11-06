package infrastructure

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// executionRequests defers outbound delivery until the command has saved the
// Work changes that authorized it. Domain behavior still selects the request
// and its correlation. This is request-local sequencing, not a durable queue;
// an interrupted delivery leaves the saved Work change for reconciliation.
type executionRequests struct {
	target  domain.ExecutionPort
	pending []func(context.Context) error
}

var _ command.ExecutionRequests = (*executionRequests)(nil)

func (r *executionRequests) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.StartExecution(ctx, request) })
	return nil
}
func (r *executionRequests) StopExecution(_ context.Context, workID, requestID string) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.StopExecution(ctx, workID, requestID) })
	return nil
}
func (r *executionRequests) CancelRunningTurn(_ context.Context, workID string) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.CancelRunningTurn(ctx, workID) })
	return nil
}
func (r *executionRequests) ReclaimCompute(_ context.Context, workID string) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.ReclaimCompute(ctx, workID) })
	return nil
}
func (r *executionRequests) RetireFencedRun(_ context.Context, workID string) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.RetireFencedRun(ctx, workID) })
	return nil
}
func (r *executionRequests) Send(ctx context.Context) error {
	for _, request := range r.pending {
		if err := request(ctx); err != nil {
			return errors.Join(domain.ErrExecutionRequest, err)
		}
	}
	return nil
}

func NewExecutionRequestsFactory(target domain.ExecutionPort) command.ExecutionRequestsFactory {
	return func() command.ExecutionRequests { return &executionRequests{target: target} }
}
