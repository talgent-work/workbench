package domain_test

import (
	"context"
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
	return domain.ComputerSeat{ComputerID: managedComputer, DeskID: desk, Waiting: true}
}

func queuedWork(t *testing.T, admittedAt time.Time) (*domain.AgentWork, *executionPort) {
	t.Helper()
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"review"}, DefaultStartupAgent: "review:worker", BasePrompt: "retained instructions"}, []domain.PluginRelease{pluginRelease("review", "1")})
	require.NoError(t, err)
	port := &executionPort{}
	agent := uuid.NewString()
	work, err := domain.NewAgentWork(t.Context(), domain.WorkInput{WorkModel: domain.MachineLoginModel(), ComputerManaged: true, StartedBy: "owner", ID: uuid.NewString(), AgentID: agent, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, Capabilities: snapshot},
		domain.StartAdmission{AdmittedAt: admittedAt, AgentID: agent, DefinitionAdmitted: true, Seat: waitingSeat("desk-1")}, port)
	require.NoError(t, err)
	return work, port
}

// A queued start keeps the Work Starting and asks for no startup until its
// Desk is seated; its Startup Deadline counts from seating, so the time it
// waited never fails it.
func TestAQueuedStartWaitsForItsDeskAndItsDeadlineCountsFromSeating(t *testing.T) {
	ctx := context.Background()
	admitted := time.Now().Add(-time.Hour)
	work, port := queuedWork(t, admitted)
	require.Equal(t, domain.WorkStarting, work.State)
	require.True(t, work.WaitsForDesk())
	require.Empty(t, port.starts, "a waiting start asks for no startup")
	require.NoError(t, work.Validate())

	failed, err := work.FailAtStartupDeadline(ctx, admitted.Add(10*domain.StartupDeadline), port)
	require.NoError(t, err)
	require.False(t, failed, "time in the line never fails the start")
	_, err = work.RecoverExecution(ctx, port)
	require.NoError(t, err)
	require.Empty(t, port.starts, "recovery does not start a waiting start")
	require.True(t, work.HoldsDesk("desk-1"), "the waiting start holds its Desk")

	seatedAt := time.Now()
	begun, err := work.BeginSeatedStart(ctx, seatedAt, port)
	require.NoError(t, err)
	require.True(t, begun)
	require.False(t, work.WaitsForDesk())
	require.Len(t, port.starts, 1)
	require.Equal(t, seatedAt.UTC().Add(domain.StartupDeadline-2*time.Minute), port.starts[0].PreparationBound, "preparation is bounded from seating")
	require.Equal(t, seatedAt.UTC().Add(domain.StartupDeadline), work.HarnessProcess.StartupDeadlineAt())
	begun, err = work.BeginSeatedStart(ctx, seatedAt.Add(time.Minute), port)
	require.NoError(t, err)
	require.False(t, begun, "a repeated seating changes nothing")

	failed, err = work.FailAtStartupDeadline(ctx, seatedAt.Add(domain.StartupDeadline-time.Second), port)
	require.NoError(t, err)
	require.False(t, failed)
	failed, err = work.FailAtStartupDeadline(ctx, seatedAt.Add(domain.StartupDeadline), port)
	require.NoError(t, err)
	require.True(t, failed)
}

// A stop or close withdraws a queued first start: the Work is closed at
// once, and its Desk leaves without any compute to reclaim.
func TestStoppingOrClosingAQueuedFirstStartClosesTheWork(t *testing.T) {
	for name, withdraw := range map[string]func(*domain.AgentWork, *executionPort) error{
		"stop": func(w *domain.AgentWork, p *executionPort) error { return w.Stop(t.Context(), "withdraw", p) },
		"close": func(w *domain.AgentWork, p *executionPort) error {
			return w.Close(t.Context(), "withdraw", domain.ClosureFacts{StartupPending: true}, p)
		},
	} {
		t.Run(name, func(t *testing.T) {
			work, port := queuedWork(t, time.Now())
			work.Events.Drain()
			require.NoError(t, withdraw(work, port))
			require.Equal(t, domain.WorkClosed, work.State)
			require.Equal(t, []domain.WorkDeskReleased{{WorkID: work.ID, DeskID: "desk-1"}}, deskReleases(work.Events.Drain()))
			require.False(t, work.HoldsDesk("desk-1"))
			require.Empty(t, port.stops)
			require.Zero(t, port.reclaims)
			require.NoError(t, work.Validate())
			begun, err := work.BeginSeatedStart(t.Context(), time.Now(), port)
			require.NoError(t, err)
			require.False(t, begun, "a withdrawn start is never begun")
		})
	}
}

// A queued Resume withdrawn by a stop returns to Stopped, and by a close is
// then closed; a queued Restart withdrawn returns to Failed.
func TestAWithdrawnQueuedContinuationReturnsToItsFormerState(t *testing.T) {
	ctx := context.Background()
	work, port := stoppedWork(t)
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: work.ComputerID, DeskID: "desk-2", Waiting: true}, ModelConfigurationProvided: true}
	starts := len(port.starts)
	require.NoError(t, work.Resume(ctx, admission, port))
	require.Equal(t, domain.WorkStarting, work.State)
	require.Len(t, port.starts, starts, "a queued Resume asks for no startup")
	work.Events.Drain()
	require.ErrorIs(t, work.Close(ctx, "close", domain.ClosureFacts{ContinuationPending: true}, port), domain.ErrWorkTransition, "a continuation still settling is not closed")
	require.True(t, work.WaitsForDesk(), "a refused close leaves the start waiting")

	require.NoError(t, work.Stop(ctx, "withdraw", port))
	require.Equal(t, domain.WorkStopped, work.State)
	require.Equal(t, []domain.WorkDeskReleased{{WorkID: work.ID, DeskID: "desk-2"}}, deskReleases(work.Events.Drain()))
	require.NoError(t, work.Validate())

	admission.Seat.DeskID = "desk-3"
	require.NoError(t, work.Resume(ctx, admission, port), "a withdrawn start holds no compute, so the Work resumes again")
	require.NoError(t, work.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkClosed, work.State)

	failed, port := startingWork(t)
	_, err := failed.FailAtStartupDeadline(ctx, time.Now().Add(domain.StartupDeadline), port)
	require.NoError(t, err)
	require.NoError(t, failed.RecordExit(ctx, domain.HarnessExit{ExitCode: 1}, port))
	require.NoError(t, failed.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkFailed, failed.State)
	admission = domain.StartAdmission{AdmittedAt: time.Now(), AgentID: failed.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: failed.ComputerID, DeskID: "desk-4", Waiting: true}, ModelConfigurationProvided: true}
	require.NoError(t, failed.Restart(ctx, admission, port))
	require.True(t, failed.WaitsForDesk())
	require.NoError(t, failed.Stop(ctx, "withdraw", port))
	require.Equal(t, domain.WorkFailed, failed.State)
	require.NotEmpty(t, failed.FailureReason)
	require.NoError(t, failed.Validate())
}
