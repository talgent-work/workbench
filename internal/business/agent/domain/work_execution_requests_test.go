package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A start's admission records its start request for its execution, carrying
// the Desk and admission time it begins from.
func TestAdmittedStartRecordsItsStartIntent(t *testing.T) {
	work := newSession(t)
	starts := recorded[domain.WorkStartAdmitted](work)
	require.Len(t, starts, 1)
	require.Equal(t, work.ID, starts[0].WorkID)
	require.Equal(t, "desk-test", starts[0].DeskID)
	require.Equal(t, work.HarnessProcess.StartingSince, starts[0].StartAdmittedAt)
	require.False(t, starts[0].Waiting, "a Desk seated at admission begins seated")
}

// An admitted stop records the request that the process exit; the
// execution's exit then records its retirement request. ExecutionRequests rebuilds
// both from the Work's facts, so a lost event is passed again.
func TestStopRecordsItsExitRequestAndExitItsRetirement(t *testing.T) {
	work := newSession(t)
	ready(t, work)
	work.Events.Drain()
	require.NoError(t, work.Stop("stop-1", false))
	stops := recorded[domain.WorkStopRequested](work)
	require.Len(t, stops, 1)
	require.Equal(t, domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: work.HarnessProcess.StartingSince, StopRequestID: "stop-1"}, stops[0])
	require.Equal(t, "stop-1", stopRequest(work), "the exit request is rebuilt until the process ended")

	exited(t, work, domain.HarnessExit{})
	releases := recorded[domain.WorkComputeReleaseRequested](work)
	require.Len(t, releases, 1)
	require.Equal(t, domain.RetiredByExit, releases[0].Cause)
	require.Empty(t, stopRequest(work), "an ended process is not asked to exit again")
	require.Equal(t, domain.RetiredByExit, releaseCause(work), "the retirement request is rebuilt until its release is confirmed")

	released(t, work)
	require.Equal(t, domain.WorkStopped, work.State)
	_, starting := executionRequest[domain.WorkStartAdmitted](work)
	require.False(t, starting, "a released start asks for nothing")
}

// Each report ending a start records the retirement request for the cause
// it reports.
func TestEachEndingReportRecordsItsRetirementCause(t *testing.T) {
	for name, tc := range map[string]struct {
		end   func(*testing.T, *domain.AgentWork)
		cause domain.RetirementCause
	}{
		"exit":      {func(t *testing.T, w *domain.AgentWork) { exited(t, w, domain.HarnessExit{ExitCode: 1}) }, domain.RetiredByExit},
		"rejection": {rejected, domain.RetiredByRejection},
		"loss":      {func(t *testing.T, w *domain.AgentWork) { lost(t, w, "gone") }, domain.RetiredByLoss},
		"abandonment": {func(t *testing.T, w *domain.AgentWork) {
			missedDeadline(t, w, "Startup Deadline reached before the Harness started.")
			abandoned(t, w)
		}, domain.RetiredByAbandonment},
	} {
		t.Run(name, func(t *testing.T) {
			work := newSession(t)
			work.Events.Drain()
			tc.end(t, work)
			releases := recorded[domain.WorkComputeReleaseRequested](work)
			require.Len(t, releases, 1)
			require.Equal(t, tc.cause, releases[0].Cause)
			require.Equal(t, work.HarnessProcess.StartingSince, releases[0].StartAdmittedAt)
			require.False(t, work.HoldsProcess())
		})
	}
}

// A report applies only to the start it concerns, once; anything that is not
// a report changes nothing.
func TestReportsApplyOnlyToTheirStart(t *testing.T) {
	work := newSession(t)
	start := work.HarnessProcess.StartingSince
	changed, err := work.ApplyExecutionReport(domain.ExecutionReady{WorkID: work.ID, StartAdmittedAt: start.Add(-time.Second)})
	require.NoError(t, err)
	require.False(t, changed, "an earlier start's report changes nothing")
	require.Equal(t, domain.WorkStarting, work.State)

	changed, err = work.ApplyExecutionReport(domain.ExecutionReady{WorkID: work.ID, StartAdmittedAt: start})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WorkWorking, work.State)
	changed, err = work.ApplyExecutionReport(domain.ExecutionReady{WorkID: work.ID, StartAdmittedAt: start})
	require.NoError(t, err)
	require.False(t, changed, "a repeated report changes nothing")

	changed, err = work.ApplyExecutionReport(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: start})
	require.NoError(t, err)
	require.False(t, changed, "a request is not a report")
}

// A release reported before the current start's process ended confirms
// nothing: the process keeps the Work until its exit.
func TestAReleaseBeforeTheExitDoesNotReleaseTheCurrentProcess(t *testing.T) {
	work := newSession(t)
	ready(t, work)
	require.NoError(t, work.Stop("stop-1", false))
	released(t, work)
	require.False(t, work.HarnessProcess.ComputeReclaimed)
	require.Equal(t, domain.WorkStopping, work.State)
	require.True(t, work.HoldsProcess())
}
