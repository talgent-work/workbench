package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type ServiceCredentialRequest struct{ OwnerID, AuthorizationID, Target string }

// SupplyServiceCredential supplies the credential of one target the
// authorization's Provider declares, for whichever Provider it is.
func (a *PersonalConnections) SupplyServiceCredential(ctx context.Context, request ServiceCredentialRequest) (credential.ServiceMaterial, error) {
	if strings.TrimSpace(request.OwnerID) == "" || strings.TrimSpace(request.AuthorizationID) == "" || strings.TrimSpace(request.Target) == "" {
		return credential.ServiceMaterial{}, domain.ErrInvalidRequiredField
	}
	if a.credentials == nil {
		return credential.ServiceMaterial{}, ErrProviderUnavailable
	}
	return a.credentials.SupplyTarget(ctx, request.OwnerID, request.AuthorizationID, request.Target)
}
