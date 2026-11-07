package plugincatalog_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func TestCredentialSupplyRetainsOwnerAuthorizationAndTarget(t *testing.T) {
	service := &credentialService{}
	mux := http.NewServeMux()
	mux.Handle(marketplaceprivatev1connect.NewCredentialServiceHandler(service))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "service-secret"})
	require.NoError(t, err)
	input := application.RepositoryCredentialRequest{OwnerUserID: "owner", AuthorizationID: "retained-authorization", RepositoryID: "123", CloneURL: "https://github.com/acme/project.git"}
	material, err := client.SupplyRepositoryCredential(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, input, service.received)
	require.Equal(t, "provider-secret", material.Password)
	require.Equal(t, time.UnixMilli(1900000000000), material.ExpiresAt)
	service.reject = true
	material, err = client.SupplyRepositoryCredential(t.Context(), input)
	var unavailable *application.ServiceCredentialUnavailable
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, "access_denied", unavailable.Reason)
	require.Empty(t, material.Password)
}

func TestCredentialSupplyDoesNotFollowRedirect(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(target.Close)
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+r.URL.Path, status)
		}))
		client, err := plugincatalog.NewClient(source.Client(), plugincatalog.Config{Address: source.URL, CredentialToken: "service-secret"})
		require.NoError(t, err)
		material, err := client.SupplyRepositoryCredential(t.Context(), application.RepositoryCredentialRequest{OwnerUserID: "owner", AuthorizationID: "retained"})
		source.Close()
		require.Error(t, err)
		require.Empty(t, material.Password)
	}
	require.Zero(t, forwarded.Load(), "redirects must not forward trusted service credentials or delegation")
}

type credentialService struct {
	marketplaceprivatev1connect.UnimplementedCredentialServiceHandler
	received application.RepositoryCredentialRequest
	reject   bool
}

func (s *credentialService) SupplyRepositoryCredential(_ context.Context, r *connect.Request[marketplaceprivatev1.SupplyRepositoryCredentialRequest]) (*connect.Response[marketplaceprivatev1.SupplyRepositoryCredentialResponse], error) {
	if s.reject || r.Header().Get("Authorization") != "Bearer service-secret" || r.Header().Get(authn.HeaderServiceID) != "agent" || r.Header().Get(authn.HeaderDelegatedUserID) != "owner" {
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	}
	s.received = application.RepositoryCredentialRequest{OwnerUserID: r.Msg.OwnerUserId, AuthorizationID: r.Msg.AuthorizationId, RepositoryID: r.Msg.RepositoryId, CloneURL: r.Msg.CloneUrl}
	return connect.NewResponse(&marketplaceprivatev1.SupplyRepositoryCredentialResponse{Username: "x-access-token", Password: "provider-secret", ExpiresAtUnixMs: 1900000000000}), nil
}
