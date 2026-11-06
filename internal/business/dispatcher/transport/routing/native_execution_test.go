package routing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/peer"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

func TestNativeExecutionRoutingPreservesScopeAndPrivateAuthentication(t *testing.T) {
	ctx := context.Background()
	calls := make(chan *connect.Request[dispatcherprivatev1.StopRequest], 1)
	ownerLocal := &nativeRouteReceiver{cancel: make(chan *connect.Request[dispatcherprivatev1.CancelTurnRequest], 1), turn: make(chan *connect.Request[dispatcherprivatev1.GetCurrentTurnRequest], 1), calls: calls, pending: make(chan *connect.Request[dispatcherprivatev1.ListPendingInteractionsRequest], 1)}
	ownerHandler := NewNativeExecutionHandler(ownerLocal, &nativeRouteDirectory{owner: command.ExecutionOwner{Instance: "owner", Alive: true}}, "owner", "shared-control", peer.NewNativeClients(http.DefaultClient))
	owner := serveNativeRoute(t, ownerHandler)
	lookups := make(chan string, 4)
	ingressHandler := NewNativeExecutionHandler(&nativeRouteReceiver{calls: make(chan *connect.Request[dispatcherprivatev1.StopRequest], 1)}, &nativeRouteDirectory{owner: command.ExecutionOwner{Instance: "owner", Addr: strings.TrimPrefix(owner.URL, "http://"), Alive: true}, lookups: lookups}, "ingress", "shared-control", peer.NewNativeClients(http.DefaultClient))
	ingress := serveNativeRoute(t, ingressHandler)
	client := dispatcherprivatev1connect.NewExecutionServiceClient(ingress.Client(), ingress.URL)
	message := &dispatcherprivatev1.StopRequest{WorkId: "work-a", StopRequestId: "stop-c"}
	_, err := client.Stop(ctx, connect.NewRequest(message))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Empty(t, lookups, "unauthenticated calls must not disclose or consult ownership")
	req := connect.NewRequest(message)
	req.Header().Set("Authorization", "Bearer shared-control")
	req.Header().Set("Traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	_, err = client.Stop(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "work-a", <-lookups)
	got := <-calls
	require.Equal(t, "work-a", got.Msg.WorkId)
	require.Equal(t, "stop-c", got.Msg.StopRequestId)
	require.Equal(t, "Bearer shared-control", got.Header().Get("Authorization"))
	require.Equal(t, "1", got.Header().Get(PeerHopHeader))
	require.Equal(t, req.Header().Get("Traceparent"), got.Header().Get("Traceparent"))
	pendingRequest := connect.NewRequest(&dispatcherprivatev1.ListPendingInteractionsRequest{WorkId: message.WorkId})
	_, err = client.ListPendingInteractions(ctx, pendingRequest)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	pendingRequest.Header().Set("Authorization", "Bearer shared-control")
	pendingResponse, err := client.ListPendingInteractions(ctx, pendingRequest)
	require.NoError(t, err)
	require.Equal(t, "work-a", <-lookups)
	pendingCall := <-ownerLocal.pending
	require.Equal(t, "work-a", pendingCall.Msg.WorkId)
	require.Equal(t, "Bearer shared-control", pendingCall.Header().Get("Authorization"))
	require.Equal(t, "1", pendingCall.Header().Get(PeerHopHeader))
	require.Equal(t, "permission", pendingResponse.Msg.Interactions[0].Id)
	turnRequest := connect.NewRequest(&dispatcherprivatev1.GetCurrentTurnRequest{WorkId: message.WorkId})
	turnRequest.Header().Set("Authorization", "Bearer shared-control")
	turn, err := client.GetCurrentTurn(ctx, turnRequest)
	require.NoError(t, err)
	require.Equal(t, "work-a", <-lookups)
	require.Equal(t, "turn-d", turn.Msg.TurnId)
	require.Equal(t, "1", (<-ownerLocal.turn).Header().Get(PeerHopHeader))
	cancelRequest := connect.NewRequest(&dispatcherprivatev1.CancelTurnRequest{WorkId: message.WorkId, TurnId: turn.Msg.TurnId})
	_, err = client.CancelTurn(ctx, cancelRequest)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	cancelRequest.Header().Set("Authorization", "Bearer shared-control")
	_, err = client.CancelTurn(ctx, cancelRequest)
	require.NoError(t, err)
	require.Equal(t, "work-a", <-lookups)
	cancellation := <-ownerLocal.cancel
	require.Equal(t, "turn-d", cancellation.Msg.TurnId)
	require.Equal(t, "work-a", cancellation.Msg.WorkId)
	require.Equal(t, "Bearer shared-control", cancellation.Header().Get("Authorization"))
	require.Equal(t, "1", cancellation.Header().Get(PeerHopHeader))
	req.Header().Set(PeerHopHeader, "1")
	_, err = client.Stop(ctx, req)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Empty(t, calls, "a second peer hop must not execute the command")
}

type nativeRouteDirectory struct {
	owner   command.ExecutionOwner
	lookups chan string
}

func (d *nativeRouteDirectory) Locate(_ context.Context, key string) (command.ExecutionOwner, bool, error) {
	if d.lookups != nil {
		d.lookups <- key
	}
	return d.owner, true, nil
}

type nativeRouteReceiver struct {
	cancel chan *connect.Request[dispatcherprivatev1.CancelTurnRequest]
	turn   chan *connect.Request[dispatcherprivatev1.GetCurrentTurnRequest]
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	calls   chan *connect.Request[dispatcherprivatev1.StopRequest]
	pending chan *connect.Request[dispatcherprivatev1.ListPendingInteractionsRequest]
}

func (r *nativeRouteReceiver) Stop(_ context.Context, request *connect.Request[dispatcherprivatev1.StopRequest]) (*connect.Response[dispatcherprivatev1.StopResponse], error) {
	r.calls <- request
	return connect.NewResponse(&dispatcherprivatev1.StopResponse{}), nil
}
func serveNativeRoute(t *testing.T, handler dispatcherprivatev1connect.ExecutionServiceHandler) *httptest.Server {
	t.Helper()
	path, h := dispatcherprivatev1connect.NewExecutionServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, h)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func (r *nativeRouteReceiver) ListPendingInteractions(_ context.Context, request *connect.Request[dispatcherprivatev1.ListPendingInteractionsRequest]) (*connect.Response[dispatcherprivatev1.ListPendingInteractionsResponse], error) {
	if r.pending == nil {
		return nil, connect.NewError(connect.CodeUnavailable, ErrPeerHopExceeded)
	}
	r.pending <- request
	return connect.NewResponse(&dispatcherprivatev1.ListPendingInteractionsResponse{Interactions: []*dispatcherpublicv1.PendingInteraction{{Id: "permission"}}}), nil
}

func (r *nativeRouteReceiver) CancelTurn(_ context.Context, request *connect.Request[dispatcherprivatev1.CancelTurnRequest]) (*connect.Response[dispatcherprivatev1.CancelTurnResponse], error) {
	r.cancel <- request
	return connect.NewResponse(&dispatcherprivatev1.CancelTurnResponse{}), nil
}
func (r *nativeRouteReceiver) GetCurrentTurn(_ context.Context, request *connect.Request[dispatcherprivatev1.GetCurrentTurnRequest]) (*connect.Response[dispatcherprivatev1.GetCurrentTurnResponse], error) {
	r.turn <- request
	return connect.NewResponse(&dispatcherprivatev1.GetCurrentTurnResponse{TurnId: "turn-d"}), nil
}
