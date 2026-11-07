package connectrpc_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	dispatcherconnect "github.com/talgent-work/workbench/internal/business/dispatcher/transport/connectrpc"
)

type unobservedStream struct{ err error }

func (s unobservedStream) CancelTurn(context.Context, application.CancelTurn) error { return s.err }
func (s unobservedStream) GetCurrentTurn(context.Context, string) (application.CurrentTurn, error) {
	return application.CurrentTurn{}, s.err
}
func (s unobservedStream) ListPendingInteractions(context.Context, string) ([]application.PendingInteraction, error) {
	return nil, s.err
}

// A read of a stream no replica observes now answers Unavailable, whether no
// replica reads it or its connection is lost or not yet replayed.
func TestLiveStreamReadsAnswerUnavailableWhileTheStreamIsUnobserved(t *testing.T) {
	for _, cause := range []error{command.ErrNoActiveConnection, command.ErrExecutionNotReady} {
		stream := unobservedStream{err: cause}
		server := connect.NewServer()
		dispatcherprivatev1connect.RegisterExecutionServiceHandler(server, dispatcherconnect.NewExecutionHandler(application.NewApplication(nil, nil, nil, nil, nil, nil, stream, stream, nil, nil), "token"))
		client := dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connectinprocess.New(server)))
		ctx, info := connect.NewClientContext(context.Background())
		info.RequestHeader().Set("Authorization", "Bearer token")
		_, err := client.GetCurrentTurn(ctx, &dispatcherprivatev1.GetCurrentTurnRequest{WorkId: "work"})
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), cause.Error())
		ctx, info = connect.NewClientContext(context.Background())
		info.RequestHeader().Set("Authorization", "Bearer token")
		_, err = client.ListPendingInteractions(ctx, &dispatcherprivatev1.ListPendingInteractionsRequest{WorkId: "work"})
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), cause.Error())
	}
}
