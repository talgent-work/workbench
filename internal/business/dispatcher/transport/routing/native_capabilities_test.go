package routing

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/peer"
)

func TestCapabilitiesRouteToCurrentOwnerWithPrivateAuthentication(t *testing.T) {
	receiver := &capabilityRouteReceiver{calls: make(chan *connect.Request[dispatcherprivatev1.GetCapabilitiesRequest], 1)}
	owner := serveNativeRoute(t, NewNativeExecutionHandler(receiver, &nativeRouteDirectory{owner: command.ExecutionOwner{Instance: "owner", Alive: true}}, "owner", "control", peer.NewNativeClients(http.DefaultClient)))
	lookups := make(chan string, 1)
	ingress := serveNativeRoute(t, NewNativeExecutionHandler(&dispatcherprivatev1connect.UnimplementedExecutionServiceHandler{}, &nativeRouteDirectory{owner: command.ExecutionOwner{Instance: "owner", Addr: strings.TrimPrefix(owner.URL, "http://"), Alive: true}, lookups: lookups}, "ingress", "control", peer.NewNativeClients(http.DefaultClient)))
	client := dispatcherprivatev1connect.NewExecutionServiceClient(ingress.Client(), ingress.URL)
	request := connect.NewRequest(&dispatcherprivatev1.GetCapabilitiesRequest{WorkId: "work"})
	_, err := client.GetCapabilities(t.Context(), request)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Empty(t, lookups)
	request.Header().Set("Authorization", "Bearer control")
	response, err := client.GetCapabilities(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "work", <-lookups)
	got := <-receiver.calls
	require.Equal(t, "work", got.Msg.WorkId)
	require.Equal(t, "Bearer control", got.Header().Get("Authorization"))
	require.Equal(t, "1", got.Header().Get(PeerHopHeader))
	require.True(t, response.Msg.Observed)
	require.Equal(t, "pkg:check", response.Msg.Commands[0].Name)
	require.Equal(t, "[path]", response.Msg.Commands[0].ArgumentHint)
	request.Header().Set(PeerHopHeader, "1")
	_, err = client.GetCapabilities(t.Context(), request)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Empty(t, receiver.calls)
}

type capabilityRouteReceiver struct {
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	calls chan *connect.Request[dispatcherprivatev1.GetCapabilitiesRequest]
}

func (r *capabilityRouteReceiver) GetCapabilities(_ context.Context, request *connect.Request[dispatcherprivatev1.GetCapabilitiesRequest]) (*connect.Response[dispatcherprivatev1.GetCapabilitiesResponse], error) {
	r.calls <- request
	return connect.NewResponse(&dispatcherprivatev1.GetCapabilitiesResponse{Observed: true, Commands: []*dispatcherprivatev1.NativeCommand{{Name: "pkg:check", Description: "Check", ArgumentHint: "[path]"}}}), nil
}
