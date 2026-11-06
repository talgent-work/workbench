package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Close is the one way to close a Work. A running Work is stopped first,
// between Turns, and closes once stopped; nothing it still owes is dropped.
func TestCloseStopsARunningWorkFirstAndClosesItOnceStopped(t *testing.T) {
	ctx := context.Background()
	s := newSession(t)
	port := &executionPort{}
	require.ErrorIs(t, s.Close(ctx, "finish", domain.ClosureFacts{}, port), domain.ErrWorkTransition, "a starting Work is not closed")
	require.NoError(t, recoverStartup(ctx, s, port))
	require.NoError(t, s.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, s.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, s.ObserveTurn(domain.TurnObservation{Sequence: 10, Active: true}, false, false, time.Now()))
	require.ErrorIs(t, s.Close(ctx, "finish", domain.ClosureFacts{}, port), domain.ErrTurnActive)
	require.NoError(t, s.ObserveTurn(domain.TurnObservation{Sequence: 20, Completed: true}, false, false, time.Now()))
	for _, fact := range []domain.ClosureFacts{{InputPending: true}, {StartupPending: true}, {ContinuationPending: true}} {
		require.ErrorIs(t, s.Close(ctx, "finish", fact, port), domain.ErrWorkTransition)
		require.Equal(t, domain.WorkWorking, s.State)
		require.Empty(t, s.CloseRequestReference)
	}

	require.NoError(t, s.Close(ctx, "finish", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkStopping, s.State, "a running Work is stopped before it closes")
	require.Equal(t, "finish", s.CloseRequestReference)
	require.Equal(t, []string{"finish"}, port.stops)
	require.False(t, s.KeptCloseApplicable(), "the kept close applies once the Work has stopped")
	require.ErrorIs(t, s.RequireReadyProcess(), domain.ErrWorkTransition)
	require.NoError(t, s.Close(ctx, "retry", domain.ClosureFacts{}, port))
	require.Equal(t, "finish", s.CloseRequestReference, "a repeated close keeps the first request")

	require.NoError(t, s.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, s.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, s.State)
	require.True(t, s.KeptCloseApplicable())
	ended := s.Events.Drain()
	require.Equal(t, []domain.Event{
		domain.WorkDeskReleased{WorkID: s.ID, DeskID: s.HarnessProcess.DeskID},
		domain.WorkExecutionEnded{WorkID: s.ID},
	}, ended, "the Desk leaves once compute is released, and the stop is told so the kept close applies")
	require.ErrorIs(t, s.Resume(ctx, admission(s), port), domain.ErrWorkTransition, "a Work being closed is not resumed")

	require.NoError(t, s.Close(ctx, s.CloseRequestReference, domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkClosed, s.State)
	require.Equal(t, "finish", s.CloseRequestReference)
	require.False(t, s.KeptCloseApplicable())
}

// A stopped or failed Work closes directly; a pending Resume or Restart, or
// input the stopped process still holds, prevents it.
func TestCloseAppliesDirectlyToAStoppedWork(t *testing.T) {
	ctx := context.Background()
	s := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, s, port))
	require.NoError(t, s.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, s.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, s.Stop(ctx, "stop", port))
	require.NoError(t, s.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, s.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, s.State)
	require.ErrorIs(t, s.Close(ctx, "archive", domain.ClosureFacts{ContinuationPending: true}, port), domain.ErrWorkTransition)
	require.NoError(t, s.Close(ctx, "archive", domain.ClosureFacts{InputPending: true}, port), "input waiting for a Resume is dropped with the Work")
	require.Equal(t, domain.WorkClosed, s.State)
	require.Equal(t, []string{"stop"}, port.stops, "a stopped Work needs no stop")
}
