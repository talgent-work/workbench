package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// cloudTurningWork is a Cloud Work whose Harness runs a Turn.
func cloudTurningWork(t *testing.T) (*domain.AgentWork, *executionPort) {
	t.Helper()
	ctx := context.Background()
	work, port := newSession(t), &executionPort{}
	require.NoError(t, recoverStartup(ctx, work, port))
	require.NoError(t, work.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, work.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, work.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, time.Now()))
	require.False(t, work.OnManagedComputer())
	return work, port
}

// A Cloud Work's start records no Disconnection: the attachment it is
// observed by is what the start establishes, and its Startup Deadline
// governs it. Once it works, a loss counts.
func TestACloudStartRecordsNoDisconnection(t *testing.T) {
	ctx := context.Background()
	work, port := newSession(t), &executionPort{}
	require.NoError(t, recoverStartup(ctx, work, port))
	require.Equal(t, domain.WorkStarting, work.State)
	require.True(t, work.ObservesEnvironment())
	changed, err := work.RecordDisconnection(time.Now())
	require.NoError(t, err)
	require.False(t, changed)
	require.False(t, work.Disconnected())

	require.NoError(t, work.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, work.ConfirmStarted(ctx, time.Now(), nil))
	changed, err = work.RecordDisconnection(time.Now())
	require.NoError(t, err)
	require.True(t, changed, "a Working Cloud Work's loss is its Disconnection")
}

// A return of Dispatcher's attachment ends the Disconnection of the loss it
// follows, never a later one it precedes; it is compared as the loss is
// kept, never before the current start.
func TestAReturnEndsOnlyTheDisconnectionItFollows(t *testing.T) {
	work, _ := cloudTurningWork(t)
	first := time.Now()
	_, err := work.RecordDisconnection(first)
	require.NoError(t, err)
	require.True(t, work.EndDisconnectionOf(first))
	require.False(t, work.Disconnected())
	require.False(t, work.EndDisconnectionOf(first), "nothing is under way")

	later := first.Add(time.Minute)
	_, err = work.RecordDisconnection(later)
	require.NoError(t, err)
	require.False(t, work.EndDisconnectionOf(first), "a late return of an earlier loss keeps the later one")
	require.Equal(t, later.UTC(), work.DisconnectedSince)
	require.True(t, work.EndDisconnectionOf(later.Add(time.Second)))

	early := work.HarnessProcess.StartingSince.Add(-time.Hour)
	_, err = work.RecordDisconnection(early)
	require.NoError(t, err)
	require.True(t, work.EndDisconnectionOf(early), "a loss before the start counts from the start on return too")
	require.Equal(t, domain.WorkWorking, work.State)
}

// Past the Disconnection timeout a Working Cloud Work is stopped with the
// disconnection reason whatever its Turn, by reclaiming its sandbox: it is
// Stopping, shows its Disconnection, and becomes Stopped on the release
// confirmation, which vouches for the exit nothing reported.
func TestStopBeyondReachReclaimsACloudWorksSandbox(t *testing.T) {
	ctx := context.Background()
	work, port := cloudTurningWork(t)
	lost := time.Now()
	_, err := work.RecordDisconnection(lost)
	require.NoError(t, err)
	stopped, err := work.StopBeyondReach(ctx, lost.Add(domain.DisconnectionTimeout-time.Second), false, port)
	require.NoError(t, err)
	require.False(t, stopped, "a short loss changes nothing")
	require.Equal(t, domain.WorkWorking, work.State)

	stopped, err = work.StopBeyondReach(ctx, lost.Add(domain.DisconnectionTimeout), false, port)
	require.NoError(t, err)
	require.True(t, stopped)
	require.Equal(t, domain.WorkStopping, work.State)
	require.Equal(t, domain.StopReasonDisconnected, work.StopReason)
	require.True(t, work.Disconnected(), "the Disconnection stays shown until the release")
	require.False(t, work.EndDisconnection(), "a return after the reclamation keeps it shown")
	require.False(t, work.EndDisconnectionOf(lost))
	require.True(t, work.Disconnected())
	require.Equal(t, 1, port.reclaims)
	require.Empty(t, port.stops, "nothing asks the unreachable process to stop")
	require.Zero(t, port.retires)
	require.NoError(t, work.Validate())

	stopped, err = work.StopBeyondReach(ctx, lost.Add(time.Hour), false, port)
	require.NoError(t, err)
	require.False(t, stopped, "a repeated sweep reclaims through recovery, not again here")
	_, err = work.RecoverExecution(ctx, port)
	require.NoError(t, err)
	require.Equal(t, 2, port.reclaims, "recovery repeats the reclamation until it is confirmed")
	require.Empty(t, port.stops)

	work.Events.Drain()
	require.NoError(t, work.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, work.State)
	require.True(t, work.HarnessProcess.Exited)
	require.False(t, work.Disconnected())
	require.Len(t, deskReleases(work.Events.Drain()), 1)
	require.NoError(t, work.AdmitResume(), "Resume continues the conversation")
}

// A sandbox release is no exit while the Work still reaches its process.
func TestAReleaseVouchesForNoExitWithoutAStopBeyondReach(t *testing.T) {
	ctx := context.Background()
	work, port := cloudTurningWork(t)
	require.NoError(t, work.ObserveTurn(domain.TurnObservation{Sequence: 2, Completed: true}, false, false, time.Now()))
	require.NoError(t, work.Stop(ctx, "stop", port))
	require.Equal(t, domain.WorkStopping, work.State)
	require.NoError(t, work.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopping, work.State, "the stop waits for the process's exit")
	require.False(t, work.HarnessProcess.Exited)
}

// During a Cloud Disconnection a stop is admitted whatever the Turn and
// reclaims the sandbox; a close is admitted too, kept while the stop
// completes, and a Closing Work past the timeout is reclaimed and Closed.
func TestStopAndCloseDuringACloudDisconnectionReclaimTheSandbox(t *testing.T) {
	ctx := context.Background()
	work, port := cloudTurningWork(t)
	require.ErrorIs(t, work.Stop(ctx, "stop", port), domain.ErrTurnActive)
	_, err := work.RecordDisconnection(time.Now())
	require.NoError(t, err)
	require.NoError(t, work.Stop(ctx, "stop", port))
	require.Equal(t, domain.WorkStopping, work.State)
	require.Equal(t, domain.StopReasonUserStopped, work.StopReason)
	require.Equal(t, 1, port.reclaims)
	require.Empty(t, port.stops)
	require.NoError(t, work.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, work.State)

	closing, port := cloudTurningWork(t)
	_, err = closing.RecordDisconnection(time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, closing.Close(ctx, "close", domain.ClosureFacts{InputPending: true}, port), domain.ErrInputPending)
	require.NoError(t, closing.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkStopping, closing.State)
	require.Equal(t, "close", closing.CloseRequestReference)
	require.Equal(t, 1, port.reclaims)
	require.NoError(t, closing.ConfirmComputeReclaimed())
	require.True(t, closing.KeptCloseApplicable())
	require.NoError(t, closing.Close(ctx, closing.CloseRequestReference, domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkClosed, closing.State, "the released run leaves nothing to wait for")

	failed, port := cloudTurningWork(t)
	require.NoError(t, failed.RecordFailure("model unavailable"))
	require.NoError(t, failed.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkClosing, failed.State)
	lost := time.Now()
	_, err = failed.RecordDisconnection(lost)
	require.NoError(t, err)
	stopped, err := failed.StopBeyondReach(ctx, lost.Add(domain.DisconnectionTimeout), false, port)
	require.NoError(t, err)
	require.True(t, stopped)
	require.Equal(t, domain.WorkClosing, failed.State)
	require.NoError(t, failed.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkClosed, failed.State)
}
