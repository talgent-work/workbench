package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestRejectedPreparationEndsTheStartAndFencesItsRequest(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	input := command.PrepareWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-338", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "retained rejected input"}}
	input.WorkspaceReference = sessions.session.WorkspaceReference
	// A definitive preparation rejection after the start was admitted.
	store := &initialRequestStore{request: command.InitialExecutionRequest{PrepareWorkExecution: input, Version: 1, Submitted: true, Prepared: true, Rejected: true, StartFailed: true, FailureReason: "Repository preparation was rejected. Check the configuration before retrying."}}
	prepare := command.NewPrepareWorkExecutionHandler(store, sessions, sessionAccess{}, agentquery.ModelConfigurations{})
	w := worldOver(t, sessions)
	w.ready(t)
	failure := errors.New("database unavailable")
	sessions.saveErr = failure
	recover := command.NewRecoverInitialExecutionHandler(store, sessions, prepare)
	require.ErrorIs(t, recover.Handle(ctx, input.WorkID), failure)
	require.False(t, store.request.CleanupConfirmed)
	require.False(t, store.request.RejectionApplied, "the rejection is applied only with the Work's end")
	require.Equal(t, domain.WorkWorking, sessions.session.State)
	require.Empty(t, sessions.stopRequests())
	// A duplicate start cannot acknowledge the rejected request.
	_, err := prepare.Handle(ctx, input)
	require.ErrorIs(t, err, command.ErrInitialExecutionRejected)
	sessions.saveErr = nil
	require.NoError(t, recover.Handle(ctx, input.WorkID))
	require.Equal(t, domain.WorkStopping, sessions.session.State)
	require.Equal(t, input.RequestID, sessions.session.StopRequestReference)
	require.Equal(t, []string{input.RequestID}, sessions.stopRequests(), "the end is committed before the process is asked to exit")
	require.False(t, store.request.CleanupConfirmed, "a rejected start keeps its input for an explicit Restart")
	require.Equal(t, input.Input, store.request.Input)
	require.NoError(t, recover.Handle(ctx, input.WorkID))
	require.Equal(t, []string{input.RequestID}, sessions.stopRequests(), "an applied rejection ends the start once")
	require.Equal(t, domain.WorkStopping, sessions.session.State, "preparation failure cannot close the conversation")
	require.NoError(t, w.coordination.RequestProcessExit(ctx, input.WorkID))
	w.exit(t, 0)
	w.release(t)
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	// An explicit Resume can permit later input after normal stop. The rejected
	// original input remains retained, and recovery cannot send it to that execution.
	err = command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, &continuationRequests{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: input.WorkID, RequestID: uuid.NewString()})
	require.NoError(t, err)
	w.ready(t)
	require.NoError(t, recover.Handle(ctx, input.WorkID))
	require.Equal(t, domain.WorkWorking, sessions.session.State, "old cleanup cannot fail a later explicit execution")
	delivered := &commandtest.InputBuffer{}
	ordinary := command.NewDeliverWorkInputHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, delivered))
	require.NoError(t, deliveryErr(ordinary.Deliver(ctx, command.DeliverWorkInput{SubjectID: "owner", WorkID: input.WorkID, Input: command.ExecutionInput{ID: uuid.NewString(), Text: "explicit fresh input"}})))
	require.Equal(t, "explicit fresh input", delivered.Inputs[0].Text)
	require.Equal(t, input.Input, store.request.Input)
}
