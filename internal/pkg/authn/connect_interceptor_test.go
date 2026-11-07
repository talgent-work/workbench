package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
)

func TestGatewayUserInterceptorResolvesSubjectAsUserID(t *testing.T) {
	// Trusted gateway claims must become the user id used by product
	// authorization; request bodies must not choose the actor.
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true})
	req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	req.Header().Set(HeaderAuthenticatedBy, "envoy")
	req.Header().Set(HeaderIssuer, " https://auth.example.com ")
	req.Header().Set(HeaderSubject, " 2c857667-a533-5efc-b6da-fc9cce51d31b ")
	req.Header().Set(HeaderEmail, "jace@example.com")
	req.Header().Set(HeaderDisplayName, "Jace")
	req.Header().Set(HeaderAvatarURL, "https://example.com/avatar.png")
	req.Header().Set(HeaderRoles, "admin, viewer,")

	resp, err := client.GetWork(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "2c857667-a533-5efc-b6da-fc9cce51d31b", resp.Msg.GetWork().GetWorkId())
}

func TestGatewayUserInterceptorRejectsBearerWithoutSubjectHeader(t *testing.T) {
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true})
	req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	req.Header().Set(HeaderAuthenticatedBy, "envoy")
	req.Header().Set("Authorization", "Bearer eyJhbGciOiJub25lIn0.eyJzdWIiOiJzdWItMSJ9.") // gitleaks:allow (an unsigned fixture token)

	_, err := client.GetWork(context.Background(), req)

	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGatewayUserInterceptorRequiresTrustedGatewayMarker(t *testing.T) {
	// Protected browser calls must fail closed unless the trusted gateway marks
	// the request as already authenticated.
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true})
	req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	req.Header().Set(HeaderIssuer, "https://auth.example.com")
	req.Header().Set(HeaderSubject, "sub-1")

	_, err := client.GetWork(context.Background(), req)

	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGatewayUserInterceptorRequiresOIDCSubject(t *testing.T) {
	// Envoy-authenticated requests without a subject cannot produce a stable
	// user id, so they must not reach protected handlers.
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true})

	for name, headers := range map[string]map[string]string{
		"missing subject": {
			HeaderAuthenticatedBy: "envoy",
			HeaderIssuer:          "https://auth.example.com",
		},
		"legacy string subject": {
			HeaderAuthenticatedBy: "envoy",
			HeaderIssuer:          "https://auth.example.com",
			HeaderSubject:         "sub-1",
		},
		"composite subject": {
			HeaderAuthenticatedBy: "envoy",
			HeaderIssuer:          "https://auth.example.com",
			HeaderSubject:         "oidc:https://auth.example.com#2c857667-a533-5efc-b6da-fc9cce51d31b",
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
			for key, value := range headers {
				req.Header().Set(key, value)
			}

			_, err := client.GetWork(context.Background(), req)

			require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		})
	}
}

func TestGatewayUserInterceptorAcceptsDelegatedInternalUser(t *testing.T) {
	// Internal service calls carry service identity plus delegated user context,
	// preserving the browser user through service-to-service hops.
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true})
	req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	req.Header().Set(HeaderServiceID, "work")
	req.Header().Set(HeaderDelegatedUserID, "sub-1")

	resp, err := client.GetWork(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "sub-1", resp.Msg.GetWork().GetWorkId())
}

func TestGatewayUserInterceptorRejectsSpoofedDelegatedUser(t *testing.T) {
	// Public clients must not be able to authenticate by sending only the
	// delegated-user header; a service identity is required.
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true})
	req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	req.Header().Set(HeaderDelegatedUserID, "sub-1")

	_, err := client.GetWork(context.Background(), req)

	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGatewayUserInterceptorAllowsExcludedServiceOnlyProcedure(t *testing.T) {
	// Intent (real): selected internal service-only procedures perform their own
	// service identity check and must not be rejected before reaching the handler.
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{
		Enabled:            true,
		ExcludedProcedures: []string{agentpublicv1connect.AgentWorkServiceGetWorkProcedure},
	})
	req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	req.Header().Set(HeaderServiceID, "caller")

	resp, err := client.GetWork(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "anonymous", resp.Msg.GetWork().GetWorkId())
}

func TestGatewayUserInterceptorRejectsInvalidDelegatedUserID(t *testing.T) {
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true})

	for name, userID := range map[string]string{
		"empty":           "",
		"prefixed user":   "oidc:https://auth.example.com#sub-1",
		"contains marker": "issuer#sub-1",
	} {
		t.Run(name, func(t *testing.T) {
			req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
			req.Header().Set(HeaderServiceID, "work")
			req.Header().Set(HeaderDelegatedUserID, userID)

			_, err := client.GetWork(context.Background(), req)

			require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		})
	}
}

func newGatewayAuthnTestClient(t *testing.T, cfg GatewayActorConfig) agentpublicv1connect.AgentWorkServiceClient {
	t.Helper()

	mux := http.NewServeMux()
	path, handler := agentpublicv1connect.NewAgentWorkServiceHandler(
		&authnTestAgentWorkService{},
		connect.WithInterceptors(NewConnectUnaryInterceptor(cfg)),
	)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return agentpublicv1connect.NewAgentWorkServiceClient(server.Client(), server.URL)
}

type authnTestAgentWorkService struct {
	agentpublicv1connect.UnimplementedAgentWorkServiceHandler
}

func (s *authnTestAgentWorkService) GetWork(ctx context.Context, _ *connect.Request[agentpublicv1.GetWorkRequest]) (*connect.Response[agentpublicv1.GetWorkResponse], error) {
	userID, ok := CurrentUserID(ctx)
	if !ok {
		userID = "anonymous"
	}
	return connect.NewResponse(&agentpublicv1.GetWorkResponse{
		Work: &agentpublicv1.WorkInfo{WorkId: userID},
	}), nil
}

type nativeVerifierFixture struct {
	reject bool
	calls  int
}

func (v *nativeVerifierFixture) VerifyNativeProcess(_ context.Context, workID, credential string) (NativeProcess, error) {
	v.calls++
	if v.reject || workID != "work" || credential != "credential" {
		return NativeProcess{}, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return NativeProcess{SubjectID: "native-owner", ProjectID: "project"}, nil
}

func TestNativeCredentialOverridesCallerClaimedUser(t *testing.T) {
	verifier := &nativeVerifierFixture{}
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true, NativeProcessVerifier: verifier, NativeProcedures: []string{agentpublicv1connect.AgentWorkServiceGetWorkProcedure}})
	req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	req.Header().Set(HeaderNativeWorkID, "work")
	req.Header().Set(HeaderNativeCredential, "credential")
	req.Header().Set(HeaderServiceID, "executor")
	req.Header().Set(HeaderDelegatedUserID, "spoofed-owner")
	response, err := client.GetWork(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "native-owner", response.Msg.GetWork().GetWorkId())
	verifier.reject = true
	_, err = client.GetWork(context.Background(), req)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "rejected proof must not fall through to service claims")
}

func TestIncompleteNativeCredentialCannotUseGatewayIdentity(t *testing.T) {
	verifier := &nativeVerifierFixture{}
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true, NativeProcessVerifier: verifier, NativeProcedures: []string{agentpublicv1connect.AgentWorkServiceGetWorkProcedure}})
	req := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	req.Header().Set(HeaderNativeWorkID, "work")
	req.Header().Set(HeaderAuthenticatedBy, "envoy")
	req.Header().Set(HeaderSubject, "2c857667-a533-5efc-b6da-fc9cce51d31b")
	_, err := client.GetWork(context.Background(), req)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Zero(t, verifier.calls)
}

func TestNativeCredentialsCannotEnterUnlistedProcedures(t *testing.T) {
	verifier := &nativeVerifierFixture{}
	client := newGatewayAuthnTestClient(t, GatewayActorConfig{Enabled: true, NativeProcessVerifier: verifier})
	request := connect.NewRequest(&agentpublicv1.GetWorkRequest{})
	request.Header().Set(HeaderNativeWorkID, "work")
	request.Header().Set(HeaderNativeCredential, "credential")
	request.Header().Set(HeaderAuthenticatedBy, "envoy")
	request.Header().Set(HeaderSubject, "2c857667-a533-5efc-b6da-fc9cce51d31b")
	_, err := client.GetWork(context.Background(), request)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "valid native proof must not become a general user session")
}
