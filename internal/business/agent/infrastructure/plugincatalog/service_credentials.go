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
	ctx, info, err := delegated(ctx, input.OwnerID)
	if err != nil {
		return command.ServiceCredentialMaterial{}, err
	}
	info.RequestHeader().Set("Authorization", "Bearer "+c.credentialToken)
	response, err := c.credentials.SupplyServiceCredential(ctx, &marketplaceprivatev1.SupplyServiceCredentialRequest{OwnerUserId: input.OwnerID, AuthorizationId: input.AuthorizationID, Target: input.Target})
	if err != nil {
		return command.ServiceCredentialMaterial{}, credentialUnavailable(err, info)
	}
	return command.ServiceCredentialMaterial{Token: response.GetToken(), ExpiresAt: declaredExpiry(response.GetExpiresAtUnixMs())}, nil
}

// credentialUnavailable reads Marketplace's refusal of a call, whose status
// arrives in the call's response metadata.
func credentialUnavailable(err error, info *connect.CallInfo) error {
	reason := "unavailable"
	var typed *connect.Error
	if errors.As(err, &typed) {
		if typed.Code() == connect.CodePermissionDenied {
			reason = "access_denied"
		}
		reported := info.ResponseHeader().Get(credentialStatusHeader)
		if reported == "" {
			reported = info.ResponseTrailer().Get(credentialStatusHeader)
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
