package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type ServiceCredentialRequest struct{ OwnerID, AuthorizationID, Target string }

func (a *PersonalConnections) SupplyServiceCredential(ctx context.Context, request ServiceCredentialRequest) (credential.ServiceMaterial, error) {
	if strings.TrimSpace(request.OwnerID) == "" || strings.TrimSpace(request.AuthorizationID) == "" || strings.TrimSpace(request.Target) == "" {
		return credential.ServiceMaterial{}, domain.ErrInvalidRequiredField
	}
	authorization, err := a.authorizations.Get(ctx, request.AuthorizationID)
	if err != nil {
		return credential.ServiceMaterial{}, err
	}
	if authorization.OwnerID != request.OwnerID || authorization.Provider != domain.ProviderGitHub {
		return credential.ServiceMaterial{}, domain.ErrPermissionDenied
	}
	return a.github.SupplyServiceCredential(ctx, request.OwnerID, request.AuthorizationID, request.Target)
}
