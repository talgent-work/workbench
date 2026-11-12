package routing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/peer"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
)

func TestNativeExecutionRoutingPreservesScopeAndPrivateAuthentication(t *testing.T) {
	ctx := context.Background()
	calls := make(chan call[dispatcherprivatev1.StopRequest], 1)
	ownerLocal := &nativeRouteReceiver{cancel: make(chan call[dispatcherprivatev1.CancelTurnRequest], 1), turn: make(chan call[dispatcherprivatev1.GetCurrentTurnRequest], 1), calls: calls, pending: make(chan call[dispatcherprivatev1.ListPendingInteractionsRequest], 1)}
	ownerHandler := NewNativeExecutionHandler(ownerLocal, &nativeRouteDirectory{owner: command.ExecutionOwner{Instance: "owner", Alive: true}}, "owner", "shared-control", peer.NewNativeClients(http.DefaultClient))
	owner := serveNativeRoute(t, ownerHandler)
	lookups := make(chan string, 4)
	ingressHandler := NewNativeExecutionHandler(&nativeRouteReceiver{calls: make(chan call[dispatcherprivatev1.StopRequest], 1)}, &nativeRouteDirectory{owner: command.ExecutionOwner{Instance: "owner", Addr: strings.TrimPrefix(owner.URL, "http://"), Alive: true}, lookups: lookups}, "ingress", "shared-control", peer.NewNativeClients(http.DefaultClient))
	ingress := serveNativeRoute(t, ingressHandler)
	client := dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(ingress.Client(), ingress.URL)))
	message := &dispatcherprivatev1.StopRequest{WorkId: "work-a", StopRequestId: "stop-c"}
	_, err := client.Stop(ctx, message)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Empty(t, lookups, "unauthenticated calls must not disclose or consult ownership")
	const traceparent = "00-11111111111111111111111111111111-2222222222222222-01"
	_, err = client.Stop(withHeaders(ctx, "Authorization", "Bearer shared-control", "Traceparent", traceparent), message)
	require.NoError(t, err)
	require.Equal(t, "work-a", <-lookups)
	got := <-calls
	require.Equal(t, "work-a", got.request.WorkId)
	require.Equal(t, "stop-c", got.request.StopRequestId)
	require.Equal(t, "Bearer shared-control", got.header.Get("Authorization"))
	require.Equal(t, "1", got.header.Get(PeerHopHeader))
	require.Equal(t, traceparent, got.header.Get("Traceparent"))
	pendingRequest := &dispatcherprivatev1.ListPendingInteractionsRequest{WorkId: message.WorkId}
	_, err = client.ListPendingInteractions(ctx, pendingRequest)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	pendingResponse, err := client.ListPendingInteractions(withHeaders(ctx, "Authorization", "Bearer shared-control"), pendingRequest)
	require.NoError(t, err)
	require.Equal(t, "work-a", <-lookups)
	pendingCall := <-ownerLocal.pending
	require.Equal(t, "work-a", pendingCall.request.WorkId)
	require.Equal(t, "Bearer shared-control", pendingCall.header.Get("Authorization"))
	require.Equal(t, "1", pendingCall.header.Get(PeerHopHeader))
	require.Equal(t, "permission", pendingResponse.Interactions[0].Id)
	turn, err := client.GetCurrentTurn(withHeaders(ctx, "Authorization", "Bearer shared-control"), &dispatcherprivatev1.GetCurrentTurnRequest{WorkId: message.WorkId})
	require.NoError(t, err)
	require.Equal(t, "work-a", <-lookups)
	require.Equal(t, "turn-d", turn.TurnId)
	require.Equal(t, "1", (<-ownerLocal.turn).header.Get(PeerHopHeader))
	cancelRequest := &dispatcherprivatev1.CancelTurnRequest{WorkId: message.WorkId, TurnId: turn.TurnId}
	_, err = client.CancelTurn(ctx, cancelRequest)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.CancelTurn(withHeaders(ctx, "Authorization", "Bearer shared-control"), cancelRequest)
	require.NoError(t, err)
	require.Equal(t, "work-a", <-lookups)
	cancellation := <-ownerLocal.cancel
	require.Equal(t, "turn-d", cancellation.request.TurnId)
	require.Equal(t, "work-a", cancellation.request.WorkId)
	require.Equal(t, "Bearer shared-control", cancellation.header.Get("Authorization"))
	require.Equal(t, "1", cancellation.header.Get(PeerHopHeader))
	_, err = client.Stop(withHeaders(ctx, "Authorization", "Bearer shared-control", "Traceparent", traceparent, PeerHopHeader, "1"), message)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Empty(t, calls, "a second peer hop must not execute the command")
}

// An ownership lookup that ends because its caller went away or ran out of
// time answers with that cause, not Unavailable: nothing is wrong with the
// directory. Any other lookup failure stays Unavailable.
func TestNativeExecutionRoutingAnswersALookupEndedByItsCallerWithItsCause(t *testing.T) {
	for cause, code := range map[error]connect.Code{
		context.Canceled:             connect.CodeCanceled,
		context.DeadlineExceeded:     connect.CodeDeadlineExceeded,
		errors.New("redis: refused"): connect.CodeUnavailable,
	} {
		handler := NewNativeExecutionHandler(&nativeRouteReceiver{}, &nativeRouteDirectory{err: fmt.Errorf("locate: %w", cause)}, "ingress", "shared-control", peer.NewNativeClients(http.DefaultClient))
		server := serveNativeRoute(t, handler)
		client := dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
		_, err := client.GetCurrentTurn(withHeaders(context.Background(), "Authorization", "Bearer shared-control"), &dispatcherprivatev1.GetCurrentTurnRequest{WorkId: "work-a"})
		require.Equal(t, code, connect.CodeOf(err), cause.Error())
	}
}

// withHeaders is the context of a call carrying the given header pairs.
func withHeaders(ctx context.Context, pairs ...string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	for i := 0; i+1 < len(pairs); i += 2 {
		info.RequestHeader().Set(pairs[i], pairs[i+1])
	}
	return ctx
}

// call is a request a receiver served, with its metadata.
type call[T any] struct {
	request *T
	header  *connect.Header
}

func received[T any](ctx context.Context, request *T) call[T] {
	info, _ := connect.CallInfoForServerContext(ctx)
	return call[T]{request: request, header: info.RequestHeader()}
}

type nativeRouteDirectory struct {
	owner   command.ExecutionOwner
	lookups chan string
	err     error
}

func (d *nativeRouteDirectory) Locate(_ context.Context, key string) (command.ExecutionOwner, bool, error) {
	if d.lookups != nil {
		d.lookups <- key
	}
	if d.err != nil {
		return command.ExecutionOwner{}, false, d.err
	}
	return d.owner, true, nil
}

type nativeRouteReceiver struct {
	cancel chan call[dispatcherprivatev1.CancelTurnRequest]
	turn   chan call[dispatcherprivatev1.GetCurrentTurnRequest]
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	calls   chan call[dispatcherprivatev1.StopRequest]
	pending chan call[dispatcherprivatev1.ListPendingInteractionsRequest]
}

func (r *nativeRouteReceiver) Stop(ctx context.Context, request *dispatcherprivatev1.StopRequest) (*dispatcherprivatev1.StopResponse, error) {
	r.calls <- received(ctx, request)
	return &dispatcherprivatev1.StopResponse{}, nil
}
func serveNativeRoute(t *testing.T, handler dispatcherprivatev1connect.ExecutionServiceHandler) *httptest.Server {
	t.Helper()
	path, h := "/"+dispatcherprivatev1connect.ExecutionServiceName+"/", connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, handler) })
	mux := http.NewServeMux()
	mux.Handle(path, h)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func (r *nativeRouteReceiver) ListPendingInteractions(ctx context.Context, request *dispatcherprivatev1.ListPendingInteractionsRequest) (*dispatcherprivatev1.ListPendingInteractionsResponse, error) {
	if r.pending == nil {
		return nil, connect.NewError(connect.CodeUnavailable, ErrPeerHopExceeded.Error()).WithCause(ErrPeerHopExceeded)
	}
	r.pending <- received(ctx, request)
	return &dispatcherprivatev1.ListPendingInteractionsResponse{Interactions: []*dispatcherpublicv1.PendingInteraction{{Id: "permission"}}}, nil
}

func (r *nativeRouteReceiver) CancelTurn(ctx context.Context, request *dispatcherprivatev1.CancelTurnRequest) (*dispatcherprivatev1.CancelTurnResponse, error) {
	r.cancel <- received(ctx, request)
	return &dispatcherprivatev1.CancelTurnResponse{}, nil
}
func (r *nativeRouteReceiver) GetCurrentTurn(ctx context.Context, request *dispatcherprivatev1.GetCurrentTurnRequest) (*dispatcherprivatev1.GetCurrentTurnResponse, error) {
	r.turn <- received(ctx, request)
	return &dispatcherprivatev1.GetCurrentTurnResponse{TurnId: "turn-d"}, nil
}
