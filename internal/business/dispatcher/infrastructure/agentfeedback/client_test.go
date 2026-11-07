package agentfeedback_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

func TestReportExecutionAddressesTheWorkAndDoesNotRetry(t *testing.T) {
	receiver := &feedbackReceiver{}
	handler := connecttest.Handler(func(s *connect.Server) { agentprivatev1connect.RegisterExecutionFeedbackServiceHandler(s, receiver) })
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentfeedback.NewClient(server.Client(), server.URL, "private-token")
	observation := command.ExecutionObservation{WorkID: "work", Kind: command.ExecutionExited}
	require.NoError(t, client.ReportExecution(context.Background(), observation))
	require.Equal(t, "Bearer private-token", receiver.authorization)
	require.Equal(t, "work", receiver.request.GetWorkId())
	require.Equal(t, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, receiver.request.GetKind())
	require.Equal(t, 1, receiver.calls)

	receiver.err = connect.NewError(connect.CodeUnavailable, command.ErrExecutionFeedbackUnavailable.Error()).WithCause(command.ErrExecutionFeedbackUnavailable)
	require.ErrorIs(t, client.ReportExecution(context.Background(), observation), command.ErrExecutionFeedbackUnavailable)
	require.Equal(t, 2, receiver.calls, "an uncertain report is not automatically retried")
	receiver.err = connect.NewError(connect.CodeFailedPrecondition, command.ErrExecutionFeedbackRejected.Error()).WithCause(command.ErrExecutionFeedbackRejected)
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

func (r *feedbackReceiver) Report(ctx context.Context, request *agentprivatev1.ReportRequest) (*agentprivatev1.ReportResponse, error) {
	r.calls++
	r.request = request
	info, _ := connect.CallInfoForServerContext(ctx)
	r.authorization = info.RequestHeader().Get("Authorization")
	return &agentprivatev1.ReportResponse{}, r.err
}
