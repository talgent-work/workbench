package authn

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

// fakeStreamingHandlerConn is a minimal connect.StreamingHandlerConn for
// exercising the gateway-auth streaming handler path.
type fakeStreamingHandlerConn struct {
	header    http.Header
	procedure string
}

func (f *fakeStreamingHandlerConn) Spec() connect.Spec           { return connect.Spec{Procedure: f.procedure} }
func (f *fakeStreamingHandlerConn) Peer() connect.Peer           { return connect.Peer{} }
func (f *fakeStreamingHandlerConn) Receive(any) error            { return nil }
func (f *fakeStreamingHandlerConn) RequestHeader() http.Header   { return f.header }
func (f *fakeStreamingHandlerConn) Send(any) error               { return nil }
func (f *fakeStreamingHandlerConn) ResponseHeader() http.Header  { return http.Header{} }
func (f *fakeStreamingHandlerConn) ResponseTrailer() http.Header { return http.Header{} }

// Protected streaming RPCs must receive the gateway user in context, or
// resource authorization that reads CurrentUserID can never succeed.
func TestGatewayStreamingHandlerInjectsUser(t *testing.T) {
	interceptor := NewConnectUnaryInterceptor(GatewayActorConfig{Enabled: true})
	header := http.Header{}
	header.Set(HeaderAuthenticatedBy, "envoy")
	header.Set(HeaderSubject, " 2c857667-a533-5efc-b6da-fc9cce51d31b ")
	conn := &fakeStreamingHandlerConn{header: header, procedure: "/talgent.streamgateway.public.v1.StreamGatewayService/ProtectedStream"}

	var gotID string
	var gotOK bool
	next := func(ctx context.Context, _ connect.StreamingHandlerConn) error {
		gotID, gotOK = CurrentUserID(ctx)
		return nil
	}

	require.NoError(t, interceptor.WrapStreamingHandler(next)(context.Background(), conn))
	require.True(t, gotOK)
	require.Equal(t, "2c857667-a533-5efc-b6da-fc9cce51d31b", gotID)
}

// Excluded streaming procedures, such as WatchStream, keep their
// unauthenticated posture, passing through without trusted headers.
func TestGatewayStreamingHandlerSkipsExcluded(t *testing.T) {
	interceptor := NewConnectUnaryInterceptor(GatewayActorConfig{
		Enabled:            true,
		ExcludedProcedures: []string{"/talgent.streamgateway.public.v1.StreamGatewayService/WatchStream"},
	})
	conn := &fakeStreamingHandlerConn{header: http.Header{}, procedure: "/talgent.streamgateway.public.v1.StreamGatewayService/WatchStream"}

	called := false
	next := func(context.Context, connect.StreamingHandlerConn) error { called = true; return nil }

	require.NoError(t, interceptor.WrapStreamingHandler(next)(context.Background(), conn))
	require.True(t, called)
}

// A protected streaming RPC without the trusted gateway marker must fail closed.
func TestGatewayStreamingHandlerRequiresTrustedMarker(t *testing.T) {
	interceptor := NewConnectUnaryInterceptor(GatewayActorConfig{Enabled: true})
	conn := &fakeStreamingHandlerConn{header: http.Header{}, procedure: "/talgent.streamgateway.public.v1.StreamGatewayService/ProtectedStream"}

	err := interceptor.WrapStreamingHandler(func(context.Context, connect.StreamingHandlerConn) error {
		return nil
	})(context.Background(), conn)

	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
