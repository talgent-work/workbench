package routing

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/peer"
)

func TestCapabilitiesRouteToCurrentOwnerWithPrivateAuthentication(t *testing.T) {
	receiver := &capabilityRouteReceiver{calls: make(chan call[dispatcherprivatev1.GetCapabilitiesRequest], 1)}
	owner := serveNativeRoute(t, NewNativeExecutionHandler(receiver, &nativeRouteDirectory{owner: command.ExecutionOwner{Instance: "owner", Alive: true}}, "owner", "control", peer.NewNativeClients(http.DefaultClient)))
	lookups := make(chan string, 1)
	ingress := serveNativeRoute(t, NewNativeExecutionHandler(&dispatcherprivatev1connect.UnimplementedExecutionServiceHandler{}, &nativeRouteDirectory{owner: command.ExecutionOwner{Instance: "owner", Addr: strings.TrimPrefix(owner.URL, "http://"), Alive: true}, lookups: lookups}, "ingress", "control", peer.NewNativeClients(http.DefaultClient)))
	client := dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(ingress.Client(), ingress.URL)))
	request := &dispatcherprivatev1.GetCapabilitiesRequest{WorkId: "work"}
	_, err := client.GetCapabilities(t.Context(), request)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Empty(t, lookups)
	response, err := client.GetCapabilities(withHeaders(t.Context(), "Authorization", "Bearer control"), request)
	require.NoError(t, err)
	require.Equal(t, "work", <-lookups)
	got := <-receiver.calls
	require.Equal(t, "work", got.request.WorkId)
	require.Equal(t, "Bearer control", got.header.Get("Authorization"))
	require.Equal(t, "1", got.header.Get(PeerHopHeader))
	require.True(t, response.Observed)
	require.Equal(t, "pkg:check", response.Commands[0].Name)
	require.Equal(t, "[path]", response.Commands[0].ArgumentHint)
	_, err = client.GetCapabilities(withHeaders(t.Context(), "Authorization", "Bearer control", PeerHopHeader, "1"), request)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Empty(t, receiver.calls)
}

type capabilityRouteReceiver struct {
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	calls chan call[dispatcherprivatev1.GetCapabilitiesRequest]
}

func (r *capabilityRouteReceiver) GetCapabilities(ctx context.Context, request *dispatcherprivatev1.GetCapabilitiesRequest) (*dispatcherprivatev1.GetCapabilitiesResponse, error) {
	r.calls <- received(ctx, request)
	return &dispatcherprivatev1.GetCapabilitiesResponse{Observed: true, Commands: []*dispatcherprivatev1.NativeCommand{{Name: "pkg:check", Description: "Check", ArgumentHint: "[path]"}}}, nil
}
