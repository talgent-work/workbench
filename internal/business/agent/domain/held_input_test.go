package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Every Work but a closed one admits input, whether running, stopping,
// stopped or failed; only Closing and Closed refuse it.
func TestEveryWorkButAClosedOneAdmitsInput(t *testing.T) {
	session := newSession(t)
	require.NoError(t, session.AdmitInput(), "a starting Work admits input")
	ready(t, session)
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
	require.NoError(t, session.AdmitInput())

	require.NoError(t, session.Close("close", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkStopping, session.State)
	require.NoError(t, session.AdmitInput(), "the stop before a close admits input; closing drops it later")
	require.NoError(t, session.AcceptInput(false))
	require.Equal(t, domain.WorkStopping, session.State, "accepting input never starts or keeps a Work running")
	exited(t, session, domain.HarnessExit{})
	released(t, session)
	require.NoError(t, session.Close("close", domain.ClosureFacts{}))
	require.True(t, session.Closed())
	require.ErrorIs(t, session.AdmitInput(), domain.ErrWorkTransition)
	require.ErrorIs(t, session.AcceptResponse(false), domain.ErrWorkTransition)
}

// A Failed Work closes while input is still buffered and before its compute
// is released: closing drops the buffered input instead of waiting for the
// release.
func TestFailedWorkClosesWithBufferedInputBeforeItsComputeRelease(t *testing.T) {
	failed := newSession(t)
	ready(t, failed)
	require.NoError(t, failed.RecordFailure("harness crashed"))
	require.Equal(t, domain.WorkFailed, failed.State)
	require.False(t, failed.HarnessProcess.ComputeReclaimed)
	failed.Events.Drain()
	require.NoError(t, failed.Close("close", domain.ClosureFacts{InputPending: true}))
	require.Equal(t, domain.WorkClosing, failed.State, "the close waits only for the release, not for the input")
	require.Contains(t, failed.Events.Drain(), domain.Event(domain.WorkClosingEntered{WorkID: failed.ID}), "entering Closing drops the buffered input")
	exited(t, failed, domain.HarnessExit{ExitCode: 1})
	released(t, failed)
	require.Equal(t, domain.WorkClosed, failed.State)
}

// A Stopped Work whose exited process is not yet released closes while input
// is still buffered, dropping it.
func TestStoppedWorkClosesWithBufferedInputBeforeItsComputeRelease(t *testing.T) {
	stopped := &domain.AgentWork{ID: "stopped", State: domain.WorkStopped, HarnessProcess: &domain.HarnessProcess{Exited: true, StartingSince: time.Now()}, Events: domain.NewEventCollection()}
	require.NoError(t, stopped.Close("close", domain.ClosureFacts{InputPending: true}))
	require.Equal(t, domain.WorkClosing, stopped.State, "the close waits only for the release, not for the input")
	require.Contains(t, stopped.Events.Drain(), domain.Event(domain.WorkClosingEntered{WorkID: stopped.ID}), "entering Closing drops the buffered input")
	released(t, stopped)
	require.Equal(t, domain.WorkClosed, stopped.State)
}
