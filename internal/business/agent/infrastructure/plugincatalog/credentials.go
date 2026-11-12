package plugincatalog

import (
	"context"
	"time"

	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

var _ command.MarketplacePort = (*Client)(nil)

func (c *Client) SupplyRepositoryCredential(ctx context.Context, input command.RepositoryCredentialRequest) (command.RepositoryCredentialMaterial, error) {
	if c.credentialToken == "" {
		return command.RepositoryCredentialMaterial{}, command.ErrRepositoryAuthorizationUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx, info, err := delegated(ctx, input.OwnerID)
	if err != nil {
		return command.RepositoryCredentialMaterial{}, err
	}
	info.RequestHeader().Set("Authorization", "Bearer "+c.credentialToken)
	response, err := c.credentials.SupplyRepositoryCredential(ctx, &marketplaceprivatev1.SupplyRepositoryCredentialRequest{OwnerUserId: input.OwnerID, AuthorizationId: input.AuthorizationID, RepositoryId: input.RepositoryID, CloneUrl: input.CloneURL})
	if err != nil {
		return command.RepositoryCredentialMaterial{}, credentialUnavailable(err, info)
	}
	return command.RepositoryCredentialMaterial{Username: response.GetUsername(), Password: response.GetPassword(), ExpiresAt: declaredExpiry(response.GetExpiresAtUnixMs())}, nil
}

// declaredExpiry maps Marketplace's declared expiry; 0 declares none.
func declaredExpiry(unixMillis int64) time.Time {
	if unixMillis == 0 {
		return time.Time{}
	}
	return time.UnixMilli(unixMillis)
}
