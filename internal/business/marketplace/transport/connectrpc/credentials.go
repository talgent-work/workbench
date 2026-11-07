package connectrpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"connectrpc.com/connect/v2"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

type CredentialHandler struct {
	app   *application.PersonalConnections
	token string
}

func NewCredentialHandler(app *application.PersonalConnections, token string) *CredentialHandler {
	return &CredentialHandler{app: app, token: token}
}

func (h *CredentialHandler) SupplyRepositoryCredential(ctx context.Context, request *marketplaceprivatev1.SupplyRepositoryCredentialRequest) (*marketplaceprivatev1.SupplyRepositoryCredentialResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(ctx, info.RequestHeader()); err != nil {
		return nil, err
	}
	material, err := h.app.SupplyRepositoryCredential(ctx, application.RepositoryCredentialRequest{
		OwnerUserID: request.GetOwnerUserId(), AuthorizationID: request.GetAuthorizationId(), RepositoryID: request.GetRepositoryId(), CloneURL: request.GetCloneUrl(),
	})
	if err != nil {
		return nil, credentialSupplyError(ctx, err)
	}
	return &marketplaceprivatev1.SupplyRepositoryCredentialResponse{Username: material.Username, Password: material.Password, ExpiresAtUnixMs: expiryMillis(material.ExpiresAt)}, nil
}

func (h *CredentialHandler) SupplyServiceCredential(ctx context.Context, request *marketplaceprivatev1.SupplyServiceCredentialRequest) (*marketplaceprivatev1.SupplyServiceCredentialResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(ctx, info.RequestHeader()); err != nil {
		return nil, err
	}
	material, err := h.app.SupplyServiceCredential(ctx, application.ServiceCredentialRequest{OwnerUserID: request.GetOwnerUserId(), AuthorizationID: request.GetAuthorizationId(), Target: request.GetTarget()})
	if err != nil {
		return nil, credentialSupplyError(ctx, err)
	}
	return &marketplaceprivatev1.SupplyServiceCredentialResponse{Token: material.Token, ExpiresAtUnixMs: expiryMillis(material.ExpiresAt)}, nil
}

// CredentialStatusHeader states, in the refusal's response metadata, why a
// credential was refused.
const CredentialStatusHeader = "X-Talgent-Credential-Status"

// credentialSupplyError refuses a credential and states why in the call's
// response metadata.
func credentialSupplyError(ctx context.Context, err error) error {
	reason := "unavailable"
	switch {
	case errors.Is(err, domain.ErrAuthorizationRevoked):
		reason = "revoked"
	case errors.Is(err, domain.ErrAuthorizationReconnectRequired):
		reason = "reconnect_required"
	case errors.Is(err, domain.ErrCredentialTargetInapplicable):
		reason = "incompatible"
	case errors.Is(err, domain.ErrPermissionDenied):
		reason = "access_denied"
	case errors.Is(err, domain.ErrProviderNotFound):
		// A deleted authorization supplies nothing ever again.
		reason = "revoked"
	}
	var providerErr *application.ProviderError
	if errors.As(err, &providerErr) {
		switch providerErr.Code {
		case "github_authorization_revoked":
			reason = "revoked"
		case "github_reconnect_required":
			reason = "reconnect_required"
		}
	}
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		info.ResponseHeader().Set(CredentialStatusHeader, reason)
	}
	return connect.NewError(connect.CodeFailedPrecondition, "service credential unavailable")
}

func (h *CredentialHandler) authenticate(ctx context.Context, header *connect.Header) error {
	_, native := authn.NativeProcessFromContext(ctx)
	if h.token == "" || native || header.Get(authn.HeaderNativeWorkID) != "" || header.Get(authn.HeaderNativeCredential) != "" || subtle.ConstantTimeCompare([]byte(header.Get("Authorization")), []byte("Bearer "+h.token)) != 1 {
		return connect.NewError(connect.CodePermissionDenied, "trusted credential consumer required")
	}
	return nil
}

// expiryMillis declares no expiry as 0.
func expiryMillis(expiresAt time.Time) int64 {
	if expiresAt.IsZero() {
		return 0
	}
	return expiresAt.UnixMilli()
}
