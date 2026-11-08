package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

const managedComputer = "01a19bc3-075a-7c57-b42b-7fdf73b611ee"

// waitingSeat is a Computer's answer queuing a run in its line.
func waitingSeat(desk string) domain.ComputerSeat {
	return domain.ComputerSeat{ComputerID: managedComputer, Kind: domain.ComputerManaged, DeskID: desk, Waiting: true}
}

func queuedWork(t *testing.T, admittedAt time.Time) (*domain.AgentWork, *executionPort) {
	t.Helper()
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"review"}, DefaultStartupAgent: "review:worker", BasePrompt: "retained instructions"}, []domain.PluginRelease{pluginRelease("review", "1")})
	require.NoError(t, err)
	agent := uuid.NewString()
	work, err := domain.NewAgentWork(domain.WorkInput{WorkModel: domain.MachineLoginModel(), ComputerManaged: true, StartedBy: "owner", ID: uuid.NewString(), AgentID: agent, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, Capabilities: snapshot},
		domain.StartAdmission{AdmittedAt: admittedAt, AgentID: agent, DefinitionAdmitted: true, Seat: waitingSeat("desk-1")})
	port := follow(work)
	require.NoError(t, err)
	return work, port
}

// A queued start keeps the Work Starting while its execution waits Queued for
// its Desk; its Startup Deadline counts from seating, so the time it waited
// never fails it.
func TestAQueuedStartWaitsForItsDeskAndItsDeadlineCountsFromSeating(t *testing.T) {
	admitted := time.Now().Add(-time.Hour)
	work, port := queuedWork(t, admitted)
	require.Equal(t, domain.WorkStarting, work.State)
	require.True(t, work.WaitsForDesk(true))
	require.Equal(t, 1, port.starts())
	require.NoError(t, work.Validate())

	execution := beginOf(t, work, domain.RealizationManaged)
	require.Equal(t, domain.ExecutionQueued, execution.State, "a waiting start's execution waits in the line")
	require.True(t, execution.Desk.SeatedAt.IsZero())
	require.True(t, execution.StartupDeadlineAt().IsZero(), "a Queued execution has no Startup Deadline yet")
	decision := decide(t, execution, domain.EnvironmentObservation{}, admitted.Add(10*domain.StartupDeadline))
	require.Empty(t, decision.Steps, "a Queued execution creates nothing")
	require.False(t, decision.Changed, "time in the line never fails the start")
	require.True(t, execution.HoldsDesk("desk-1"), "the waiting start holds its Desk")

	seatedAt := time.Now()
	seated, err := execution.Seat(seatedAt)
	require.NoError(t, err)
	require.True(t, seated)
	require.Equal(t, domain.ExecutionStarting, execution.State)
	require.Equal(t, seatedAt.UTC().Add(domain.StartupDeadline-2*time.Minute), execution.Launch.PreparationBound, "preparation is bounded from seating")
	require.Equal(t, seatedAt.UTC().Add(domain.StartupDeadline), execution.StartupDeadlineAt())
	seated, err = execution.Seat(seatedAt.Add(time.Minute))
	require.NoError(t, err)
	require.False(t, seated, "a repeated seating changes nothing")

	require.Equal(t, []domain.ExecutionStepKind{domain.StepSubmitCreation}, kinds(decide(t, execution, domain.EnvironmentObservation{}, seatedAt.Add(domain.StartupDeadline-time.Second))))
	require.Empty(t, execution.Outcome.StartFailure)
	decision = decide(t, execution, domain.EnvironmentObservation{}, seatedAt.Add(domain.StartupDeadline))
	require.True(t, decision.Changed)
	require.NotEmpty(t, execution.Outcome.StartFailure, "the deadline counted from seating fails the start")
}

// A stop or close withdraws a queued first start: the Work is closed at
// once, and its execution retires and leaves its Desk without any compute to
// reclaim.
func TestStoppingOrClosingAQueuedFirstStartClosesTheWork(t *testing.T) {
	for name, withdraw := range map[string]func(*domain.AgentWork) error{
		"stop": func(w *domain.AgentWork) error { return w.Stop("withdraw", true) },
		"close": func(w *domain.AgentWork) error {
			return w.Close("withdraw", domain.ClosureFacts{StartQueued: true})
		},
	} {
		t.Run(name, func(t *testing.T) {
			work, port := queuedWork(t, time.Now())
			execution := beginOf(t, work, domain.RealizationManaged)
			work.Events.Drain()
			require.NoError(t, withdraw(work))
			require.Equal(t, domain.WorkClosed, work.State)
			require.Empty(t, port.stops())
			require.Equal(t, domain.RetiredWithdrawn, releaseCause(work))
			_, begins := executionRequest[domain.WorkStartAdmitted](work)
			require.False(t, begins, "a withdrawn start is never begun")
			require.NoError(t, work.Validate())

			requested, _ := executionRequest[domain.WorkComputeReleaseRequested](work)
			retired, err := execution.Retire(requested, time.Now())
			require.NoError(t, err)
			require.True(t, retired)
			require.Equal(t, []domain.ExecutionStepKind{domain.StepLeaveDesk}, kinds(decide(t, execution, domain.EnvironmentObservation{}, time.Now())), "its Desk leaves; nothing was created")
		})
	}
}

// A queued Resume withdrawn by a stop returns to Stopped, and by a close is
// then closed; a queued Restart withdrawn returns to Failed.
func TestAWithdrawnQueuedContinuationReturnsToItsFormerState(t *testing.T) {
	work, port := stoppedWork(t)
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: work.ComputerID, Kind: domain.ComputerCloud, DeskID: "desk-2", Waiting: true}, ModelConfigurationProvided: true}
	starts := port.starts()
	require.NoError(t, work.Resume(admission))
	require.Equal(t, domain.WorkStarting, work.State)
	require.Equal(t, starts+1, port.starts(), "a queued Resume is a start whose execution waits for its Desk")
	work.Events.Drain()
	require.NoError(t, work.Stop("withdraw", true))
	require.Equal(t, domain.WorkStopped, work.State)
	require.Equal(t, []domain.RetirementCause{domain.RetiredWithdrawn}, requestsOf(work).releases, "its execution retires and leaves its Desk")
	require.NoError(t, work.Validate())

	admission.Seat.DeskID = "desk-3"
	require.NoError(t, work.Resume(admission), "a withdrawn start holds no compute, so the Work resumes again")
	require.NoError(t, work.Close("close", domain.ClosureFacts{StartQueued: true}))
	require.Equal(t, domain.WorkClosed, work.State)

	failed, _ := startingWork(t)
	missedDeadline(t, failed, "Startup Deadline reached before the Harness started.")
	exited(t, failed, domain.HarnessExit{ExitCode: 1})
	released(t, failed)
	require.Equal(t, domain.WorkFailed, failed.State)
	admission = domain.StartAdmission{AdmittedAt: time.Now(), AgentID: failed.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: failed.ComputerID, Kind: domain.ComputerCloud, DeskID: "desk-4", Waiting: true}, ModelConfigurationProvided: true}
	require.NoError(t, failed.Restart(admission))
	require.True(t, failed.WaitsForDesk(true))
	require.NoError(t, failed.Stop("withdraw", true))
	require.Equal(t, domain.WorkFailed, failed.State)
	require.NotEmpty(t, failed.FailureReason)
	require.NoError(t, failed.Validate())
}
