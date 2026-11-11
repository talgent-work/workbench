package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Only a Resume its Computer refused as unreachable marks a Stopped Work; a
// refusal for another reason spends the mark, and a reconnection asks for the
// Resume only while the Work is Stopped and input awaits, spending the mark
// otherwise.
func TestAResumeRefusedAsUnreachableIsMarkedForTheReconnection(t *testing.T) {
	work := &domain.AgentWork{State: domain.WorkStopped}
	require.False(t, work.RefuseResume(domain.ErrComputerFull), "another refusal marks nothing")
	require.True(t, work.RefuseResume(domain.ErrComputerUnreachable))
	require.True(t, work.ReachResumeDue)
	require.False(t, work.RefuseResume(domain.ErrComputerUnreachable), "a repeated refusal changes nothing")
	resume, changed := work.ResumeOnReconnection(true)
	require.True(t, resume)
	require.False(t, changed)
	require.True(t, work.RefuseResume(domain.ErrComputerFull), "a refusal for another reason spends the mark")
	require.False(t, work.ReachResumeDue)
	require.False(t, work.RefuseResume(domain.ErrComputerFull))

	failed := &domain.AgentWork{State: domain.WorkFailed}
	require.False(t, failed.RefuseResume(domain.ErrComputerUnreachable), "a Failed Work is restarted, never resumed")
}

// A reconnection that finds no input awaiting, or the Work no longer
// Stopped, spends the mark without asking for the Resume.
func TestAReconnectionWithoutInputSpendsTheMark(t *testing.T) {
	for name, work := range map[string]*domain.AgentWork{
		"no input":    {State: domain.WorkStopped, ReachResumeDue: true},
		"not stopped": {State: domain.WorkClosing, ReachResumeDue: true},
	} {
		t.Run(name, func(t *testing.T) {
			resume, changed := work.ResumeOnReconnection(name == "not stopped")
			require.False(t, resume)
			require.True(t, changed)
			require.False(t, work.ReachResumeDue)
			resume, changed = work.ResumeOnReconnection(true)
			require.False(t, resume || changed, "an unmarked Work is not resumed")
		})
	}
}

// Every admitted start clears the mark.
func TestAnAdmittedStartClearsTheReconnectionMark(t *testing.T) {
	session := newSession(t)
	ready(t, session)
	require.NoError(t, session.Stop("stop-1", false))
	exited(t, session, domain.HarnessExit{})
	released(t, session)
	require.True(t, session.RefuseResume(domain.ErrComputerUnreachable))
	require.NoError(t, session.Resume(admission(session)))
	require.Equal(t, domain.WorkStarting, session.State)
	require.False(t, session.ReachResumeDue)
}

// A Managed Work stopped beyond reach while input awaits is marked, since
// that input awaits its machine's return; without input, or stopped by a
// person before, it is not.
func TestAWorkStoppedBeyondReachWithInputAwaitsTheReconnection(t *testing.T) {
	for name, want := range map[string]bool{"input awaits": true, "no input": false, "stopping": false} {
		t.Run(name, func(t *testing.T) {
			work, _ := runningHarnessWork(t, domain.HarnessChoice{})
			if name == "stopping" {
				require.NoError(t, work.Stop("user-stop", false))
			}
			lost := time.Now()
			_, err := work.RecordDisconnection(lost, false)
			require.NoError(t, err)
			stopped := work.StopBeyondReach(lost.Add(domain.DisconnectionTimeout), name != "no input")
			require.True(t, stopped)
			require.Equal(t, domain.WorkStopped, work.State)
			require.Equal(t, want, work.ReachResumeDue)
		})
	}
}

// A retry of an owed Resume that fails without an answer is counted; the
// failure that reaches ReachResumeAttempts spends the mark, so the Work's
// recovery stops retrying it. A refusal, or a new mark, starts the count
// again.
func TestAnOwedResumeIsSpentAfterItsRetriesKeepFailing(t *testing.T) {
	work := &domain.AgentWork{State: domain.WorkStopped}
	require.False(t, work.RecordResumeRetryFailure(), "an unmarked Work counts nothing")
	require.True(t, work.RefuseResume(domain.ErrComputerUnreachable))
	for attempt := 1; attempt < domain.ReachResumeAttempts; attempt++ {
		require.False(t, work.RecordResumeRetryFailure(), "attempt %d is below the ceiling", attempt)
		require.True(t, work.ReachResumeDue)
	}
	require.Equal(t, domain.ReachResumeAttempts-1, work.ReachResumeFailures)
	require.True(t, work.RecordResumeRetryFailure(), "the failure at the ceiling spends the mark")
	require.False(t, work.ReachResumeDue)
	require.Zero(t, work.ReachResumeFailures)

	require.True(t, work.RefuseResume(domain.ErrComputerUnreachable))
	require.False(t, work.RecordResumeRetryFailure())
	require.True(t, work.RefuseResume(domain.ErrComputerFull), "a refusal spends the mark and its failures")
	require.Zero(t, work.ReachResumeFailures)
}
