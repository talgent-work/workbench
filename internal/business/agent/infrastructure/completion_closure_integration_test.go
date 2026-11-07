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
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

func completionHandler(db *xorm.Engine, continuations agentquery.ContinuationReader) *command.CloseWorkHandler {
	return command.NewCloseWorkHandler(infrastructure.NewAgentWorkRepository(db, time.Now), access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db)), executionPort{}, continuations, &commandtest.InputBuffer{})
}

// Closing a running Work for a completed Subject waits for its first input and
// its Turn, stops it, and closes it once stopped, across reconnects.
func TestSubjectCompletionClosureStopsTheWorkFirstAndSurvivesReconnect(t *testing.T) {
	ctx := context.Background()
	f := newWorkCreationFixture(t)
	ref, err := f.creation(nil, computertest.Checked(startupAuthority{}), executionPort{}).Handle(ctx, f.request)
	require.NoError(t, err)
	ready := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, exitedProcess, executionPort{}, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, ready.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	handler := completionHandler(f.db, infrastructure.NewContinuationRequests(f.db, time.Now))
	request := command.WorkControl{SubjectID: "owner", WorkID: ref, RequestID: uuid.NewString()}
	require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrWorkTransition, "an initial task input not handed off prevents closure")
	require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, ref))
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, exitedProcess, executionPort{}, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: ref, Kind: command.TurnStarted, ObservationSequence: 10}))
	require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrTurnActive, "a running Turn prevents the stop closure needs")
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: ref, Kind: command.TurnFinished, ObservationSequence: 20, TurnCompleted: true}))
	require.NoError(t, handler.Handle(ctx, request))
	accepted, err := f.sessions.Get(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, accepted.State, "a running Work is stopped before it closes")
	require.Equal(t, request.RequestID, accepted.CloseRequestReference)
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	reconnected := completionHandler(other, infrastructure.NewContinuationRequests(other, time.Now))
	require.NoError(t, reconnected.Handle(ctx, request), "a lost response retries the same accepted closure")
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: ref, Kind: command.ExecutionExited}))
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{WorkID: ref, Kind: command.ComputeReclaimed}))
	stopped, err := f.sessions.Get(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, stopped.State)
	require.NoError(t, reconnected.ApplyKeptClose(ctx, ref))
	closed, err := infrastructure.NewAgentWorkRepository(other, time.Now).Get(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	require.Equal(t, request.RequestID, closed.CloseRequestReference)
}

type continuationCompletionBarrier struct {
	agentquery.ContinuationReader
	afterRead func()
}

func (r continuationCompletionBarrier) GetWorkContinuation(ctx context.Context, id string) (agentquery.WorkContinuation, error) {
	value, err := r.ContinuationReader.GetWorkContinuation(ctx, id)
	if err == nil {
		r.afterRead()
	}
	return value, err
}

func TestCompletionAndRestartShareOneOrder(t *testing.T) {
	for _, startupFirst := range []bool{true, false} {
		name := "closure first"
		if startupFirst {
			name = "restart commits after closure read"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f, id := runningInputSession(t)
			initial := infrastructure.NewInitialExecutionRequests(f.db, time.Now)
			continuations := infrastructure.NewContinuationRequests(f.db, time.Now)
			old, err := initial.Get(ctx, id)
			require.NoError(t, err)
			failed, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			require.NoError(t, failed.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
			require.NoError(t, failed.RecordExit(ctx, domain.HarnessExit{ExitCode: 1, Reason: "native crash"}, executionPort{}))
			require.NoError(t, failed.ConfirmComputeReclaimed())
			require.NoError(t, f.sessions.Save(ctx, failed))
			observed, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, domain.WorkFailed, observed.State)
			factory := executionPort{}
			restart := command.NewRestartWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(startupAuthority{}), factory, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
			retry := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
			request := command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
			if startupFirst {
				handler := completionHandler(f.db, continuationCompletionBarrier{ContinuationReader: continuations, afterRead: func() {
					_, err := restart.Handle(ctx, retry)
					require.NoError(t, err)
				}})
				require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrConcurrentModification)
				actual, err := f.sessions.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStarting, actual.State)
				require.ErrorIs(t, completionHandler(f.db, continuations).Handle(ctx, request), domain.ErrWorkTransition)
			} else {
				require.NoError(t, completionHandler(f.db, continuations).Handle(ctx, request))
				_, err := restart.Handle(ctx, retry)
				require.ErrorIs(t, err, domain.ErrWorkTransition)
				actual, err := f.sessions.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkClosed, actual.State)
				retained, err := initial.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, old.RequestID, retained.RequestID)
			}
			retained, err := initial.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, old.PrepareWorkExecution, retained.PrepareWorkExecution)
			current, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			_, pending := current.PendingInitialTaskInput()
			require.False(t, pending, "restart and closure cannot replay a handed-off initial task input")
			require.Equal(t, observed.HarnessSessionReference, current.HarnessSessionReference)
			require.Equal(t, observed.CapabilitySnapshot, current.CapabilitySnapshot)
		})
	}
}
