package connectrpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"

	computerprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1/computerprivatev1connect"
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
func (h *ConnectionHandler) Authenticate(ctx context.Context, req *connect.Request[computerprivatev1.AuthenticateRequest]) (*connect.Response[computerprivatev1.AuthenticateResponse], error) {
	if err := h.token.authenticate(req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetCredential()) == "" {
		return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED), nil
	}
	authenticated, err := h.application.Authenticate(ctx, req.Msg.GetCredential())
	switch {
	case errors.Is(err, domain.ErrCredentialRejected):
		return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED), nil
	case errors.Is(err, domain.ErrComputerRemoved):
		return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_COMPUTER_REMOVED), nil
	case err != nil:
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerprivatev1.AuthenticateResponse{ComputerId: authenticated.ComputerID, RotationDue: authenticated.RotationDue}), nil
}

func refused(refusal computerprivatev1.CredentialRefusal) *connect.Response[computerprivatev1.AuthenticateResponse] {
	return connect.NewResponse(&computerprivatev1.AuthenticateResponse{Refusal: refusal})
}

func (h *ConnectionHandler) RotateCredential(ctx context.Context, req *connect.Request[computerprivatev1.RotateCredentialRequest]) (*connect.Response[computerprivatev1.RotateCredentialResponse], error) {
	if err := h.token.authenticate(req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetCredential()) == "" {
		return nil, publicError(domain.ErrCredentialRejected)
	}
	issued, err := h.application.RotateCredential(ctx, req.Msg.GetComputerId(), req.Msg.GetCredential())
	if err != nil {
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerprivatev1.RotateCredentialResponse{Credential: issued.Credential, ExpiresAtUnixMs: issued.ExpiresAt.UnixMilli()}), nil
}

func (h *ConnectionHandler) ConfirmCredential(ctx context.Context, req *connect.Request[computerprivatev1.ConfirmCredentialRequest]) (*connect.Response[computerprivatev1.ConfirmCredentialResponse], error) {
	if err := h.token.authenticate(req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetCredential()) == "" {
		return nil, publicError(domain.ErrCredentialRejected)
	}
	if err := h.application.ConfirmCredential(ctx, req.Msg.GetComputerId(), req.Msg.GetCredential()); err != nil {
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerprivatev1.ConfirmCredentialResponse{}), nil
}

func (h *ConnectionHandler) ReportReachable(ctx context.Context, req *connect.Request[computerprivatev1.ReportReachableRequest]) (*connect.Response[computerprivatev1.ReportReachableResponse], error) {
	if err := h.token.authenticate(req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	report := application.MachineReport{
		Versions: domain.ReportedVersions{TalgentComputer: req.Msg.GetVersions().GetTalgentComputer(), ClaudeCode: req.Msg.GetVersions().GetClaudeCode()},
		Sandbox:  req.Msg.GetClaudeCodeSandbox(),
	}
	if err := h.application.ReportReachable(ctx, req.Msg.GetComputerId(), report); err != nil {
		return nil, publicError(err)
	}
	// This release reconciles every reconnected Managed Computer before
	// anything starts there (Computer model: Reconciliation), so the machine
	// holds its starts back until it is reconciled; a platform that does not
	// announce it is never waited for.
	return connect.NewResponse(&computerprivatev1.ReportReachableResponse{MinimumVersion: h.application.MinimumVersion(), Reconciles: true}), nil
}

func (h *ConnectionHandler) ReportUnreachable(ctx context.Context, req *connect.Request[computerprivatev1.ReportUnreachableRequest]) (*connect.Response[computerprivatev1.ReportUnreachableResponse], error) {
	if err := h.token.authenticate(req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	if req.Msg.GetSinceUnixMs() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("computer: when reach was lost is required"))
	}
	if err := h.application.ReportUnreachable(ctx, req.Msg.GetComputerId(), time.UnixMilli(req.Msg.GetSinceUnixMs()).UTC()); err != nil {
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerprivatev1.ReportUnreachableResponse{}), nil
}
