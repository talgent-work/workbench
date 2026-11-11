//go:build integration

package agent_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Both readiness reports of the launched process make a Starting Work
// Working, whether it started afresh, resumed or restarted.
func TestReadinessReportsMakeAStartingWorkWorking(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	modes := []domain.ContextMode{domain.ContextFresh, domain.ContextContinue, domain.ContextRecover}
	works := map[domain.ContextMode]string{}
	for _, mode := range modes {
		works[mode] = seedWork(t, m.db, definition, workSeed{subject: string(mode), state: domain.WorkStarting, context: mode}).ID
	}
	m.start()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			work := works[mode]
			require.NoError(t, m.reportProcess(work, agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED, launchedProcess, 0))
			require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STARTING, m.state(work), "one readiness report is not readiness")
			require.NoError(t, m.reportProcess(work, agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED, launchedProcess, 0))
			require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, m.state(work))
		})
	}
}

// An ended root Turn leaves the Work Working and not Waiting.
func TestTurnEndKeepsTheWorkWorking(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkWorking}).ID
	m.start()
	require.NoError(t, m.report(&agentprivatev1.ReportRequest{ProcessId: launchedProcess, WorkId: work, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED, ObservationSequence: 1, TurnCompleted: true}))
	got := m.get(work)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, got.State, "an ended root Turn leaves the Work Working")
	require.False(t, got.GetWaiting(), "an ended root Turn is not Waiting")
}

// The exit fact of a Working Work's process decides its State: a late exit
// naming an earlier process cannot end the current one, and applying the
// running execution's reports again changes nothing; the current process's
// exit without an admitted stop is a failure, and its repetition is accepted.
func TestProcessExitWithoutStop(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	cases := []struct {
		name    string
		process string
		want    agentprivatev1.WorkState
	}{
		{name: "earlier process", process: "earlier", want: agentprivatev1.WorkState_WORK_STATE_WORKING},
		{name: "current process", process: launchedProcess, want: agentprivatev1.WorkState_WORK_STATE_FAILED},
	}
	works := map[string]string{}
	for _, c := range cases {
		works[c.name] = seedWork(t, m.db, definition, workSeed{subject: c.name, state: domain.WorkWorking, context: domain.ContextContinue, turns: 1}).ID
	}
	m.start()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work := works[c.name]
			for range 2 {
				require.NoError(t, m.reportProcess(work, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, c.process, 1))
				m.runs.applyReports(t, m.ctx, work)
				require.Equal(t, c.want, m.state(work))
			}
		})
	}
}

// A stop of a Working Work asks its process to exit and keeps it Stopping
// until the exit is acknowledged and its execution's compute released.
func TestStopEndsAWorkingWork(t *testing.T) {
	m := newLifecycleModule(t)
	work := seedWork(t, m.db, seedDefinition(t, m.db), workSeed{subject: "work-a", state: domain.WorkWorking, turns: 1}).ID
	m.start()
	_, err := m.client.StopWork(m.control(), &agentprivatev1.StopWorkRequest{SubjectId: workOwner, WorkId: work, RequestId: "stop-first"})
	require.NoError(t, err)
	m.runs.awaitExitRequest(t, m.ctx, work, "stop-first")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, m.state(work))
	require.NoError(t, m.reportProcess(work, agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, launchedProcess, 143))
	m.runs.awaitRetired(t, m.ctx, work)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, m.state(work), "an exit alone does not release compute")
	m.runs.release(t, m.ctx, work)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, m.state(work))
}
