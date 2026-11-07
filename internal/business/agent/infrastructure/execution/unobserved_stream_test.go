package execution_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

type unobservedServer struct {
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	readErr, cancelErr error
}

func (s unobservedServer) GetCurrentTurn(context.Context, *dispatcherprivatev1.GetCurrentTurnRequest) (*dispatcherprivatev1.GetCurrentTurnResponse, error) {
	return nil, s.readErr
}
func (s unobservedServer) ListPendingInteractions(context.Context, *dispatcherprivatev1.ListPendingInteractionsRequest) (*dispatcherprivatev1.ListPendingInteractionsResponse, error) {
	return nil, s.readErr
}
func (s unobservedServer) CancelTurn(context.Context, *dispatcherprivatev1.CancelTurnRequest) (*dispatcherprivatev1.CancelTurnResponse, error) {
	return nil, s.cancelErr
}

func unobservedControl(t *testing.T, server unobservedServer) *execution.DispatcherControl {
	t.Helper()
	handler := connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, server) })
	remote := httptest.NewServer(handler)
	t.Cleanup(remote.Close)
	return execution.NewDispatcherControl(remote.Client(), remote.URL, "control-test")
}

// Dispatcher answers a stream nobody reads now as Unavailable for reads and
// NotFound for the cancel control; Agent reports both as the expected
// ErrExecutionUnobserved, and any other failure as a failed request.
func TestAnUnobservedStreamIsNotAFailedRequest(t *testing.T) {
	ctx := context.Background()
	control := unobservedControl(t, unobservedServer{readErr: connect.NewError(connect.CodeUnavailable, "no reader"), cancelErr: connect.NewError(connect.CodeNotFound, "no reader")})
	_, err := control.GetCurrentTurn(ctx, "work")
	require.ErrorIs(t, err, domain.ErrExecutionUnobserved)
	_, err = control.ListPendingInteractions(ctx, "work")
	require.ErrorIs(t, err, domain.ErrExecutionUnobserved)
	require.ErrorIs(t, control.CancelTurn(ctx, "work", "turn"), domain.ErrExecutionUnobserved)

	failing := unobservedControl(t, unobservedServer{readErr: connect.NewError(connect.CodeInternal, "boom"), cancelErr: connect.NewError(connect.CodeInternal, "boom")})
	_, err = failing.GetCurrentTurn(ctx, "work")
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
	require.NotErrorIs(t, err, domain.ErrExecutionUnobserved)
	_, err = failing.ListPendingInteractions(ctx, "work")
	require.NotErrorIs(t, err, domain.ErrExecutionUnobserved)
	require.NotErrorIs(t, failing.CancelTurn(ctx, "work", "turn"), domain.ErrExecutionUnobserved)
}
