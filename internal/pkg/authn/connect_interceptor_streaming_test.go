package authn

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
)

// noStream is a server stream the handlers under test never use.
type noStream struct{}

func (noStream) Receive(any) error  { return nil }
func (noStream) SendHeaders() error { return nil }
func (noStream) Send(any) error     { return nil }

// callStream dispatches a server-streaming call of procedure with header
// through the interceptor to next.
func callStream(t *testing.T, interceptor connect.ServerInterceptor, procedure string, header map[string]string, next func(context.Context) error) error {
	t.Helper()
	server := connect.NewServer(interceptor)
	server.Register(connect.Method{
		Spec: connect.Spec{StreamType: connect.StreamTypeServer, Procedure: procedure},
		Handler: func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
			return next(ctx)
		},
	})
	info := new(connect.CallInfo)
	for key, value := range header {
		info.RequestHeader().Set(key, value)
	}
	return server.Call(context.Background(), procedure, info, noStream{})
}

// Protected streaming RPCs must receive the gateway user in context, or
// resource authorization that reads CurrentUserID can never succeed.
func TestGatewayStreamingHandlerInjectsUser(t *testing.T) {
	interceptor := NewConnectUnaryInterceptor(GatewayActorConfig{Enabled: true})
	header := map[string]string{HeaderAuthenticatedBy: "envoy", HeaderSubject: " 2c857667-a533-5efc-b6da-fc9cce51d31b "}

	var gotID string
	var gotOK bool
	next := func(ctx context.Context) error {
		gotID, gotOK = CurrentUserID(ctx)
		return nil
	}

	require.NoError(t, callStream(t, interceptor, "/streamgateway.public.v1.StreamGatewayService/ProtectedStream", header, next))
	require.True(t, gotOK)
	require.Equal(t, "2c857667-a533-5efc-b6da-fc9cce51d31b", gotID)
}

// Excluded streaming procedures, such as WatchStream, keep their
// unauthenticated posture, passing through without trusted headers.
func TestGatewayStreamingHandlerSkipsExcluded(t *testing.T) {
	interceptor := NewConnectUnaryInterceptor(GatewayActorConfig{
		Enabled:            true,
		ExcludedProcedures: []string{"/streamgateway.public.v1.StreamGatewayService/WatchStream"},
	})

	called := false
	next := func(context.Context) error { called = true; return nil }

	require.NoError(t, callStream(t, interceptor, "/streamgateway.public.v1.StreamGatewayService/WatchStream", nil, next))
	require.True(t, called)
}

// A protected streaming RPC without the trusted gateway marker must fail closed.
func TestGatewayStreamingHandlerRequiresTrustedMarker(t *testing.T) {
	interceptor := NewConnectUnaryInterceptor(GatewayActorConfig{Enabled: true})

	err := callStream(t, interceptor, "/streamgateway.public.v1.StreamGatewayService/ProtectedStream", nil, func(context.Context) error {
		return nil
	})

	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// A Work's native process reaches unary procedures only.
func TestGatewayStreamingHandlerRefusesNativeCredentials(t *testing.T) {
	interceptor := NewConnectUnaryInterceptor(GatewayActorConfig{Enabled: true})
	header := map[string]string{HeaderNativeWorkID: "work", HeaderNativeCredential: "credential"}

	err := callStream(t, interceptor, "/streamgateway.public.v1.StreamGatewayService/ProtectedStream", header, func(context.Context) error {
		return nil
	})

	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
