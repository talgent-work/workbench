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

// A kept close waits, without error, for a failed Work's compute release when
// input is still buffered, and a repeated close confirms it.
func TestKeptCloseWaitsForComputeReleaseOfAFailedWork(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Completed: true}, false, false, time.Now()))
	require.NoError(t, session.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.NoError(t, session.RecordFailure("harness crashed"))
	require.Equal(t, domain.WorkFailed, session.State)
	pending := domain.ClosureFacts{InputPending: true}
	require.NoError(t, session.Close(ctx, "close", pending, port), "the kept close waits for the compute release")
	require.Equal(t, domain.WorkFailed, session.State)
	require.NoError(t, session.Close(ctx, "another", pending, port), "any repeated close confirms the kept one")
	require.Equal(t, "close", session.CloseRequestReference)
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 1}, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.NoError(t, session.Close(ctx, "close", pending, port))
	require.True(t, session.Closed(), "input left after the release is dropped with the buffer")
}
