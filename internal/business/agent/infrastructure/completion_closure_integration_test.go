//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

func completionHandler(db *xorm.Engine, initial command.InitialExecutionReader) *command.CloseWorkHandler {
	return command.NewCloseWorkHandler(infrastructure.NewAgentWorkRepository(db, time.Now), infrastructure.NewWorkAuthorizer(db), infrastructure.NewExecutionRequestsFactory(executionPort{}), initial, infrastructure.NewExecutionQueryRepository(db), infrastructure.NewContinuationRequests(db, time.Now), &commandtest.InputBuffer{})
}

// Closing a running Work for a completed Subject waits for its first input and
// its Turn, stops it, and closes it once stopped, across reconnects.
func TestSubjectCompletionClosureStopsTheWorkFirstAndSurvivesReconnect(t *testing.T) {
	ctx := context.Background()
	f, ref := runningInputSession(t)
	initial := infrastructure.NewInitialExecutionRequests(f.db, time.Now)
	handler := completionHandler(f.db, initial)
	request := command.WorkControl{SubjectID: "owner", WorkID: ref, RequestID: uuid.NewString()}
	require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrWorkTransition, "unfinished first input prevents closure")
	started, err := initial.Get(ctx, ref)
	require.NoError(t, err)
	started.InputBuffered = true
	require.NoError(t, initial.Save(ctx, started))
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, exitedProcess, infrastructure.NewExecutionRequestsFactory(executionPort{}), &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
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
	reconnected := completionHandler(other, infrastructure.NewInitialExecutionRequests(other, time.Now))
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

type initialCompletionBarrier struct {
	command.InitialExecutionReader
	afterRead func()
}

func (r initialCompletionBarrier) Get(ctx context.Context, id string) (command.InitialExecutionRequest, error) {
	value, err := r.InitialExecutionReader.Get(ctx, id)
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
			old, err := initial.Get(ctx, id)
			require.NoError(t, err)
			old.InputBuffered = true
			require.NoError(t, initial.Save(ctx, old))
			failed, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			require.NoError(t, failed.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
			require.NoError(t, failed.RecordExit(ctx, domain.HarnessExit{ExitCode: 1, Reason: "native crash"}, executionPort{}))
			require.NoError(t, failed.ConfirmComputeReclaimed())
			require.NoError(t, f.sessions.Save(ctx, failed))
			observed, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, domain.WorkFailed, observed.State)
			factory := infrastructure.NewExecutionRequestsFactory(executionPort{})
			restart := command.NewRestartWorkHandler(time.Now, f.sessions, f.agents, infrastructure.NewWorkAuthorizer(f.db), computertest.Checked(startupAuthority{}), factory, f.sessions, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
			retry := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
			request := command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
			if startupFirst {
				handler := completionHandler(f.db, initialCompletionBarrier{InitialExecutionReader: initial, afterRead: func() {
					_, err := restart.Handle(ctx, retry)
					require.NoError(t, err)
				}})
				require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrConcurrentModification)
				actual, err := f.sessions.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStarting, actual.State)
				require.ErrorIs(t, completionHandler(f.db, initial).Handle(ctx, request), domain.ErrWorkTransition)
			} else {
				require.NoError(t, completionHandler(f.db, initial).Handle(ctx, request))
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
			require.True(t, retained.InputBuffered, "restart and closure cannot replay processed initial input")
			current, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, observed.HarnessSessionReference, current.HarnessSessionReference)
			require.Equal(t, observed.CapabilitySnapshot, current.CapabilitySnapshot)
		})
	}
}
