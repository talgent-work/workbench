package domain_test

import (
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
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: session.ID, AgentID: session.AgentID, Capabilities: session.CapabilitySnapshot, RepresentedPerson: "", WorkLayout: domain.DefaultWorkLayout()},
		domain.StartAdmission{AgentID: session.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.ErrorIs(t, err, domain.ErrInvalidWork, "an admission without its time cannot start a Work")
	require.Nil(t, work, "nothing starts")
}

// The Work applies the failure its execution reports for a missed Startup
// Deadline, naming the earliest incomplete step, and asks the process it may
// hold to end; a failed start does not fail again.
func TestStartupDeadlineFailureNamesTheEarliestIncompleteStep(t *testing.T) {
	session := newSession(t)
	port := follow(session)
	missedDeadline(t, session, "Startup Deadline reached before the execution environment was ready.")
	require.Equal(t, domain.WorkFailed, session.State)
	require.Equal(t, "Startup Deadline reached before the execution environment was ready.", session.FailureReason)
	require.Len(t, port.stops(), 1)
	require.False(t, session.HoldsActiveProcess())

	changed, err := session.ApplyExecutionReport(domain.ExecutionStartFailed{WorkID: session.ID, StartAdmittedAt: session.HarnessProcess.StartingSince, Reason: "later"})
	require.NoError(t, err)
	require.False(t, changed, "a failed start does not fail again")
	require.Equal(t, "Startup Deadline reached before the execution environment was ready.", session.FailureReason)
	require.Len(t, port.stops(), 1)
}

// A failed start whose execution reports it never launched a Harness process
// and no longer can is abandoned: its environment is released without an
// exit, a late report cannot revive it, and once the release is confirmed
// the Work restarts.
func TestAbandonedStartReleasesItsEnvironmentWithoutAProcess(t *testing.T) {
	session := newSession(t)
	port := follow(session)
	missedDeadline(t, session, "Startup Deadline reached before the Harness started.")
	abandoned(t, session)
	require.True(t, session.HarnessProcess.StartAbandoned)
	require.Equal(t, 1, port.reclaims(), "the environment is released at once")
	require.Equal(t, domain.RetiredByAbandonment, releaseCause(session))
	require.False(t, session.HoldsProcess())

	ready(t, session)
	require.False(t, session.HarnessProcess.Ready, "a late report cannot revive an abandoned start")
	exited(t, session, domain.HarnessExit{ExitCode: 1})
	require.False(t, session.HarnessProcess.Exited, "an abandoned start has no exit to record")
	require.ErrorIs(t, session.Restart(admission(session)), domain.ErrComputeReclamationPending)

	stops := len(port.stops())
	changed, err := session.RecoverExecution()
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, domain.RetiredByAbandonment, releaseCause(session), "recovery keeps asking for the release")
	require.Len(t, port.stops(), stops, "nothing is stopped again")

	released(t, session)
	require.NoError(t, session.Restart(admission(session)))
	require.Equal(t, domain.WorkStarting, session.State)
	require.False(t, session.HarnessProcess.StartAbandoned, "the new start is a new process")
}

// A start still within its deadline is failed by the deadline first; a start
// that ran a process ends by its exit, never by an abandonment report.
func TestAbandonmentAppliesOnlyToAFailedStartWithoutAProcess(t *testing.T) {
	starting := newSession(t)
	abandoned(t, starting)
	require.False(t, starting.HarnessProcess.StartAbandoned)
	require.Equal(t, domain.WorkStarting, starting.State)

	ran := newSession(t)
	ready(t, ran)
	require.NoError(t, ran.RecordFailure("failed"))
	abandoned(t, ran)
	require.False(t, ran.HarnessProcess.StartAbandoned, "a contradicting report never blocks recovery")
}

// Closing an abandoned start completes on the release confirmation alone.
func TestClosingAnAbandonedStartClosesOnRelease(t *testing.T) {
	session := newSession(t)
	missedDeadline(t, session, "Startup Deadline reached before the Harness started.")
	require.NoError(t, session.Close("close", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosing, session.State)
	abandoned(t, session)
	released(t, session)
	require.Equal(t, domain.WorkClosed, session.State)
}
