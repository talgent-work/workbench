package plugincatalog

import (
	"context"
	"time"

	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
)

var _ application.CredentialSupplyPort = (*Client)(nil)

func (c *Client) SupplyRepositoryCredential(ctx context.Context, input application.RepositoryCredentialRequest) (application.RepositoryCredentialMaterial, error) {
	if c.credentialToken == "" {
		return application.RepositoryCredentialMaterial{}, application.ErrRepositoryAuthorizationUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx, info, err := delegated(ctx, input.OwnerUserID)
	if err != nil {
		return application.RepositoryCredentialMaterial{}, err
	}
	info.RequestHeader().Set("Authorization", "Bearer "+c.credentialToken)
	response, err := c.credentials.SupplyRepositoryCredential(ctx, &marketplaceprivatev1.SupplyRepositoryCredentialRequest{OwnerUserId: input.OwnerUserID, AuthorizationId: input.AuthorizationID, RepositoryId: input.RepositoryID, CloneUrl: input.CloneURL})
	if err != nil {
		return application.RepositoryCredentialMaterial{}, credentialUnavailable(err, info)
	}
	return application.RepositoryCredentialMaterial{Username: response.GetUsername(), Password: response.GetPassword(), ExpiresAt: declaredExpiry(response.GetExpiresAtUnixMs())}, nil
}

// declaredExpiry maps Marketplace's declared expiry; 0 declares none.
func declaredExpiry(unixMillis int64) time.Time {
	if unixMillis == 0 {
		return time.Time{}
	}
	return time.UnixMilli(unixMillis)
}
