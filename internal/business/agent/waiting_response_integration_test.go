//go:build integration

package agent_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// The controlled Harness keeps both requests pending after accepting a reply.
// Work state must follow platform admission, independently of protocol latency.
func assertWaitingResponseAdmission(t *testing.T, ctx context.Context, client agentprivatev1connect.AgentServiceClient, feedback agentprivatev1connect.ExecutionFeedbackServiceClient, port *sessionCommandRecorder, work string) {
	t.Helper()
	report := func(kind agentprivatev1.FeedbackKind, sequence int64, interaction string) {
		t.Helper()
		r := &agentprivatev1.ReportRequest{ProcessId: "process", WorkId: work, Kind: kind, ObservationSequence: sequence, InteractionId: interaction, RequiredInteractionIds: func() []string {
			var ids []string
			for _, item := range port.pending {
				ids = append(ids, item.ID)
			}
			return ids
		}()}
		rCtx, rInfo := connect.NewClientContext(ctx)
		rInfo.RequestHeader().Set("Authorization", "Bearer feedback")
		_, err := feedback.Report(rCtx, r)
		require.NoError(t, err)
	}
	waiting := func() bool {
		t.Helper()
		response, err := client.GetWork(sessionControlRequest(ctx), &agentprivatev1.GetWorkRequest{SubjectId: "employee-a", WorkId: work})
		require.NoError(t, err)
		require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, response.State, "Waiting is not a Work State")
		return response.GetWaiting()
	}
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_STARTED, 2, "")
	port.pending = []command.PendingInteraction{{ID: "approval-a"}, {ID: "question-b"}}
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, 3, "approval-a")
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, 4, "question-b")
	require.True(t, waiting())
	respond := func(id string) error {
		_, err := client.RespondToInteraction(sessionControlRequest(ctx), &agentprivatev1.RespondToInteractionRequest{SubjectId: "employee-a", WorkId: work, InteractionId: id, Allow: true})
		return err
	}
	port.replyError = errors.New("input buffer unavailable")
	require.Error(t, respond("approval-a"))
	require.True(t, waiting(), "a reply whose hand-off fails is not accepted")
	port.replyError = nil
	require.NoError(t, respond("approval-a"))
	require.False(t, waiting(), "an accepted reply ends Waiting")
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, 5, "question-b")
	require.True(t, waiting(), "the next interaction feedback restores Waiting while another request remains")
	require.NoError(t, respond("question-b"))
	require.False(t, waiting())
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, 4, "question-b")
	require.False(t, waiting(), "stale required-response feedback cannot recreate user debt")
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, 6, "question-b")
	require.False(t, waiting(), "buffered replies answer requests the Harness has not resolved yet")
	// Ordinary input is accepted during a pending request. A repeated older
	// request must not undo a later accepted input.
	port.settle("approval-a")
	port.settle("question-b")
	port.pending = []command.PendingInteraction{{ID: "question-c"}}
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, 7, "question-c")
	require.True(t, waiting())
	port.inputError = errors.New("input buffer unavailable")
	inputID := uuid.NewString()
	_, err := client.DeliverInput(sessionControlRequest(ctx), &agentprivatev1.DeliverInputRequest{SubjectId: "employee-a", WorkId: work, InputId: inputID, Input: "accepted after the request"})
	require.Error(t, err)
	require.True(t, waiting())
	port.inputError = nil
	_, err = client.DeliverInput(sessionControlRequest(ctx), &agentprivatev1.DeliverInputRequest{SubjectId: "employee-a", WorkId: work, InputId: inputID, Input: "accepted after the request"})
	require.NoError(t, err)
	require.True(t, waiting(), "input held behind the unanswered request keeps the Turn Waiting")
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, 7, "question-c")
	require.True(t, waiting())
	port.settle(inputID)
	port.pending = nil
	// The Turn ends, so the Work can stop between Turns.
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED, 8, "")
	require.False(t, waiting(), "the Turn's end ends Waiting")
}
