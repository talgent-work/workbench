package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// beginOf begins the AgentExecution of the Work's current start, as Begin
// execution does from the Work's start request.
func beginOf(t *testing.T, work *domain.AgentWork, realization domain.ExecutionRealization) *domain.AgentExecution {
	t.Helper()
	admitted, ok := executionRequest[domain.WorkStartAdmitted](work)
	require.True(t, ok, "the Work asks for its start")
	execution, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: admitted, Launch: work.LaunchSpecification(), Realization: realization})
	require.NoError(t, err)
	return execution
}

// runningEnvironment observes the one running environment env.
func runningEnvironment(env string) domain.EnvironmentObservation {
	return domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: env, Running: true}}}
}

// decide reconciles the execution against observation at now.
func decide(t *testing.T, execution *domain.AgentExecution, observation domain.EnvironmentObservation, now time.Time) domain.ExecutionDecision {
	t.Helper()
	decision, err := execution.Reconcile(observation, now)
	require.NoError(t, err)
	return decision
}

// kinds lists the kinds of the decided steps.
func kinds(decision domain.ExecutionDecision) []domain.ExecutionStepKind {
	var result []domain.ExecutionStepKind
	for _, step := range decision.Steps {
		result = append(result, step.Kind)
	}
	return result
}

// boundExecution is a seated Cloud execution of a new Work whose creation
// bound the running environment "env-1" and whose credentials were delivered.
func boundExecution(t *testing.T) (*domain.AgentWork, *domain.AgentExecution) {
	t.Helper()
	work := newSession(t)
	execution := beginOf(t, work, domain.RealizationCloud)
	now := execution.Desk.SeatedAt
	require.Equal(t, []domain.ExecutionStepKind{domain.StepSubmitCreation}, kinds(decide(t, execution, domain.EnvironmentObservation{}, now)))
	require.True(t, execution.RecordSubmission())
	changed, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "env-1"})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepDeliverCredentials}, kinds(decide(t, execution, runningEnvironment("env-1"), now)))
	require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{Digest: "model-1"}))
	return work, execution
}

// launchedExecution is a bound execution whose preparation ended and whose
// Harness process "process-1" was launched.
func launchedExecution(t *testing.T) (*domain.AgentWork, *domain.AgentExecution) {
	t.Helper()
	work, execution := boundExecution(t)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepPrepare}, kinds(decide(t, execution, runningEnvironment("env-1"), execution.Desk.SeatedAt)))
	require.True(t, recordPreparation(t, execution))
	launched, err := execution.RecordLaunch("process-1")
	require.NoError(t, err)
	require.True(t, launched)
	return work, execution
}

// executionEvents drains the execution's events.
func executionEvents(execution *domain.AgentExecution) []domain.Event {
	return execution.Events.Drain()
}

// recordPreparation records that the execution's preparation ended with no
// Plugin selected, and reports whether that changed it.
func recordPreparation(t *testing.T, execution *domain.AgentExecution) bool {
	t.Helper()
	changed, err := execution.RecordPreparation(domain.PreparationReport{})
	require.NoError(t, err)
	return changed
}
