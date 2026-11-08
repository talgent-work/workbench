package plugincatalog_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func TestServiceSupplyTransmitsRetainedAuthorityAndCurrentMaterial(t *testing.T) {
	service := &serviceSupplyEndpoint{token: "initial-token"}
	client := newServiceSupplyClient(t, service)
	for _, target := range []string{"github-api", "github-remote-mcp"} {
		input := command.ServiceCredentialRequest{OwnerUserID: "owner", AuthorizationID: "retained", Target: target}
		for _, token := range []string{"initial-token", "rotated-token"} {
			service.token = token
			material, err := client.SupplyServiceCredential(t.Context(), input)
			require.NoError(t, err)
			require.Equal(t, token, material.Token)
			require.Equal(t, time.UnixMilli(1900000000000), material.ExpiresAt)
			require.Equal(t, input, service.received)
		}
	}
}

func TestServiceSupplyLimitsFailureFeedbackToKnownReasons(t *testing.T) {
	for _, scenario := range []struct {
		reported, expected string
		code               connect.Code
	}{
		{"revoked", "revoked", connect.CodeFailedPrecondition},
		{"reconnect_required", "reconnect_required", connect.CodeFailedPrecondition},
		{"incompatible", "incompatible", connect.CodeFailedPrecondition},
		{"access_denied", "access_denied", connect.CodeFailedPrecondition},
		{"", "access_denied", connect.CodePermissionDenied},
		{"provider-secret", "unavailable", connect.CodeInternal},
	} {
		t.Run(scenario.expected+"/"+scenario.code.String(), func(t *testing.T) {
			failure := connect.NewError(scenario.code, "provider-secret")
			client := newServiceSupplyClient(t, &serviceSupplyEndpoint{failure: failure, status: scenario.reported})
			material, err := client.SupplyServiceCredential(t.Context(), command.ServiceCredentialRequest{OwnerUserID: "owner", AuthorizationID: "retained", Target: "github-api"})
			var unavailable *command.ServiceCredentialUnavailable
			require.ErrorAs(t, err, &unavailable)
			require.Equal(t, scenario.expected, unavailable.Reason)
			require.Equal(t, scenario.expected == "unavailable", unavailable.Temporary, "only a reason Marketplace confirmed is not temporary")
			require.NotContains(t, err.Error(), "provider-secret")
			require.Empty(t, material)
		})
	}
}

func newServiceSupplyClient(t *testing.T, service *serviceSupplyEndpoint) *plugincatalog.Client {
	t.Helper()
	mux := http.NewServeMux()
	srv := connect.NewServer()
	marketplaceprivatev1connect.RegisterCredentialServiceHandler(srv, service)
	connecthttp.Mount(mux, srv, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "service-secret"})
	require.NoError(t, err)
	return client
}

type serviceSupplyEndpoint struct {
	marketplaceprivatev1connect.UnimplementedCredentialServiceHandler
	received command.ServiceCredentialRequest
	token    string
	failure  error
	// status is the credential status Marketplace states with failure.
	status string
}

func (s *serviceSupplyEndpoint) SupplyServiceCredential(ctx context.Context, r *marketplaceprivatev1.SupplyServiceCredentialRequest) (*marketplaceprivatev1.SupplyServiceCredentialResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if info.RequestHeader().Get("Authorization") != "Bearer service-secret" || info.RequestHeader().Get(authn.HeaderServiceID) != "agent" || info.RequestHeader().Get(authn.HeaderDelegatedUserID) != r.OwnerUserId {
		return nil, connect.NewError(connect.CodePermissionDenied, "")
	}
	s.received = command.ServiceCredentialRequest{OwnerUserID: r.OwnerUserId, AuthorizationID: r.AuthorizationId, Target: r.Target}
	if s.failure != nil {
		if s.status != "" {
			info.ResponseHeader().Set("X-Talgent-Credential-Status", s.status)
		}
		return nil, s.failure
	}
	return &marketplaceprivatev1.SupplyServiceCredentialResponse{Token: s.token, ExpiresAtUnixMs: 1900000000000}, nil
}
