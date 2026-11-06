package application

import (
	"context"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type RepositoryCredentialRequest struct {
	OwnerUserID, AuthorizationID, RepositoryID, CloneURL string
}

// RepositoryCredential is transient material for a trusted execution boundary.
// It must never be mapped to ordinary connection responses or saved configuration.
type RepositoryCredential struct {
	Username, Password string
	ExpiresAt          time.Time
}

func (a *PersonalConnections) SupplyRepositoryCredential(ctx context.Context, request RepositoryCredentialRequest) (RepositoryCredential, error) {
	if strings.TrimSpace(request.OwnerUserID) == "" || strings.TrimSpace(request.AuthorizationID) == "" || strings.TrimSpace(request.RepositoryID) == "" || strings.TrimSpace(request.CloneURL) == "" {
		return RepositoryCredential{}, domain.ErrInvalidRequiredField
	}
	authorization, err := a.authorizations.Get(ctx, request.AuthorizationID)
	if err != nil {
		return RepositoryCredential{}, err
	}
	if authorization.OwnerUserID != request.OwnerUserID || authorization.Provider != domain.ProviderGitHub {
		return RepositoryCredential{}, domain.ErrPermissionDenied
	}
	// The observed scope verifies the selected external identity and target; it
	// grants nothing, and the Provider still decides the checkout.
	// No personal binding lookup is involved: unbinding does not rewrite Work.
	repository, found := authorization.AccessScope.Repository(request.RepositoryID)
	if !found {
		// The retained scope may be stale: ask the Provider before refusing.
		if err := a.observeAccessScope(ctx, authorization.ID, authorizationObservationTimeout); err != nil {
			return RepositoryCredential{}, err
		}
		if authorization, err = a.authorizations.Get(ctx, request.AuthorizationID); err != nil {
			return RepositoryCredential{}, err
		}
		if repository, found = authorization.AccessScope.Repository(request.RepositoryID); !found {
			return RepositoryCredential{}, domain.ErrRepositoryNotFound
		}
	}
	if repository.CloneURL != request.CloneURL {
		return RepositoryCredential{}, domain.ErrCredentialTargetInapplicable
	}
	material, err := a.supplier.SupplyRepositoryCredential(ctx, request.OwnerUserID, request.AuthorizationID, request.CloneURL)
	if err != nil {
		return RepositoryCredential{}, err
	}
	return RepositoryCredential{Username: material.Username, Password: material.Password, ExpiresAt: material.ExpiresAt}, nil
}
