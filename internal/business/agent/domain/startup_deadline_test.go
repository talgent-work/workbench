package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestEveryProcessRecordsWhenItsStartEnteredStarting(t *testing.T) {
	session := newSession(t)
	require.NoError(t, session.Validate())
	session.HarnessProcess.StartingSince = time.Time{}
	require.ErrorIs(t, session.Validate(), domain.ErrInvalidWork)

	port := &executionPort{}
	_, err := domain.NewAgentWork(context.Background(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: session.ID, AgentID: session.AgentID, Capabilities: session.CapabilitySnapshot, WorkspaceReference: "workspace-a", HomeBinding: session.HomeBinding},
		domain.StartAdmission{AgentID: session.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, port)
	require.ErrorIs(t, err, domain.ErrInvalidWork, "an admission without its time cannot start a Work")
	require.Empty(t, port.starts)
}

func TestDegradableStepsEndBeforeTheRequiredStepsThatFollowThem(t *testing.T) {
	session := newSession(t)
	started := session.HarnessProcess.StartingSince

	port := &executionPort{}
	_, err := session.RecoverExecution(context.Background(), port)
	require.NoError(t, err)
	require.Len(t, port.starts, 1)
	require.Equal(t, started.Add(domain.StartupDeadline-2*time.Minute), port.starts[0].PreparationBound, "Plugin preparation and then repository checkout leave time to start the Harness")
}

func TestStartupDeadlineFailureNamesTheEarliestIncompleteStep(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	deadline := session.HarnessProcess.StartingSince.Add(domain.StartupDeadline)
	port := &executionPort{}

	failed, err := session.FailAtStartupDeadline(ctx, deadline.Add(-time.Nanosecond), port)
	require.NoError(t, err)
	require.False(t, failed)

	failed, err = session.FailAtStartupDeadline(ctx, deadline, port)
	require.NoError(t, err)
	require.True(t, failed)
	require.Equal(t, domain.WorkFailed, session.State)
	require.Equal(t, "Startup Deadline reached before the execution environment was ready.", session.FailureReason)
	require.Len(t, port.stops, 1)
	require.False(t, session.HoldsActiveProcess())

	failed, err = session.FailAtStartupDeadline(ctx, deadline.Add(time.Hour), port)
	require.NoError(t, err)
	require.False(t, failed, "a failed start does not fail again")
}

var abandonedObservation = domain.ProcessObservation{State: domain.ProcessStartAbandoned}

// When the execution authority reports that a failed start never launched a
// Harness process and no longer can, the start is abandoned: its environment
// is released without an exit, a late report cannot revive it, and once the
// release is confirmed the Work restarts.
func TestAbandonedStartReleasesItsEnvironmentWithoutAProcess(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	deadline := session.HarnessProcess.StartingSince.Add(domain.StartupDeadline)
	port := &executionPort{}
	failed, err := session.FailAtStartupDeadline(ctx, deadline, port)
	require.NoError(t, err)
	require.True(t, failed)
	require.NoError(t, session.ReconcileProcess(ctx, abandonedObservation, port))
	require.True(t, session.HarnessProcess.StartAbandoned)
	require.Equal(t, 1, port.reclaims, "the environment is released at once")
	require.False(t, session.HoldsProcess())

	require.NoError(t, session.ConfirmPrepared(ctx, deadline.Add(time.Minute), port))
	require.False(t, session.HarnessProcess.Prepared, "a late report cannot revive an abandoned start")
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 1}, port))
	require.False(t, session.HarnessProcess.Exited, "an abandoned start has no exit to record")
	require.ErrorIs(t, session.Restart(ctx, admission(session), port), domain.ErrComputeReclamationPending)

	stops := len(port.stops)
	changed, err := session.RecoverExecution(ctx, port)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 2, port.reclaims, "recovery repeats the release")
	require.Len(t, port.stops, stops, "nothing is stopped again")

	require.NoError(t, session.ConfirmComputeReclaimed())
	require.NoError(t, session.Restart(ctx, admission(session), port))
	require.Equal(t, domain.WorkStarting, session.State)
	require.False(t, session.HarnessProcess.StartAbandoned, "the new start is a new process")
}

// A start still within its deadline is failed by the deadline first; a start
// that ran a process ends by its exit, never by an abandonment report.
func TestAbandonmentAppliesOnlyToAFailedStartWithoutAProcess(t *testing.T) {
	ctx := context.Background()
	starting := newSession(t)
	require.NoError(t, starting.ReconcileProcess(ctx, abandonedObservation, &executionPort{}))
	require.False(t, starting.HarnessProcess.StartAbandoned)
	require.Equal(t, domain.WorkStarting, starting.State)

	ran := newSession(t)
	require.NoError(t, ran.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, ran.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, ran.RecordFailure("failed"))
	require.NoError(t, ran.ReconcileProcess(ctx, abandonedObservation, &executionPort{}), "a contradicting report never blocks recovery")
	require.False(t, ran.HarnessProcess.StartAbandoned)
}

// Closing an abandoned start completes on the release confirmation alone.
func TestClosingAnAbandonedStartClosesOnRelease(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	_, err := session.FailAtStartupDeadline(ctx, session.HarnessProcess.StartingSince.Add(domain.StartupDeadline), port)
	require.NoError(t, err)
	require.NoError(t, session.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkClosing, session.State)
	require.NoError(t, session.ReconcileProcess(ctx, abandonedObservation, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkClosed, session.State)
}
