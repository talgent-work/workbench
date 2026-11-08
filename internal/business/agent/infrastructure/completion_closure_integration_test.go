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

func completionHandler(db *xorm.Engine) *command.CloseWorkHandler {
	return completionHandlerOver(db, infrastructure.NewAgentWorkRepository(db, time.Now))
}

func completionHandlerOver(db *xorm.Engine, works domain.AgentWorkRepository) *command.CloseWorkHandler {
	return command.NewCloseWorkHandler(works, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), executionsOf(db), &commandtest.InputBuffer{})
}

// Closing a running Work for a completed Subject waits for its first input and
// its Turn, stops it, and closes it once stopped, across reconnects.
func TestSubjectCompletionClosureStopsTheWorkFirstAndSurvivesReconnect(t *testing.T) {
	ctx := context.Background()
	f := newWorkCreationFixture(t)
	ref, err := f.creation(nil, computertest.Checked(startupAuthority{})).Handle(ctx, f.request)
	require.NoError(t, err)
	feedback := newRuns(t, f.db, f.sessions)
	feedback.ready(ctx, ref)
	handler := completionHandler(f.db)
	request := command.WorkControl{SubjectID: "owner", WorkID: ref, RequestID: uuid.NewString()}
	require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrWorkTransition, "an initial task input not handed off prevents closure")
	require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, ref))
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
	reconnected := completionHandler(other)
	require.NoError(t, reconnected.Handle(ctx, request), "a lost response retries the same accepted closure")
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: ref, Kind: command.ExecutionExited}))
	feedback.release(ctx, ref)
	stopped, err := f.sessions.Get(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, stopped.State)
	require.NoError(t, reconnected.ApplyKeptClose(ctx, ref))
	closed, err := infrastructure.NewAgentWorkRepository(other, time.Now).Get(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, closed.State)
	require.Equal(t, request.RequestID, closed.CloseRequestReference)
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
			failed, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			require.NoError(t, failed.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
			reportExit(t, failed, domain.HarnessExit{ExitCode: 1, Reason: "native crash"})
			reportReleased(t, failed)
			require.NoError(t, f.sessions.Save(ctx, failed))
			observed, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, domain.WorkFailed, observed.State)
			restart := command.NewRestartWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
			retry := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
			request := command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
			if startupFirst {
				handler := completionHandlerOver(f.db, workReadBarrier{AgentWorkRepository: f.sessions, done: new(bool), after: func() {
					_, err := restart.Handle(ctx, retry)
					require.NoError(t, err)
				}})
				require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrConcurrentModification)
				actual, err := f.sessions.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStarting, actual.State)
				require.ErrorIs(t, completionHandler(f.db).Handle(ctx, request), domain.ErrWorkTransition)
			} else {
				require.NoError(t, completionHandler(f.db).Handle(ctx, request))
				_, err := restart.Handle(ctx, retry)
				require.ErrorIs(t, err, domain.ErrWorkTransition)
				actual, err := f.sessions.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, domain.WorkClosed, actual.State)
			}
			current, err := f.sessions.Get(ctx, id)
			require.NoError(t, err)
			_, pending := current.PendingInitialTaskInput()
			require.False(t, pending, "restart and closure cannot replay a handed-off initial task input")
			require.Equal(t, observed.HarnessSessionReference, current.HarnessSessionReference)
			require.Equal(t, observed.CapabilitySnapshot, current.CapabilitySnapshot)
		})
	}
}
