package connectrpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect/v2"

	computerprivatev1 "github.com/talgent-work/contracts/gen/go/computer/private/v1"
	"github.com/talgent-work/contracts/gen/go/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// ConnectionHandler serves the Computer Gateway, authenticated by the
// control token.
type ConnectionHandler struct {
	application *application.Application
	token       controlToken
}

var _ computerprivatev1connect.ConnectionServiceHandler = (*ConnectionHandler)(nil)

func NewConnectionHandler(app *application.Application, token string) *ConnectionHandler {
	return &ConnectionHandler{application: app, token: controlToken(token)}
}

// Authenticate answers a refused credential in its response, so the
// Gateway tells a refusal, which ends the machine's connection for good,
// from an error, after which the credential was not checked.
func (h *ConnectionHandler) Authenticate(ctx context.Context, req *computerprivatev1.AuthenticateRequest) (*computerprivatev1.AuthenticateResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetCredential()) == "" {
		return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED), nil
	}
	authenticated, err := h.application.Authenticate(ctx, req.GetCredential())
	switch {
	case errors.Is(err, domain.ErrCredentialRejected):
		return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED), nil
	case errors.Is(err, domain.ErrComputerRemoved):
		return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_COMPUTER_REMOVED), nil
	case err != nil:
		return nil, publicError(err)
	}
	return &computerprivatev1.AuthenticateResponse{ComputerId: authenticated.ComputerID, RotationDue: authenticated.RotationDue}, nil
}

func refused(refusal computerprivatev1.CredentialRefusal) *computerprivatev1.AuthenticateResponse {
	return &computerprivatev1.AuthenticateResponse{Refusal: refusal}
}

func (h *ConnectionHandler) RotateCredential(ctx context.Context, req *computerprivatev1.RotateCredentialRequest) (*computerprivatev1.RotateCredentialResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetCredential()) == "" {
		return nil, publicError(domain.ErrCredentialRejected)
	}
	issued, err := h.application.RotateCredential(ctx, req.GetComputerId(), req.GetCredential())
	if err != nil {
		return nil, publicError(err)
	}
	return &computerprivatev1.RotateCredentialResponse{Credential: issued.Credential, ExpiresAtUnixMs: issued.ExpiresAt.UnixMilli()}, nil
}

func (h *ConnectionHandler) ConfirmCredential(ctx context.Context, req *computerprivatev1.ConfirmCredentialRequest) (*computerprivatev1.ConfirmCredentialResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetCredential()) == "" {
		return nil, publicError(domain.ErrCredentialRejected)
	}
	if err := h.application.ConfirmCredential(ctx, req.GetComputerId(), req.GetCredential()); err != nil {
		return nil, publicError(err)
	}
	return &computerprivatev1.ConfirmCredentialResponse{}, nil
}

func (h *ConnectionHandler) ReportReachable(ctx context.Context, req *computerprivatev1.ReportReachableRequest) (*computerprivatev1.ReportReachableResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	report := application.MachineReport{
		Versions: domain.ReportedVersions{TalgentComputer: req.GetVersions().GetTalgentComputer(), ClaudeCode: req.GetVersions().GetClaudeCode()},
		Sandbox:  req.GetClaudeCodeSandbox(),
	}
	if err := h.application.ReportReachable(ctx, req.GetComputerId(), report); err != nil {
		return nil, publicError(err)
	}
	// This release reconciles every reconnected Managed Computer before
	// anything starts there (Computer model: Reconciliation), so the machine
	// holds its starts back until it is reconciled; a platform that does not
	// announce it is never waited for.
	return &computerprivatev1.ReportReachableResponse{MinimumVersion: h.application.MinimumVersion(), Reconciles: true}, nil
}

func (h *ConnectionHandler) ReportUnreachable(ctx context.Context, req *computerprivatev1.ReportUnreachableRequest) (*computerprivatev1.ReportUnreachableResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if req.GetSinceUnixMs() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "computer: when reach was lost is required")
	}
	if err := h.application.ReportUnreachable(ctx, req.GetComputerId(), time.UnixMilli(req.GetSinceUnixMs()).UTC()); err != nil {
		return nil, publicError(err)
	}
	return &computerprivatev1.ReportUnreachableResponse{}, nil
}
