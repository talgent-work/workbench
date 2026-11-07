package execution_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

type turnServer struct {
	*dispatcherServer
	turn      *dispatcherprivatev1.GetCurrentTurnResponse
	cancelErr error
	cancelled []string
}

func (s *turnServer) GetCurrentTurn(context.Context, *dispatcherprivatev1.GetCurrentTurnRequest) (*dispatcherprivatev1.GetCurrentTurnResponse, error) {
	return s.turn, nil
}

func (s *turnServer) CancelTurn(_ context.Context, r *dispatcherprivatev1.CancelTurnRequest) (*dispatcherprivatev1.CancelTurnResponse, error) {
	s.cancelled = append(s.cancelled, r.TurnId)
	return &dispatcherprivatev1.CancelTurnResponse{}, s.cancelErr
}

func turnEnvironment(t *testing.T, remote *turnServer) *execution.Environment {
	t.Helper()
	handler := connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, remote) })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	environment, err := execution.NewEnvironment(&environmentServer{}, execution.NewDispatcherControl(server.Client(), server.URL, "control-test"), &storageMounts{}, computerPlaces{})
	require.NoError(t, err)
	return environment
}

// The waiting timeout cancels a held Turn through the Environment; asking
// with no running Turn, with a cancellation pending, or after the Turn ended
// changes nothing.
func TestCancelRunningTurnIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		turn      *dispatcherprivatev1.GetCurrentTurnResponse
		cancelErr error
		cancelled []string
	}{
		{name: "running", turn: &dispatcherprivatev1.GetCurrentTurnResponse{TurnId: "turn", Active: true}, cancelled: []string{"turn"}},
		{name: "no turn", turn: &dispatcherprivatev1.GetCurrentTurnResponse{}},
		{name: "already cancelling", turn: &dispatcherprivatev1.GetCurrentTurnResponse{TurnId: "turn", Active: true, CancelRequested: true}},
		{name: "ended meanwhile", turn: &dispatcherprivatev1.GetCurrentTurnResponse{TurnId: "turn", Active: true}, cancelErr: connect.NewError(connect.CodeFailedPrecondition, "turn ended"), cancelled: []string{"turn"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			remote := &turnServer{dispatcherServer: &dispatcherServer{}, turn: c.turn, cancelErr: c.cancelErr}
			require.NoError(t, turnEnvironment(t, remote).CancelRunningTurn(ctx, "work"))
			require.Equal(t, c.cancelled, remote.cancelled)
		})
	}
	remote := &turnServer{dispatcherServer: &dispatcherServer{}, turn: &dispatcherprivatev1.GetCurrentTurnResponse{TurnId: "turn", Active: true}, cancelErr: connect.NewError(connect.CodeUnavailable, "dispatcher down")}
	require.Error(t, turnEnvironment(t, remote).CancelRunningTurn(ctx, "work"), "a failed cancellation is retried by recovery")
}
