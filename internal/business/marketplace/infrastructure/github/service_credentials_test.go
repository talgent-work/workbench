package github

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
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
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
	srv := connect.NewServer()
	marketplaceprivatev1connect.RegisterCredentialServiceHandler(srv, transport.NewCredentialHandler(app, "control-secret"))
	connecthttp.Mount(mux, srv)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := marketplaceprivatev1connect.NewCredentialServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	request := func(target string) *marketplaceprivatev1.SupplyServiceCredentialRequest {
		return &marketplaceprivatev1.SupplyServiceCredentialRequest{OwnerUserId: "owner", AuthorizationId: authorization.ID, Target: target}
	}
	// consumer is the context of a call by the trusted credential consumer,
	// with any further header pairs.
	consumer := func(pairs ...string) (context.Context, *connect.CallInfo) {
		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set("Authorization", "Bearer control-secret")
		for i := 0; i+1 < len(pairs); i += 2 {
			info.RequestHeader().Set(pairs[i], pairs[i+1])
		}
		return ctx, info
	}
	for _, target := range []string{"github-api", "github-remote-mcp"} {
		ctx, _ := consumer()
		response, err := client.SupplyServiceCredential(ctx, request(target))
		require.NoError(t, err)
		require.Equal(t, "first-provider-token", response.Token)
	}
	for _, scenario := range []string{"wrong consumer", "foreign owner", "unsupported target"} {
		r := request("github-api")
		ctx, _ := consumer()
		switch scenario {
		case "wrong consumer":
			ctx, _ = consumer("Authorization", "Bearer wrong")
		case "foreign owner":
			r.OwnerUserId = "foreign"
		case "unsupported target":
			r.Target = "https://other.example/mcp"
		}
		response, err := client.SupplyServiceCredential(ctx, r)
		require.Error(t, err, scenario)
		require.Nil(t, response)
		require.NotContains(t, err.Error(), "first-provider-token")
	}
	var revocation *connect.Error
	require.NoError(t, repo.authorization.MarkRevoked(time.Now()))
	ctx, info := consumer()
	_, err = client.SupplyServiceCredential(ctx, request("github-api"))
	require.ErrorAs(t, err, &revocation)
	require.Equal(t, "revoked", info.ResponseHeader().Get(transport.CredentialStatusHeader))
	// Reauthorization restores supply through the same stored authorization ID.
	ciphertext, err = protected.Encrypt("replacement-provider-token")
	require.NoError(t, err)
	require.NoError(t, repo.authorization.ReplaceWith(domain.ProviderAuthorizationInput{OwnerUserID: "owner", Provider: domain.ProviderGitHub,
		ProviderAccountID: "new-account", ProviderLogin: "new-login", AccessTokenCiphertext: ciphertext, ExpiresAt: time.Now().Add(time.Hour)}))
	ctx, _ = consumer()
	response, err := client.SupplyServiceCredential(ctx, request("github-api"))
	require.NoError(t, err)
	require.Equal(t, "replacement-provider-token", response.Token)
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
