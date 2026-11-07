package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Every Work but a closed one admits input, whether running, stopping,
// stopped or failed; only Closing and Closed refuse it.
func TestEveryWorkButAClosedOneAdmitsInput(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, session.AdmitInput(), "a starting Work admits input")
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
	require.NoError(t, session.AdmitInput())

	require.NoError(t, session.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkStopping, session.State)
	require.NoError(t, session.AdmitInput(), "the stop before a close admits input; closing drops it later")
	require.NoError(t, session.AcceptInput(false))
	require.Equal(t, domain.WorkStopping, session.State, "accepting input never starts or keeps a Work running")
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.NoError(t, session.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.True(t, session.Closed())
	require.ErrorIs(t, session.AdmitInput(), domain.ErrWorkTransition)
	require.ErrorIs(t, session.AcceptResponse(false), domain.ErrWorkTransition)
}

// A Stopped or Failed Work closes while input is still buffered, whether or
// not its compute is released: closing drops the buffered input instead of
// waiting for the release.
func TestStoppedOrFailedWorkClosesWithBufferedInputBeforeItsComputeRelease(t *testing.T) {
	ctx := context.Background()
	pending := domain.ClosureFacts{InputPending: true}

	failed := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, failed, port))
	require.NoError(t, failed.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, failed.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, failed.RecordFailure("harness crashed"))
	require.Equal(t, domain.WorkFailed, failed.State)
	require.False(t, failed.HarnessProcess.ComputeReclaimed)
	failed.Events.Drain()
	require.NoError(t, failed.Close(ctx, "close", pending, port))
	require.Equal(t, domain.WorkClosing, failed.State, "the close waits only for the release, not for the input")
	require.Contains(t, failed.Events.Drain(), domain.Event(domain.WorkClosingEntered{WorkID: failed.ID}), "entering Closing drops the buffered input")
	require.NoError(t, failed.RecordExit(ctx, domain.HarnessExit{ExitCode: 1}, port))
	require.NoError(t, failed.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkClosed, failed.State)

	stopped := &domain.AgentWork{ID: "stopped", State: domain.WorkStopped, Events: domain.NewEventCollection()}
	require.NoError(t, stopped.Close(ctx, "close", pending, &executionPort{}), "a Stopped Work with no confirmed release closes too")
	require.True(t, stopped.Closed())
	require.Contains(t, stopped.Events.Drain(), domain.Event(domain.WorkClosingEntered{WorkID: stopped.ID}))
}
