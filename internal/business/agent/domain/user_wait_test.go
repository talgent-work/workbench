package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestUserWaitOriginAndStopReasonSurviveRepeatedFacts(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}

	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	origin := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 999, Active: true}, false, false, origin))
	require.NoError(t, session.ApplyResponseRequirement(1000, true, origin))
	require.NoError(t, session.ApplyResponseRequirement(1000, true, origin.Add(20*time.Minute)))
	require.Equal(t, origin, session.HarnessProcess.WaitingSince)
	require.NoError(t, session.ApplyResponseRequirement(1000, false, origin), "an exact replay is ignored without an execution fence")
	require.Equal(t, origin, session.HarnessProcess.WaitingSince)
	changed, err := session.StopAfterTimeout(ctx, origin.Add(30*time.Minute-time.Nanosecond), domain.BufferedWork{}, port)
	require.NoError(t, err)
	require.False(t, changed)
	require.True(t, session.Waiting())
	changed, err = session.StopAfterTimeout(ctx, origin.Add(30*time.Minute), domain.BufferedWork{}, port)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WorkStopping, session.State)
	require.Equal(t, domain.StopReasonWaitingTimeout, session.StopReason)
	stop := session.StopRequestReference
	require.NotEmpty(t, stop)
	require.NoError(t, session.Stop(ctx, stop, port))
	require.Equal(t, domain.StopReasonWaitingTimeout, session.StopReason, "retry cannot relabel a policy stop as user stop")
	require.NoError(t, session.ApplyResponseRequirement(1000, false, origin.Add(time.Hour)))
	require.Equal(t, origin, session.HarnessProcess.WaitingSince, "a stopped Work still owes the answer")
	require.Equal(t, domain.WorkStopping, session.State)
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, session.State)
	require.Equal(t, domain.StopReasonWaitingTimeout, session.StopReason)
	denied := admission(session)
	denied.Seat = domain.ComputerSeat{}
	require.ErrorIs(t, session.Resume(ctx, denied, port), domain.ErrCapacityUnavailable)
	require.Equal(t, domain.StopReasonWaitingTimeout, session.StopReason)
	require.NoError(t, session.Resume(ctx, admission(session), port))
	require.Empty(t, session.StopReason)
	require.Empty(t, session.StopRequestReference)
	require.Equal(t, origin, session.HarnessProcess.WaitingSince, "the unanswered question survives the new start")
	require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, session.RecordIdleness(false, origin.Add(2*time.Hour)))
	require.True(t, session.Idle(), "no Turn is paused, so the resumed Work is idle")
	require.False(t, session.TimeoutDue(origin.Add(2*time.Hour)), "the carried Waiting is not measured")
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, origin.Add(3*time.Hour)))
	require.False(t, session.Waiting(), "a new Turn is no longer paused")
}

func TestExecutionEndAndNaturalExitRemainDistinctFromUserStop(t *testing.T) {
	for _, kind := range []string{"end running", "end starting", "user"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			session := newSession(t)
			port := &executionPort{}

			require.NoError(t, recoverStartup(ctx, session, port))
			if kind == "end starting" {
				require.NoError(t, session.RequestExecutionEnd(ctx, "end", port))
			}
			require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
			require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
			switch kind {
			case "end running":
				require.NoError(t, session.RequestExecutionEnd(ctx, "end", port))
			case "end starting":
				_, err := session.RecoverExecution(ctx, port)
				require.NoError(t, err)
			case "user":
				require.NoError(t, session.Stop(ctx, "user-stop", port))
			}
			require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
			expected := domain.StopReasonCompleted
			if kind == "user" {
				expected = domain.StopReasonUserStopped
			}
			require.Equal(t, expected, session.StopReason)
		})
	}
}

// Intent (real): under the Lifecycle observation rule an exit without an
// admitted stop, or before readiness, is a failure keeping its code; an exit
// after readiness with an admitted stop is normal whatever its code.
func TestExitIsClassifiedByReadinessAndAdmittedStop(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ready, stop   bool
		code          int
		state         domain.WorkState
		failureReason string
	}{
		{"stopped with a nonzero code", true, true, 143, domain.WorkStopping, ""},
		{"exited by itself", true, false, 0, domain.WorkFailed, "native_process_exited_unexpectedly (exit code 0)"},
		{"exited before readiness", false, false, 1, domain.WorkFailed, "native_initialization_failed (exit code 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			session := newSession(t)
			port := &executionPort{}
			require.NoError(t, recoverStartup(ctx, session, port))
			if tc.ready {
				require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
				require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))
			}
			if tc.stop {
				require.NoError(t, session.Stop(ctx, "stop", port))
			}
			reason := ""
			if !tc.ready {
				reason = "native_initialization_failed"
			}
			require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: tc.code, Reason: reason}, port))
			require.Equal(t, tc.state, session.State)
			require.Equal(t, tc.failureReason, session.FailureReason)
			require.False(t, session.HoldsProcess())
			require.Equal(t, 1, port.reclaims)
		})
	}
}

// A failure recorded before the exit keeps explaining it and gains the exit
// code.
func TestExitAfterARecordedFailureKeepsItsCode(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	failed, err := session.FailAtStartupDeadline(ctx, time.Now().Add(domain.StartupDeadline+time.Minute), port)
	require.NoError(t, err)
	require.True(t, failed)
	prior := session.FailureReason
	require.NotEmpty(t, prior)
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 137}, port))
	require.Equal(t, prior+" (exit code 137)", session.FailureReason)
}

// A resumed Work that asks the forgotten question again restarts Waiting from
// the new question, even when the question's feedback precedes the Turn's
// start; until then the carried Waiting is never measured.
func TestAResumedWorkRestartsWaitingWhenItAsksAgain(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))
	origin := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, true, origin))
	expired, err := session.StopAfterTimeout(ctx, origin.Add(domain.WaitingTimeout), domain.BufferedWork{}, port)
	require.NoError(t, err)
	require.True(t, expired, "the forgotten question stops the Work")
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 2}, false, false, origin.Add(domain.WaitingTimeout)))
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, origin, session.HarnessProcess.WaitingSince, "the cancelled Turn's end after the stop keeps Waiting")
	require.NoError(t, session.Resume(ctx, admission(session), port))
	require.False(t, session.TimeoutDue(origin.Add(time.Hour)), "a starting Work never times out")
	require.NoError(t, session.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(ctx, time.Now(), nil))
	asked := origin.Add(2 * time.Hour)
	require.NoError(t, session.ApplyResponseRequirement(1, true, asked))
	require.Equal(t, origin, session.HarnessProcess.WaitingSince, "no Turn runs yet")
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 2, Active: true}, false, true, asked))
	require.Equal(t, asked, session.HarnessProcess.WaitingSince, "the new Turn's question restarts Waiting")
	require.False(t, session.TimeoutDue(asked.Add(domain.WaitingTimeout-time.Second)))
	require.True(t, session.TimeoutDue(asked.Add(domain.WaitingTimeout)))
}
