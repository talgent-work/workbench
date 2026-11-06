package plugincatalog

import (
	"context"
	"time"

	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
)

var _ application.RepositoryCredentialPort = (*Client)(nil)

func (c *Client) SupplyRepositoryCredential(ctx context.Context, input application.RepositoryCredentialRequest) (application.RepositoryCredentialMaterial, error) {
	if c.credentialToken == "" {
		return application.RepositoryCredentialMaterial{}, application.ErrRepositoryAuthorizationUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := delegated(ctx, input.OwnerUserID, &marketplaceprivatev1.SupplyRepositoryCredentialRequest{OwnerUserId: input.OwnerUserID, AuthorizationId: input.AuthorizationID, RepositoryId: input.RepositoryID, CloneUrl: input.CloneURL})
	if err != nil {
		return application.RepositoryCredentialMaterial{}, err
	}
	request.Header().Set("Authorization", "Bearer "+c.credentialToken)
	response, err := c.credentials.SupplyRepositoryCredential(ctx, request)
	if err != nil {
		return application.RepositoryCredentialMaterial{}, credentialUnavailable(err)
	}
	return application.RepositoryCredentialMaterial{Username: response.Msg.GetUsername(), Password: response.Msg.GetPassword(), ExpiresAt: declaredExpiry(response.Msg.GetExpiresAtUnixMs())}, nil
}

// declaredExpiry maps Marketplace's declared expiry; 0 declares none.
func declaredExpiry(unixMillis int64) time.Time {
	if unixMillis == 0 {
		return time.Time{}
	}
	return time.UnixMilli(unixMillis)
}
