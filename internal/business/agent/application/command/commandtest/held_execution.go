package commandtest

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// HeldExecution holds the execution requests a test's Domain behavior
// selects until the test sends them, as a use case does once it saved the
// Work changes that authorized them. It mirrors the use cases' own unexported
// deferral so tests outside package command can drive Domain behavior the
// same way; it is test support only.
type HeldExecution struct {
	target  domain.ExecutionPort
	pending []func(context.Context) error
}

var _ domain.ExecutionPort = (*HeldExecution)(nil)

// Hold opens a delivery scope over target.
func Hold(target domain.ExecutionPort) *HeldExecution { return &HeldExecution{target: target} }

func (h *HeldExecution) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	h.pending = append(h.pending, func(ctx context.Context) error { return h.target.StartExecution(ctx, request) })
	return nil
}

func (h *HeldExecution) StopExecution(_ context.Context, workID, requestID string) error {
	h.pending = append(h.pending, func(ctx context.Context) error { return h.target.StopExecution(ctx, workID, requestID) })
	return nil
}

func (h *HeldExecution) CancelRunningTurn(_ context.Context, workID string) error {
	h.pending = append(h.pending, func(ctx context.Context) error { return h.target.CancelRunningTurn(ctx, workID) })
	return nil
}

func (h *HeldExecution) ReclaimCompute(_ context.Context, workID string) error {
	h.pending = append(h.pending, func(ctx context.Context) error { return h.target.ReclaimCompute(ctx, workID) })
	return nil
}

func (h *HeldExecution) RetireFencedRun(_ context.Context, workID string) error {
	h.pending = append(h.pending, func(ctx context.Context) error { return h.target.RetireFencedRun(ctx, workID) })
	return nil
}

// Send delivers the held requests in order, stopping at the first failure.
func (h *HeldExecution) Send(ctx context.Context) error {
	for _, request := range h.pending {
		if err := request(ctx); err != nil {
			return errors.Join(domain.ErrExecutionRequest, err)
		}
	}
	return nil
}
