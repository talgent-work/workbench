package connectrpc_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
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
		handler := dispatcherconnect.NewExecutionHandler(application.NewApplication(nil, nil, nil, nil, nil, nil, stream, stream, nil, nil), "token")
		turnRequest := connect.NewRequest(&dispatcherprivatev1.GetCurrentTurnRequest{WorkId: "work"})
		turnRequest.Header().Set("Authorization", "Bearer token")
		_, err := handler.GetCurrentTurn(context.Background(), turnRequest)
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), cause.Error())
		pendingRequest := connect.NewRequest(&dispatcherprivatev1.ListPendingInteractionsRequest{WorkId: "work"})
		pendingRequest.Header().Set("Authorization", "Bearer token")
		_, err = handler.ListPendingInteractions(context.Background(), pendingRequest)
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), cause.Error())
	}
}
