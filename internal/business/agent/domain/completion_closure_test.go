package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Close is the one way to close a Work. A running Work is stopped first,
// between Turns, and closes once stopped; nothing it still owes is dropped.
func TestCloseStopsARunningWorkFirstAndClosesItOnceStopped(t *testing.T) {
	s := newSession(t)
	port := follow(s)
	require.ErrorIs(t, s.Close("finish", domain.ClosureFacts{}), domain.ErrWorkTransition, "a starting Work is not closed")
	ready(t, s)
	require.NoError(t, s.ObserveTurn(domain.TurnObservation{Sequence: 10, Active: true}, false, false, time.Now()))
	require.ErrorIs(t, s.Close("finish", domain.ClosureFacts{}), domain.ErrTurnActive)
	require.NoError(t, s.ObserveTurn(domain.TurnObservation{Sequence: 20, Completed: true}, false, false, time.Now()))
	for _, fact := range []domain.ClosureFacts{{InputPending: true}, {StartupPending: true}, {ContinuationPending: true}} {
		require.ErrorIs(t, s.Close("finish", fact), domain.ErrWorkTransition)
		require.Equal(t, domain.WorkWorking, s.State)
		require.Empty(t, s.CloseRequestReference)
	}

	require.NoError(t, s.Close("finish", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkStopping, s.State, "a running Work is stopped before it closes")
	require.Equal(t, "finish", s.CloseRequestReference)
	require.Equal(t, []string{"finish"}, port.stops())
	require.False(t, s.KeptCloseApplicable(), "the kept close applies once the Work has stopped")
	require.ErrorIs(t, s.RequireReadyProcess(), domain.ErrWorkTransition)
	require.NoError(t, s.Close("retry", domain.ClosureFacts{}))
	require.Equal(t, "finish", s.CloseRequestReference, "a repeated close keeps the first request")

	exited(t, s, domain.HarnessExit{})
	released(t, s)
	require.Equal(t, domain.WorkStopped, s.State)
	require.True(t, s.KeptCloseApplicable())
	require.Equal(t, []domain.WorkExecutionEnded{{WorkID: s.ID}}, recorded[domain.WorkExecutionEnded](s), "once compute is released the stop is told, so the kept close applies")
	require.ErrorIs(t, s.Resume(admission(s)), domain.ErrWorkTransition, "a Work being closed is not resumed")

	require.NoError(t, s.Close(s.CloseRequestReference, domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosed, s.State)
	require.Equal(t, "finish", s.CloseRequestReference)
	require.False(t, s.KeptCloseApplicable())
}

// A stopped or failed Work closes directly; a pending Resume or Restart, or
// input the stopped process still holds, prevents it.
func TestCloseAppliesDirectlyToAStoppedWork(t *testing.T) {
	s := newSession(t)
	port := follow(s)
	ready(t, s)
	require.NoError(t, s.Stop("stop", false))
	exited(t, s, domain.HarnessExit{})
	released(t, s)
	require.Equal(t, domain.WorkStopped, s.State)
	require.ErrorIs(t, s.Close("archive", domain.ClosureFacts{ContinuationPending: true}), domain.ErrWorkTransition)
	require.NoError(t, s.Close("archive", domain.ClosureFacts{InputPending: true}), "input waiting for a Resume is dropped with the Work")
	require.Equal(t, domain.WorkClosed, s.State)
	require.Equal(t, []string{"stop"}, port.stops(), "a stopped Work needs no stop")
}
