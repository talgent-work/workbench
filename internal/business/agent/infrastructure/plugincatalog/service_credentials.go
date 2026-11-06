package plugincatalog

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
)

var _ application.ServiceCredentialPort = (*Client)(nil)

func (c *Client) SupplyServiceCredential(ctx context.Context, input application.ServiceCredentialRequest) (application.ServiceCredentialMaterial, error) {
	if c.credentialToken == "" {
		return application.ServiceCredentialMaterial{}, &application.ServiceCredentialUnavailable{Reason: "unavailable", Temporary: true}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := delegated(ctx, input.OwnerUserID, &marketplaceprivatev1.SupplyServiceCredentialRequest{OwnerUserId: input.OwnerUserID, AuthorizationId: input.AuthorizationID, Target: input.Target})
	if err != nil {
		return application.ServiceCredentialMaterial{}, err
	}
	request.Header().Set("Authorization", "Bearer "+c.credentialToken)
	response, err := c.credentials.SupplyServiceCredential(ctx, request)
	if err != nil {
		return application.ServiceCredentialMaterial{}, credentialUnavailable(err)
	}
	return application.ServiceCredentialMaterial{Token: response.Msg.GetToken(), ExpiresAt: declaredExpiry(response.Msg.GetExpiresAtUnixMs())}, nil
}

func credentialUnavailable(err error) error {
	reason := "unavailable"
	var typed *connect.Error
	if errors.As(err, &typed) {
		if typed.Code() == connect.CodePermissionDenied {
			reason = "access_denied"
		}
		switch reported := typed.Meta().Get("X-Talgent-Credential-Status"); reported {
		case "revoked", "reconnect_required", "incompatible", "access_denied", "unavailable":
			reason = reported
		}
	}
	// Only a reason Marketplace confirmed says anything about the
	// authorization; anything else may succeed on retry.
	return &application.ServiceCredentialUnavailable{Reason: reason, Temporary: reason == "unavailable"}
}
