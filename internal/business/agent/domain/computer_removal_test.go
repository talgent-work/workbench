package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// removed is the authority's confirmation that the Work's Managed Computer was
// removed.
var removed = domain.EnvironmentObservation{Removed: true, LossReason: domain.ComputerRemovedReason}

// applyReports applies every report of the execution to its Work.
func applyReports(t *testing.T, work *domain.AgentWork, execution *domain.AgentExecution) {
	t.Helper()
	for _, r := range execution.Reports() {
		report(t, work, r)
	}
}

// The removal of a Work's Managed Computer is the loss of its execution
// environment: the execution reports it, Working becomes Failed and Closing
// becomes Closed once the execution is released, with nothing asked of the
// gone machine.
func TestAComputerRemovalLosesTheEnvironmentOfItsWorks(t *testing.T) {
	working, port := turningWork(t)
	execution := beginOf(t, working, domain.RealizationManaged)
	decision := decide(t, execution, removed, time.Now())
	require.True(t, decision.Changed)
	require.Empty(t, decision.Steps)
	require.True(t, execution.Outcome.Lost)
	applyReports(t, working, execution)
	require.Equal(t, domain.WorkFailed, working.State)
	require.Equal(t, domain.ComputerRemovedReason, working.FailureReason)
	require.True(t, working.HarnessProcess.EnvironmentLost)
	require.False(t, working.HoldsProcess())
	require.Equal(t, domain.RetiredByLoss, releaseCause(working), "its execution retires and leaves its Desk")
	require.Empty(t, port.stops())

	require.False(t, decide(t, execution, removed, time.Now()).Changed, "observing the removal again changes nothing")
	stops := len(port.stops())
	applyReports(t, working, execution)
	require.Len(t, port.stops(), stops, "applying the loss again changes nothing")

	closing, _ := runningHarnessWork(t, domain.HarnessChoice{})
	execution = beginOf(t, closing, domain.RealizationManaged)
	require.NoError(t, closing.RecordFailure("model unavailable"))
	require.NoError(t, closing.Close("close", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosing, closing.State)
	decide(t, execution, removed, time.Now())
	applyReports(t, closing, execution)
	requested, _ := executionRequest[domain.WorkComputeReleaseRequested](closing)
	retired, err := execution.Retire(requested, time.Now())
	require.NoError(t, err)
	require.True(t, retired)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepLeaveDesk}, kinds(decide(t, execution, domain.EnvironmentObservation{}, time.Now())))
	require.True(t, execution.RecordDeskLeft(time.Now()))
	confirmed, err := execution.ConfirmRelease(domain.EnvironmentObservation{}, time.Now())
	require.NoError(t, err)
	require.True(t, confirmed)
	applyReports(t, closing, execution)
	require.Equal(t, domain.WorkClosed, closing.State)
	require.Equal(t, "model unavailable", closing.FailureReason, "an earlier failure keeps explaining it")
}

// A start still waiting in a removed Computer's line ends, and a Work that
// stopped before the removal keeps its State: it can only be closed.
func TestAComputerRemovalEndsAWaitingStartAndLeavesAStoppedWork(t *testing.T) {
	queued, _ := queuedWork(t, time.Now())
	execution := beginOf(t, queued, domain.RealizationManaged)
	decide(t, execution, removed, time.Now())
	require.True(t, execution.Outcome.Lost, "a Queued execution on a removed Computer is lost")
	require.False(t, commandtest.Submit(execution), "nothing is created on a removed Computer")
	applyReports(t, queued, execution)
	require.Equal(t, domain.WorkFailed, queued.State)

	stopped, _ := stoppedWork(t)
	changed, err := stopped.ApplyExecutionReport(domain.ExecutionEnvironmentLost{WorkID: stopped.ID, StartAdmittedAt: stopped.HarnessProcess.StartingSince, Reason: domain.ComputerRemovedReason})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, domain.WorkStopped, stopped.State)
}
