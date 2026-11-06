package connectrpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/pkg/authn"
)

type CredentialHandler struct {
	app   *application.PersonalConnections
	token string
}

func NewCredentialHandler(app *application.PersonalConnections, token string) *CredentialHandler {
	return &CredentialHandler{app: app, token: token}
}

func (h *CredentialHandler) SupplyRepositoryCredential(ctx context.Context, request *connect.Request[marketplaceprivatev1.SupplyRepositoryCredentialRequest]) (*connect.Response[marketplaceprivatev1.SupplyRepositoryCredentialResponse], error) {
	if err := h.authenticate(ctx, request.Header()); err != nil {
		return nil, err
	}
	material, err := h.app.SupplyRepositoryCredential(ctx, application.RepositoryCredentialRequest{
		OwnerUserID: request.Msg.GetOwnerUserId(), AuthorizationID: request.Msg.GetAuthorizationId(), RepositoryID: request.Msg.GetRepositoryId(), CloneURL: request.Msg.GetCloneUrl(),
	})
	if err != nil {
		return nil, credentialSupplyError(err)
	}
	return connect.NewResponse(&marketplaceprivatev1.SupplyRepositoryCredentialResponse{Username: material.Username, Password: material.Password, ExpiresAtUnixMs: expiryMillis(material.ExpiresAt)}), nil
}

func (h *CredentialHandler) SupplyServiceCredential(ctx context.Context, request *connect.Request[marketplaceprivatev1.SupplyServiceCredentialRequest]) (*connect.Response[marketplaceprivatev1.SupplyServiceCredentialResponse], error) {
	if err := h.authenticate(ctx, request.Header()); err != nil {
		return nil, err
	}
	material, err := h.app.SupplyServiceCredential(ctx, application.ServiceCredentialRequest{OwnerUserID: request.Msg.GetOwnerUserId(), AuthorizationID: request.Msg.GetAuthorizationId(), Target: request.Msg.GetTarget()})
	if err != nil {
		return nil, credentialSupplyError(err)
	}
	return connect.NewResponse(&marketplaceprivatev1.SupplyServiceCredentialResponse{Token: material.Token, ExpiresAtUnixMs: expiryMillis(material.ExpiresAt)}), nil
}

func credentialSupplyError(err error) error {
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
	mapped := connect.NewError(connect.CodeFailedPrecondition, errors.New("service credential unavailable"))
	mapped.Meta().Set("X-Talgent-Credential-Status", reason)
	return mapped
}

func (h *CredentialHandler) authenticate(ctx context.Context, header http.Header) error {
	_, native := authn.NativeProcessFromContext(ctx)
	if h.token == "" || native || header.Get(authn.HeaderNativeWorkID) != "" || header.Get(authn.HeaderNativeCredential) != "" || subtle.ConstantTimeCompare([]byte(header.Get("Authorization")), []byte("Bearer "+h.token)) != 1 {
		return connect.NewError(connect.CodePermissionDenied, errors.New("trusted credential consumer required"))
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
