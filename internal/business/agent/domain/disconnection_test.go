package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// turningWork is a Managed Work whose Harness runs a Turn.
func turningWork(t *testing.T) (*domain.AgentWork, *executionPort) {
	t.Helper()
	work, port := runningHarnessWork(t, domain.HarnessChoice{})
	require.NoError(t, work.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, time.Now()))
	require.True(t, work.HarnessProcess.Turn.Active)
	return work, port
}

// A Disconnection is a fact with an origin: it changes no State, keeps its
// first origin, never precedes the current start and ends when observation
// returns.
func TestADisconnectionIsRecordedWithoutChangingState(t *testing.T) {
	work, _ := turningWork(t)
	lost := time.Now()
	changed, err := work.RecordDisconnection(lost, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WorkWorking, work.State)
	require.True(t, work.Disconnected())
	changed, err = work.RecordDisconnection(lost, false)
	require.NoError(t, err)
	require.False(t, changed, "a repeated fact keeps the Disconnection's origin")
	changed, err = work.RecordDisconnection(lost.Add(-time.Second), false)
	require.NoError(t, err)
	require.False(t, changed, "an earlier loss does not move the origin back")
	require.Equal(t, lost.UTC(), work.DisconnectedSince)
	require.NoError(t, work.Validate())

	require.True(t, work.EndDisconnection())
	require.False(t, work.EndDisconnection())
	require.Equal(t, domain.WorkWorking, work.State)

	changed, err = work.RecordDisconnection(work.HarnessProcess.StartingSince.Add(-time.Hour), false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, work.HarnessProcess.StartingSince, work.DisconnectedSince, "a Disconnection never precedes the current start")

	_, err = work.RecordDisconnection(time.Time{}, false)
	require.ErrorIs(t, err, domain.ErrInvalidWork)

	queued, _ := queuedWork(t, time.Now())
	changed, err = queued.RecordDisconnection(time.Now(), true)
	require.NoError(t, err)
	require.False(t, changed, "a start waiting for its Desk has no environment to lose")
	require.False(t, queued.ObservesEnvironment(true))
	// Its execution was seated since: the admission's waiting no longer
	// decides, and the seated start's environment can be lost.
	require.True(t, queued.ObservesEnvironment(false))
	changed, err = queued.RecordDisconnection(time.Now(), false)
	require.NoError(t, err)
	require.True(t, changed, "a seated start's environment is observed, whatever its admission")
}

// A loss newer than the Disconnection under way follows a return of reach
// this Work never saw: the machine's fence restarted its count, so a new
// Disconnection starts there and the timeout counts from it.
func TestANewerLossStartsANewDisconnection(t *testing.T) {
	work, _ := turningWork(t)
	first := time.Now()
	_, err := work.RecordDisconnection(first, false)
	require.NoError(t, err)
	again := first.Add(4 * time.Minute)
	changed, err := work.RecordDisconnection(again, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, again.UTC(), work.DisconnectedSince)

	stopped := work.StopBeyondReach(first.Add(domain.DisconnectionTimeout), false)
	require.False(t, stopped, "the machine has not fenced the new loss yet")
	require.Equal(t, domain.WorkWorking, work.State)
	stopped = work.StopBeyondReach(again.Add(domain.DisconnectionTimeout), false)
	require.True(t, stopped)
}

// A short blip changes nothing; past the Disconnection timeout a Working
// Managed Work is Stopped with the disconnection reason whatever its Turn,
// its Desk leaves and nothing waits for a release confirmation.
func TestStopBeyondReachStopsAManagedWorkPastTheTimeout(t *testing.T) {
	work, port := turningWork(t)
	work.Events.Drain()
	lost := time.Now()
	_, err := work.RecordDisconnection(lost, false)
	require.NoError(t, err)

	stopped := work.StopBeyondReach(lost.Add(domain.DisconnectionTimeout-time.Second), false)
	require.False(t, stopped, "a Disconnection under the timeout changes nothing")
	require.Equal(t, domain.WorkWorking, work.State)

	stopped = work.StopBeyondReach(lost.Add(domain.DisconnectionTimeout), false)
	require.True(t, stopped)
	require.Equal(t, domain.WorkStopped, work.State)
	require.Equal(t, domain.StopReasonDisconnected, work.StopReason)
	require.NotEmpty(t, work.StopRequestReference)
	require.False(t, work.Disconnected(), "the Disconnection ends with the run")
	require.Empty(t, port.stops(), "nothing asks the unreachable machine to stop")
	require.Equal(t, 1, port.retires(), "the platform's record of the fenced run is retired")
	require.Zero(t, port.reclaims(), "the fence vouches for release; no compute is reclaimed")
	require.Len(t, recorded[domain.WorkExecutionEnded](work), 1, "the stop ends once compute is released")
	require.NoError(t, work.Validate())
	require.NoError(t, work.AdmitResume(), "Resume continues the conversation")

	stopped = work.StopBeyondReach(lost.Add(time.Hour), false)
	require.False(t, stopped, "a repeated sweep changes nothing")
}

// A Stopping Work keeps its admitted stop's reason; a Closing one becomes
// Closed.
func TestStopBeyondReachEndsStoppingAndClosingManagedWorks(t *testing.T) {
	work, _ := runningHarnessWork(t, domain.HarnessChoice{})
	require.NoError(t, work.Stop("user-stop", false))
	require.Equal(t, domain.WorkStopping, work.State)
	lost := time.Now()
	_, err := work.RecordDisconnection(lost, false)
	require.NoError(t, err)
	stopped := work.StopBeyondReach(lost.Add(domain.DisconnectionTimeout), false)
	require.True(t, stopped)
	require.Equal(t, domain.WorkStopped, work.State)
	require.Equal(t, domain.StopReasonUserStopped, work.StopReason)
	require.Equal(t, "user-stop", work.StopRequestReference)

	// A failed Work closed while its process may still run is Closing until
	// its release, which the fence stands in for.
	closing, _ := runningHarnessWork(t, domain.HarnessChoice{})
	require.NoError(t, closing.RecordFailure("model unavailable"))
	require.NoError(t, closing.Close("close", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosing, closing.State)
	_, err = closing.RecordDisconnection(time.Now(), false)
	require.NoError(t, err)
	stopped = closing.StopBeyondReach(time.Now().Add(domain.DisconnectionTimeout), false)
	require.True(t, stopped)
	require.Equal(t, domain.WorkClosed, closing.State)
}

// During a Disconnection a stop takes effect at once whatever the Turn,
// making the Managed Work Stopped without a release confirmation.
func TestStopDuringADisconnectionIsAdmittedAtOnce(t *testing.T) {
	work, port := turningWork(t)
	require.ErrorIs(t, work.Stop("stop", false), domain.ErrTurnActive, "while observed, a running Turn refuses a stop")
	_, err := work.RecordDisconnection(time.Now(), false)
	require.NoError(t, err)
	require.NoError(t, work.Stop("stop", false))
	require.Equal(t, domain.WorkStopped, work.State)
	require.Equal(t, domain.StopReasonUserStopped, work.StopReason)
	require.Equal(t, "stop", work.StopRequestReference)
	require.Empty(t, port.stops())
	require.True(t, work.HarnessProcess.ComputeReclaimed)
}

// During a Disconnection a close takes effect at once whatever the Turn: a
// Working Managed Work becomes Closed, while input still awaiting
// processing refuses it as on any running Work.
func TestCloseDuringADisconnectionIsAdmittedAtOnce(t *testing.T) {
	work, _ := turningWork(t)
	require.ErrorIs(t, work.Close("close", domain.ClosureFacts{}), domain.ErrTurnActive)
	_, err := work.RecordDisconnection(time.Now(), false)
	require.NoError(t, err)
	require.ErrorIs(t, work.Close("close", domain.ClosureFacts{InputPending: true}), domain.ErrInputPending)
	require.Equal(t, domain.WorkWorking, work.State)

	work.Events.Drain()
	require.NoError(t, work.Close("close", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosed, work.State)
	require.Equal(t, "close", work.CloseRequestReference)
	require.Equal(t, domain.StopReasonCompleted, work.StopReason)
	var closed bool
	for _, event := range work.Events.Drain() {
		if _, ok := event.(domain.WorkClosedEvent); ok {
			closed = true
		}
	}
	require.True(t, closed, "WorkClosed is recorded")
	require.NoError(t, work.Close("close", domain.ClosureFacts{}), "a repeated close confirms it")

	stopping, _ := runningHarnessWork(t, domain.HarnessChoice{})
	require.NoError(t, stopping.Stop("stop", false))
	_, err = stopping.RecordDisconnection(time.Now(), false)
	require.NoError(t, err)
	require.NoError(t, stopping.Close("close", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosed, stopping.State, "a Stopping Work is closed without its release confirmation")
}
