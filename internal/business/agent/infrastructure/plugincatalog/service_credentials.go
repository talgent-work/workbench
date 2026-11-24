package plugincatalog

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect/v2"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// credentialStatusHeader is the metadata in which Marketplace states why it
// refused a credential.
const credentialStatusHeader = "X-Talgent-Credential-Status"

func (c *Client) SupplyServiceCredential(ctx context.Context, input command.ServiceCredentialRequest) (command.ServiceCredentialMaterial, error) {
	if c.credentialToken == "" {
		return command.ServiceCredentialMaterial{}, &command.ServiceCredentialUnavailable{Reason: "unavailable", Temporary: true}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx, err := c.credentialed(ctx, input.OwnerID)
	if err != nil {
		return command.ServiceCredentialMaterial{}, err
	}
	response, err := c.credentials.SupplyServiceCredential(ctx, &marketplaceprivatev1.SupplyServiceCredentialRequest{OwnerUserId: input.OwnerID, AuthorizationId: input.AuthorizationID, Target: input.Target})
	if err != nil {
		return command.ServiceCredentialMaterial{}, credentialUnavailable(ctx, err)
	}
	return command.ServiceCredentialMaterial{Token: response.GetToken(), ExpiresAt: declaredExpiry(response.GetExpiresAtUnixMs()), Hosts: response.GetHosts(),
		Paths: response.GetPaths(), Methods: response.GetMethods()}, nil
}

// credentialUnavailable reads Marketplace's refusal of the call ctx made,
// whose status arrives in the call's response metadata.
func credentialUnavailable(ctx context.Context, err error) error {
	reason := "unavailable"
	var typed *connect.Error
	if errors.As(err, &typed) {
		if typed.Code() == connect.CodePermissionDenied {
			reason = "access_denied"
		}
		var reported string
		if info, ok := connect.CallInfoForClientContext(ctx); ok {
			reported = info.ResponseHeader().Get(credentialStatusHeader)
			if reported == "" {
				reported = info.ResponseTrailer().Get(credentialStatusHeader)
			}
		}
		switch reported {
		case "revoked", "reconnect_required", "incompatible", "access_denied", "unavailable":
			reason = reported
		}
	}
	// Only a reason Marketplace confirmed says anything about the
	// authorization; anything else may succeed on retry.
	return &command.ServiceCredentialUnavailable{Reason: reason, Temporary: reason == "unavailable"}
}
