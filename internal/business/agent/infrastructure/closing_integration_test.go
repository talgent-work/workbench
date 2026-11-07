//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

func TestClosingRemainsDiscoverableAndConvergesOnEnvironmentLoss(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	port := &recoveryExecutionPort{}
	factory := port
	authorization := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db))
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	ref, err := continuedRef(id, command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, authorization, computertest.Checked(startupAuthority{}), factory, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	require.NoError(t, err)
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, observedProcess{State: domain.ProcessRunning}, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.TurnStarted, WorkID: ref, ObservationSequence: 10}))
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.TurnFinished, WorkID: ref, ObservationSequence: 20, TurnCompleted: true}))
	// Agent records a failure without an observed exit; a compute-release
	// receipt alone does not prove the process ended.
	failed, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.NoError(t, failed.RecordFailure("runtime failed"))
	require.NoError(t, f.sessions.Save(ctx, failed))
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ComputeReclaimed, WorkID: ref}))
	closeRequestID := uuid.NewString()
	require.NoError(t, command.NewCloseWorkHandler(f.sessions, authorization, factory, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{}).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: closeRequestID}))
	pending, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosing, pending.State)
	require.True(t, pending.HoldsProcess(), "the process may still run until its exit or loss is confirmed")
	ids, err := infrastructure.NewExecutionQueryRepository(f.db).ListWorkIDs(ctx, "", 100)
	require.NoError(t, err)
	require.Contains(t, ids, id, "Closing must remain recoverable")

	// Recreate adapters on an independent connection, as after a service restart.
	db, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	feedback = command.NewApplyExecutionFeedbackHandler(time.Now, sessions, observedProcess{State: domain.ProcessEnvironmentLost}, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	for range 2 {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.EnvironmentLost, WorkID: ref}))
	}
	closed, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	require.Equal(t, closeRequestID, closed.CloseRequestReference)
	require.Equal(t, domain.TurnObservation{Sequence: 20, Completed: true}, closed.HarnessProcess.Turn, "turn evidence survives persistence and reconnect")
	require.True(t, closed.HarnessProcess.EnvironmentLost)
	require.False(t, closed.HarnessProcess.Exited, "normal exit must not be fabricated")
	require.NotEmpty(t, closed.FailureReason)
	require.Equal(t, pending.WorkspaceReference, closed.WorkspaceReference)
	require.Equal(t, pending.HomeBinding, closed.HomeBinding)
	require.False(t, closed.HoldsProcess())
	require.ErrorIs(t, closed.RequireReadyProcess(), domain.ErrWorkTransition)
}
