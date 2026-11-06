package plugincatalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"github.com/talgent-work/workbench/pkg/authn"
)

func TestServiceSupplyTransmitsRetainedAuthorityAndCurrentMaterial(t *testing.T) {
	service := &serviceSupplyEndpoint{token: "initial-token"}
	client := newServiceSupplyClient(t, service)
	for _, target := range []string{"github-api", "github-remote-mcp"} {
		input := application.ServiceCredentialRequest{OwnerUserID: "owner", AuthorizationID: "retained", Target: target}
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
			failure := connect.NewError(scenario.code, errors.New("provider-secret"))
			failure.Meta().Set("X-Talgent-Credential-Status", scenario.reported)
			client := newServiceSupplyClient(t, &serviceSupplyEndpoint{failure: failure})
			material, err := client.SupplyServiceCredential(t.Context(), application.ServiceCredentialRequest{OwnerUserID: "owner", AuthorizationID: "retained", Target: "github-api"})
			var unavailable *application.ServiceCredentialUnavailable
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
	mux.Handle(marketplaceprivatev1connect.NewCredentialServiceHandler(service))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "service-secret"})
	require.NoError(t, err)
	return client
}

type serviceSupplyEndpoint struct {
	marketplaceprivatev1connect.UnimplementedCredentialServiceHandler
	received application.ServiceCredentialRequest
	token    string
	failure  error
}

func (s *serviceSupplyEndpoint) SupplyServiceCredential(_ context.Context, r *connect.Request[marketplaceprivatev1.SupplyServiceCredentialRequest]) (*connect.Response[marketplaceprivatev1.SupplyServiceCredentialResponse], error) {
	if r.Header().Get("Authorization") != "Bearer service-secret" || r.Header().Get(authn.HeaderServiceID) != "agent" || r.Header().Get(authn.HeaderDelegatedUserID) != r.Msg.OwnerUserId {
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	}
	s.received = application.ServiceCredentialRequest{OwnerUserID: r.Msg.OwnerUserId, AuthorizationID: r.Msg.AuthorizationId, Target: r.Msg.Target}
	if s.failure != nil {
		return nil, s.failure
	}
	return connect.NewResponse(&marketplaceprivatev1.SupplyServiceCredentialResponse{Token: s.token, ExpiresAtUnixMs: 1900000000000}), nil
}
