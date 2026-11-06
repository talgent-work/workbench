package agentfeedback_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
)

func TestReportExecutionAddressesTheWorkAndDoesNotRetry(t *testing.T) {
	receiver := &feedbackReceiver{}
	_, handler := agentprivatev1connect.NewExecutionFeedbackServiceHandler(receiver)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentfeedback.NewClient(server.Client(), server.URL, "private-token")
	observation := command.ExecutionObservation{WorkID: "work", Kind: command.ExecutionExited}
	require.NoError(t, client.ReportExecution(context.Background(), observation))
	require.Equal(t, "Bearer private-token", receiver.authorization)
	require.Equal(t, "work", receiver.request.GetWorkId())
	require.Equal(t, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, receiver.request.GetKind())
	require.Equal(t, 1, receiver.calls)

	receiver.err = connect.NewError(connect.CodeUnavailable, command.ErrExecutionFeedbackUnavailable)
	require.ErrorIs(t, client.ReportExecution(context.Background(), observation), command.ErrExecutionFeedbackUnavailable)
	require.Equal(t, 2, receiver.calls, "an uncertain report is not automatically retried")
	receiver.err = connect.NewError(connect.CodeFailedPrecondition, command.ErrExecutionFeedbackRejected)
	require.ErrorIs(t, client.ReportExecution(context.Background(), observation), command.ErrExecutionFeedbackRejected)
	require.Equal(t, 3, receiver.calls)
	observation.WorkID = ""
	require.ErrorIs(t, client.ReportExecution(context.Background(), observation), command.ErrInvalidExecutionFeedback)
	require.Equal(t, 3, receiver.calls, "feedback without a Work is never sent")
	observation.WorkID = "work"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, client.ReportExecution(ctx, observation), context.Canceled)
}

type feedbackReceiver struct {
	agentprivatev1connect.UnimplementedExecutionFeedbackServiceHandler
	request       *agentprivatev1.ReportRequest
	authorization string
	calls         int
	err           error
}

func (r *feedbackReceiver) Report(_ context.Context, request *connect.Request[agentprivatev1.ReportRequest]) (*connect.Response[agentprivatev1.ReportResponse], error) {
	r.calls++
	r.request = request.Msg
	r.authorization = request.Header().Get("Authorization")
	return connect.NewResponse(&agentprivatev1.ReportResponse{}), r.err
}
