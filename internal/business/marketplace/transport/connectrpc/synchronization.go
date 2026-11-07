package connectrpc

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

type SynchronizationHandler struct {
	app            *application.CatalogSynchronization
	authentication *CredentialHandler
}

func NewSynchronizationHandler(app *application.CatalogSynchronization, token string) *SynchronizationHandler {
	return &SynchronizationHandler{app, NewCredentialHandler(nil, token)}
}
func (h *SynchronizationHandler) RequestSynchronization(ctx context.Context, req *marketplaceprivatev1.RequestSynchronizationRequest) (*marketplaceprivatev1.RequestSynchronizationResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authentication.authenticate(ctx, info.RequestHeader()); err != nil {
		return nil, err
	}
	run, err := h.app.Request(ctx, req.GetForce())
	if err != nil {
		return nil, synchronizationError(err)
	}
	return &marketplaceprivatev1.RequestSynchronizationResponse{Run: synchronizationMessage(run)}, nil
}
func (h *SynchronizationHandler) GetSynchronization(ctx context.Context, req *marketplaceprivatev1.GetSynchronizationRequest) (*marketplaceprivatev1.GetSynchronizationResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authentication.authenticate(ctx, info.RequestHeader()); err != nil {
		return nil, err
	}
	run, err := h.app.Latest(ctx)
	if err != nil {
		return nil, synchronizationError(err)
	}
	return &marketplaceprivatev1.GetSynchronizationResponse{Run: synchronizationMessage(run)}, nil
}
func synchronizationError(err error) error {
	if errors.Is(err, application.ErrSynchronizationDisabled) {
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	}
	return connect.NewError(connect.CodeUnavailable, "marketplace synchronization unavailable")
}
func synchronizationMessage(run *application.SynchronizationRun) *marketplaceprivatev1.SynchronizationRun {
	if run == nil {
		return nil
	}
	message := &marketplaceprivatev1.SynchronizationRun{Id: run.ID, Status: run.Status, Force: run.Force, RequestedAtUnixMs: run.RequestedAt, DeadlineUnixMs: run.Deadline, FinishedAtUnixMs: run.FinishedAt}
	for _, failure := range run.Failures {
		message.Failures = append(message.Failures, &marketplaceprivatev1.SynchronizationFailure{Source: failure.Source, Slug: failure.Slug, Reason: failure.Reason})
	}
	return message
}
