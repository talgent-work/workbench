package github

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func TestServiceCredentialHTTPChecksAuthorityTargetAndCurrentGrant(t *testing.T) {
	protected := mustTokenCipher(t)
	ciphertext, err := protected.Encrypt("first-provider-token")
	require.NoError(t, err)
	authorization := mustAuthorization(t, "owner", ciphertext, time.Now().Add(time.Hour))
	repo := &fakeAuthorizationRepository{authorization: authorization}
	supplier, err := NewCredentialSupplier(enabledConfig(t), repo, protected)
	require.NoError(t, err)
	app := application.NewPersonalConnections(repo, nil, nil, nil, supplier, nil, nil, supplier, nil)
	mux := http.NewServeMux()
	mux.Handle(marketplaceprivatev1connect.NewCredentialServiceHandler(transport.NewCredentialHandler(app, "control-secret")))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := marketplaceprivatev1connect.NewCredentialServiceClient(server.Client(), server.URL)
	request := func(target string) *connect.Request[marketplaceprivatev1.SupplyServiceCredentialRequest] {
		r := connect.NewRequest(&marketplaceprivatev1.SupplyServiceCredentialRequest{OwnerUserId: "owner", AuthorizationId: authorization.ID, Target: target})
		r.Header().Set("Authorization", "Bearer control-secret")
		return r
	}
	for _, target := range []string{"github-api", "github-remote-mcp"} {
		response, err := client.SupplyServiceCredential(t.Context(), request(target))
		require.NoError(t, err)
		require.Equal(t, "first-provider-token", response.Msg.Token)
	}
	for _, scenario := range []string{"wrong consumer", "native workload", "foreign owner", "unsupported target"} {
		r := request("github-api")
		switch scenario {
		case "wrong consumer":
			r.Header().Set("Authorization", "Bearer wrong")
		case "native workload":
			r.Header().Set(authn.HeaderNativeWorkID, "work")
		case "foreign owner":
			r.Msg.OwnerUserId = "foreign"
		case "unsupported target":
			r.Msg.Target = "https://other.example/mcp"
		}
		response, err := client.SupplyServiceCredential(t.Context(), r)
		require.Error(t, err, scenario)
		require.Nil(t, response)
		require.NotContains(t, err.Error(), "first-provider-token")
	}
	var revocation *connect.Error
	require.NoError(t, repo.authorization.MarkRevoked(time.Now()))
	_, err = client.SupplyServiceCredential(t.Context(), request("github-api"))
	require.ErrorAs(t, err, &revocation)
	require.Equal(t, "revoked", revocation.Meta().Get("X-Talgent-Credential-Status"))
	// Reauthorization restores supply through the same stored authorization ID.
	ciphertext, err = protected.Encrypt("replacement-provider-token")
	require.NoError(t, err)
	require.NoError(t, repo.authorization.ReplaceWith(domain.ProviderAuthorizationInput{OwnerUserID: "owner", Provider: domain.ProviderGitHub,
		ProviderAccountID: "new-account", ProviderLogin: "new-login", AccessTokenCiphertext: ciphertext, ExpiresAt: time.Now().Add(time.Hour)}))
	response, err := client.SupplyServiceCredential(t.Context(), request("github-api"))
	require.NoError(t, err)
	require.Equal(t, "replacement-provider-token", response.Msg.Token)
}

func TestServiceCredentialRejectsUnverifiedEnterpriseContract(t *testing.T) {
	cfg := enabledConfig(t)
	cfg.APIBaseURL = "https://github.example/api/v3"
	cipher := &passthroughTokenCipher{}
	supplier, err := NewCredentialSupplier(cfg, &fakeAuthorizationRepository{authorization: mustAuthorization(t, "owner", "token", time.Now().Add(time.Hour))}, cipher)
	require.NoError(t, err)
	_, err = supplier.SupplyServiceCredential(t.Context(), "owner", "authorization-1", "github-api")
	require.ErrorIs(t, err, domain.ErrCredentialTargetInapplicable)
	require.Zero(t, cipher.decryptCalls)
}
