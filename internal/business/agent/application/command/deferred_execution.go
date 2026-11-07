package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// deferredExecution holds the execution requests Domain behavior selects
// until the use case has saved the Work changes that authorized them; send
// then delivers them in order. Domain behavior still selects each request and
// its correlation. This is request-local sequencing, not a durable queue: an
// interrupted delivery leaves the saved Work change for reconciliation, and a
// use case that saves nothing sends nothing it did not mean to.
type deferredExecution struct {
	target  domain.ExecutionPort
	pending []func(context.Context) error
}

var _ domain.ExecutionPort = (*deferredExecution)(nil)

// deferExecution opens an isolated delivery scope for one use case.
func deferExecution(target domain.ExecutionPort) *deferredExecution {
	return &deferredExecution{target: target}
}

func (r *deferredExecution) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.StartExecution(ctx, request) })
	return nil
}

func (r *deferredExecution) StopExecution(_ context.Context, workID, requestID string) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.StopExecution(ctx, workID, requestID) })
	return nil
}

func (r *deferredExecution) CancelRunningTurn(_ context.Context, workID string) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.CancelRunningTurn(ctx, workID) })
	return nil
}

func (r *deferredExecution) ReclaimCompute(_ context.Context, workID string) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.ReclaimCompute(ctx, workID) })
	return nil
}

func (r *deferredExecution) RetireFencedRun(_ context.Context, workID string) error {
	r.pending = append(r.pending, func(ctx context.Context) error { return r.target.RetireFencedRun(ctx, workID) })
	return nil
}

// send delivers the held requests in order, stopping at the first failure.
func (r *deferredExecution) send(ctx context.Context) error {
	for _, request := range r.pending {
		if err := request(ctx); err != nil {
			return errors.Join(domain.ErrExecutionRequest, err)
		}
	}
	return nil
}
