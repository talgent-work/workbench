package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Ordinary input and responses share one acceptance condition, the current
// lifecycle: a running Turn and a pending Required Response do not reject
// input, which then becomes Steer. Waiting is the Turn held by the
// user's answer; input keeps it Waiting and only an accepted reply ends it.
func TestInputIsAcceptedDuringATurnAndBehindARequiredResponse(t *testing.T) {
	session := newSession(t)
	require.NoError(t, session.AcceptInput(false), "a starting Work keeps input for its Harness")
	require.Equal(t, domain.WorkStarting, session.State)
	ready(t, session)

	origin := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, origin))
	require.NoError(t, session.ApplyResponseRequirement(2, true, origin))
	require.True(t, session.Waiting())
	require.NoError(t, session.AcceptInput(false), "input joins the Turn behind the pending request")
	require.True(t, session.Waiting(), "the request still needs a human")
	require.Equal(t, origin, session.HarnessProcess.WaitingSince, "the waiting interval keeps running")
	require.NoError(t, session.AcceptResponse(false))
	require.False(t, session.Waiting(), "the answer releases the held input")

	// Accepting a reply ends Waiting even while another request remains; the
	// next interaction feedback restores it.
	require.NoError(t, session.ApplyResponseRequirement(3, true, origin))
	require.NoError(t, session.AcceptResponse(false))
	require.False(t, session.Waiting())
	require.NoError(t, session.ApplyResponseRequirement(4, true, origin.Add(time.Minute)))
	require.True(t, session.Waiting())

	// A replayed identity changes nothing.
	require.NoError(t, session.AcceptResponse(true))
	require.Equal(t, origin.Add(time.Minute), session.HarnessProcess.WaitingSince)
	require.Equal(t, domain.WorkWorking, session.State, "Waiting is not a Work State")
}

// An ended Turn, whatever its outcome, ends Waiting and leaves the Work idle
// unless input awaits processing; accepted input or the executor's Turn
// activity report ends idleness, and a running Turn is never idle.
func TestAnEndedTurnLeavesTheWorkIdleUntilInputOrActivity(t *testing.T) {
	session := newSession(t)
	ready(t, session)
	origin := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	require.NoError(t, session.RecordIdleness(true, origin), "input awaiting processing")
	require.False(t, session.Idle())
	require.NoError(t, session.RecordIdleness(false, origin), "a ready Work with nothing to process")
	require.True(t, session.Idle())

	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, true, origin))
	require.False(t, session.Idle())
	require.True(t, session.Waiting())
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 2}, true, false, origin.Add(time.Minute)))
	require.False(t, session.Waiting(), "the Turn's end ends Waiting")
	require.False(t, session.Idle(), "buffered input is still to be processed")
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 3}, false, false, origin.Add(2*time.Minute)))
	require.Equal(t, origin.Add(2*time.Minute), session.HarnessProcess.IdleSince)
	require.NoError(t, session.RecordIdleness(false, origin.Add(3*time.Minute)))
	require.Equal(t, origin.Add(2*time.Minute), session.HarnessProcess.IdleSince, "a repeated fact keeps the origin")

	require.NoError(t, session.AcceptInput(false))
	require.False(t, session.Idle())
	require.NoError(t, session.RecordIdleness(false, origin.Add(4*time.Minute)))
	require.True(t, session.ObserveTurnActivity())
	require.False(t, session.Idle())
	require.False(t, session.ObserveTurnActivity(), "nothing changes for a Work that is not idle")
}

// A Work is stopped only between Turns: a user stop during a Turn is
// rejected and a retained end waits for the Turn.
func TestStopHappensBetweenTurns(t *testing.T) {
	session := newSession(t)
	ready(t, session)
	origin := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, origin))

	require.ErrorIs(t, session.Stop("user-stop", false), domain.ErrTurnActive)
	require.ErrorIs(t, session.Close("close", domain.ClosureFacts{}), domain.ErrTurnActive, "closing stops the process, so it waits for the Turn")
	require.Equal(t, domain.WorkWorking, session.State)

	require.NoError(t, session.ApplyResponseRequirement(2, true, origin))
	require.NoError(t, session.RequestExecutionEnd("end"))
	require.Equal(t, domain.WorkWorking, session.State, "the end waits for the Turn")
	require.Equal(t, "end", session.EndRequestReference)
	changed, err := session.RecoverExecution()
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, session.AcceptInput(false), "a Work being ended keeps input for its next start")
	require.True(t, session.Waiting(), "input does not overtake the end admitted first")

	require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 3}, false, false, origin.Add(time.Hour)))
	changed, err = session.RecoverExecution()
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WorkStopping, session.State)
	require.Equal(t, "end", session.StopRequestReference)
}

// Buffered input defers an idle timeout; an idle or Waiting Work stops after
// 30 continuous minutes, a Waiting Turn being cancelled first since it never
// ends by itself; recovery repeats the cancellation until it has.
func TestAnIdleOrWaitingWorkStopsAfterTheTimeout(t *testing.T) {
	origin := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	ready := func(t *testing.T) (*domain.AgentWork, *executionPort) {
		session := newSession(t)
		port := follow(session)
		ready(t, session)
		return session, port
	}
	t.Run("idle", func(t *testing.T) {
		session, _ := ready(t)
		require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1}, false, false, origin))
		expired, err := session.StopAfterTimeout(origin.Add(domain.WaitingTimeout), domain.BufferedWork{Input: true})
		require.NoError(t, err)
		require.False(t, expired, "buffered input is still to be processed")
		expired, err = session.StopAfterTimeout(origin.Add(domain.WaitingTimeout-time.Second), domain.BufferedWork{})
		require.NoError(t, err)
		require.False(t, expired)
		expired, err = session.StopAfterTimeout(origin.Add(domain.WaitingTimeout), domain.BufferedWork{})
		require.NoError(t, err)
		require.True(t, expired)
		require.Equal(t, domain.WorkStopping, session.State)
		require.Equal(t, domain.StopReasonWaitingTimeout, session.StopReason)
		require.False(t, session.Idle())
	})
	t.Run("Waiting", func(t *testing.T) {
		session, port := ready(t)
		require.NoError(t, session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, origin))
		require.False(t, session.TimeoutDue(origin.Add(time.Hour)), "a progressing Turn never times out")
		require.NoError(t, session.ApplyResponseRequirement(2, true, origin))
		expired, err := session.StopAfterTimeout(origin.Add(domain.WaitingTimeout), domain.BufferedWork{Reply: true})
		require.NoError(t, err)
		require.False(t, expired, "a buffered reply answers the request")
		expired, err = session.StopAfterTimeout(origin.Add(domain.WaitingTimeout), domain.BufferedWork{Input: true})
		require.NoError(t, err)
		require.True(t, expired, "input is held behind the request")
		require.Equal(t, domain.WorkStopping, session.State)
		require.Equal(t, 1, port.cancels(), "the held Turn is cancelled before the process is asked to exit")
		require.Equal(t, []string{session.StopRequestReference}, port.stops())
		_, err = session.RecoverExecution()
		require.NoError(t, err)
		stop, asked := intent[domain.WorkStopRequested](session)
		require.True(t, asked, "recovery passes the exit request again")
		require.True(t, stop.CancelWaitingTurn, "with the Turn's cancellation first, until the Turn ended")
	})
}
