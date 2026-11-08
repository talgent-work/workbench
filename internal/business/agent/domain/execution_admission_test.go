package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A launch or credential delivery is admitted only into the bound
// environment of an unretired execution without an exit request, start
// failure, abandonment or loss (Bound environment only, Exit request ends
// delivery, Abandonment is final).
func TestAnEnvironmentActionIsAdmittedOnlyIntoTheBoundEnvironmentOfAWantedRun(t *testing.T) {
	_, bound := boundExecution(t)
	require.True(t, bound.AdmitEnvironmentAction("env-1"), "the bound environment of a wanted run")
	require.False(t, bound.AdmitEnvironmentAction("env-2"), "another environment is not the bound one")
	require.False(t, bound.AdmitEnvironmentAction(""), "no environment is not the bound one")

	unbound := beginOf(t, newSession(t), domain.RealizationCloud)
	require.True(t, commandtest.Submit(unbound))
	require.False(t, unbound.AdmitEnvironmentAction(""), "an unbound execution admits nothing")
	require.False(t, unbound.AdmitEnvironmentAction("env-1"), "an unbound execution admits nothing")

	for name, refuse := range map[string]func(*testing.T, *domain.AgentWork, *domain.AgentExecution){
		"exit request": func(t *testing.T, work *domain.AgentWork, execution *domain.AgentExecution) {
			changed, err := execution.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt, StopRequestID: "stop-1"})
			require.NoError(t, err)
			require.True(t, changed)
		},
		"start failure": func(t *testing.T, _ *domain.AgentWork, execution *domain.AgentExecution) {
			// Past its Startup Deadline, but within the margin that keeps
			// a launch begun just before it from being abandoned.
			decide(t, execution, runningEnvironment("env-1"), execution.StartupDeadlineAt())
			require.NotEmpty(t, execution.Outcome.StartFailure)
			require.False(t, execution.Outcome.Abandoned)
		},
		"abandonment": func(t *testing.T, _ *domain.AgentWork, execution *domain.AgentExecution) {
			decide(t, execution, runningEnvironment("env-1"), execution.StartupDeadlineAt().Add(time.Hour))
			require.True(t, execution.Outcome.Abandoned)
		},
		"loss": func(t *testing.T, _ *domain.AgentWork, execution *domain.AgentExecution) {
			decide(t, execution, domain.EnvironmentObservation{}, execution.Desk.SeatedAt)
			require.True(t, execution.Outcome.Lost)
		},
		"retirement": func(t *testing.T, work *domain.AgentWork, execution *domain.AgentExecution) {
			changed, err := execution.Retire(domain.WorkComputeReleaseRequested{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt, Cause: domain.RetiredUnreached}, execution.Desk.SeatedAt)
			require.NoError(t, err)
			require.True(t, changed)
		},
	} {
		t.Run(name, func(t *testing.T) {
			work, execution := boundExecution(t)
			refuse(t, work, execution)
			require.False(t, execution.AdmitEnvironmentAction("env-1"))
			launched, err := execution.RecordLaunch("process-1")
			require.NoError(t, err)
			require.False(t, launched, "no launch is recorded")
			require.False(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{Digest: "model-2"}), "no delivery is recorded")
		})
	}
}

// A start whose Work asked it to exit, or that no longer wants its run,
// submits no creation, while nothing is bound yet.
func TestASubmissionNeedsAWantedRun(t *testing.T) {
	work := newSession(t)
	stopped := beginOf(t, work, domain.RealizationCloud)
	_, err := stopped.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: stopped.StartAdmittedAt, StopRequestID: "stop-1"})
	require.NoError(t, err)
	require.False(t, commandtest.Submit(stopped), "a start asked to exit submits nothing")

	retired := beginOf(t, newSession(t), domain.RealizationCloud)
	_, err = retired.Retire(domain.WorkComputeReleaseRequested{WorkID: retired.WorkID, StartAdmittedAt: retired.StartAdmittedAt, Cause: domain.RetiredWithdrawn}, retired.Desk.SeatedAt)
	require.NoError(t, err)
	require.False(t, commandtest.Submit(retired), "a retired start submits nothing")

	submitted := beginOf(t, newSession(t), domain.RealizationCloud)
	require.True(t, commandtest.Submit(submitted))
	require.False(t, commandtest.Submit(submitted), "a creation is submitted once")
}

// A launch is recorded only for the bound environment the run still wants,
// once: a stop admitted during preparation launches nothing (Exit request
// ends delivery).
func TestAStopAdmittedDuringPreparationLaunchesNothing(t *testing.T) {
	work, execution := boundExecution(t)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepPrepare}, kinds(decide(t, execution, runningEnvironment("env-1"), execution.Desk.SeatedAt)))
	_, err := execution.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt, StopRequestID: "stop-1"})
	require.NoError(t, err)
	require.True(t, recordPreparation(t, execution), "the preparation's outcomes are still recorded")
	launched, err := execution.RecordLaunch("process-1")
	require.NoError(t, err)
	require.False(t, launched)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepWithdrawCredentials}, kinds(decide(t, execution, runningEnvironment("env-1"), execution.Desk.SeatedAt)), "the delivered credentials are cleared instead (Credential withdrawal)")
}

// A start that failed its Startup Deadline after launching keeps its process,
// but replaces none of its credentials.
func TestAFailedStartReplacesNoCredentials(t *testing.T) {
	_, execution := launchedExecution(t)
	attached := runningEnvironment("env-1")
	attached.Attached, attached.ModelDigest = true, "model-2"
	decision := decide(t, execution, attached, execution.StartupDeadlineAt())
	require.NotEmpty(t, execution.Outcome.StartFailure)
	require.NotContains(t, kinds(decision), domain.StepDeliverCredentials)
}
