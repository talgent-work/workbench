package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestTurnFeedbackIsCorrectedByCurrentProcess(t *testing.T) {
	session := newSession(t)
	require.NoError(t, recoverStartup(context.Background(), session, &executionPort{}))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.ErrorIs(t, session.ObserveTurn(domain.TurnObservation{Sequence: 0, Active: true}, false, false, time.Now()), domain.ErrInvalidWork)
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 10, Active: true}, false, false, time.Now()))
	require.True(t, session.HarnessProcess.Turn.Active)
	require.ErrorIs(t, session.ObserveTurn(domain.TurnObservation{Sequence: 0, Completed: true}, false, false, time.Now()), domain.ErrInvalidWork)
	require.True(t, session.HarnessProcess.Turn.Active)
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 20, Completed: true}, false, false, time.Now()))
	require.True(t, session.HarnessProcess.Turn.Completed)
	require.Equal(t, domain.WorkWorking, session.State, "a finished turn does not stop the process")
	require.True(t, session.Idle())
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 30, Active: true}, false, false, time.Now()))
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 30, Completed: true}, false, false, time.Now()), "an exact replay position is a no-op")
	require.True(t, session.HarnessProcess.Turn.Active)
	// An earlier position of the current process is a replay and changes nothing.
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 25, Completed: true}, false, false, time.Now()))
	require.True(t, session.HarnessProcess.Turn.Active, "an earlier position is a replay")
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 40, Completed: false}, false, false, time.Now()))
	require.False(t, session.HarnessProcess.Turn.Active)
	require.False(t, session.HarnessProcess.Turn.Completed, "interruption is not normal completion")
	require.NoError(t, session.Close(context.Background(), "close", domain.ClosureFacts{}, &executionPort{}))
	require.Equal(t, domain.WorkStopping, session.State, "a running Work is stopped before it closes")
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 50, Active: true}, false, false, time.Now()))
	require.Equal(t, domain.WorkStopping, session.State)
	require.ErrorIs(t, session.RequireReadyProcess(), domain.ErrWorkTransition)
}

// A new process's positions start over: a smaller position than the previous
// process reached still applies.
func TestNewProcessPositionsApplyEvenWhenSmaller(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 300, Active: true}, false, false, time.Now()))
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 301}, false, false, time.Now()))
	require.NoError(t, session.Stop(ctx, "stop", port))
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 143}, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.NoError(t, session.Resume(ctx, admission(session), port))
	require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))

	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 10, Active: true}, false, false, time.Now()))
	require.Equal(t, domain.TurnObservation{Sequence: 10, Active: true}, session.HarnessProcess.Turn)
}

// A ready Work with nothing to process is idle, as after a Resume or Restart
// without a message; input awaiting processing keeps it busy. The initial task
// input is buffered before the Harness is ready, so no guard waits for it.
func TestReadyWorkIsIdleOnceNothingIsLeftToProcess(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	require.NoError(t, recoverStartup(ctx, session, &executionPort{}))
	require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, session.RecordIdleness(true, time.Now()))
	require.False(t, session.Idle(), "admitted input keeps it busy")
	require.NoError(t, session.RecordIdleness(false, time.Now()))
	require.True(t, session.Idle(), "nothing to do: it is idle")
	require.Equal(t, domain.WorkWorking, session.State)
}
