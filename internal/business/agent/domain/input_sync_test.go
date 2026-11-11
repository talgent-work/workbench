package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// syncingManaged is a Managed execution whose first input sync is due.
func syncingManaged(t *testing.T) (*domain.AgentExecution, domain.EnvironmentObservation, time.Time) {
	t.Helper()
	managed := beginOf(t, newSession(t), domain.RealizationManaged)
	require.True(t, commandtest.Submit(managed))
	_, err := managed.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "env-m"})
	require.NoError(t, err)
	require.True(t, managed.RecordCredentialDelivery(domain.CredentialDelivery{}))
	observed := runningEnvironment("env-m")
	observed.InputsRefresh = time.Minute
	return managed, observed, managed.Desk.SeatedAt
}

// Input copy retry: a failing sync is attempted at most InputSyncAttempts
// times. The attempt that reaches the ceiling abandons the sync naming its
// cause, and nothing syncs again, not even the refresh, until a later
// change starts a new attempt.
func TestInputSyncIsAbandonedAtItsCeilingUntilALaterChange(t *testing.T) {
	managed, observed, now := syncingManaged(t)
	for attempt := 1; attempt < domain.InputSyncAttempts; attempt++ {
		changed, abandoned := managed.RecordInputSyncFailure("syncing the input copies failed")
		require.True(t, changed)
		require.False(t, abandoned, "attempt %d is below the ceiling", attempt)
		require.Contains(t, kinds(decide(t, managed, observed, now)), domain.StepSyncInputs)
	}
	changed, abandoned := managed.RecordInputSyncFailure("syncing the input copies timed out")
	require.True(t, changed)
	require.True(t, abandoned)
	require.Equal(t, "Input copies not synced after 30 attempts. Last error: syncing the input copies timed out.", managed.Inputs.Abandoned)
	require.NotContains(t, kinds(decide(t, managed, observed, now)), domain.StepSyncInputs, "an abandoned sync is not retried, though its copies were never synced")
	changed, abandoned = managed.RecordInputSyncFailure("syncing the input copies failed")
	require.False(t, changed || abandoned, "an abandoned sync records nothing more")

	require.True(t, managed.MarkInputsChanged())
	require.Empty(t, managed.Inputs.Abandoned, "a later change starts a new attempt")
	require.Contains(t, kinds(decide(t, managed, observed, now)), domain.StepSyncInputs)
	_, abandoned = managed.RecordInputSyncFailure("syncing the input copies failed")
	require.False(t, abandoned, "the new attempt has its own ceiling")
}

// A successful sync clears the failures counted against the ceiling.
func TestInputSyncSuccessClearsItsFailures(t *testing.T) {
	managed, _, now := syncingManaged(t)
	for range domain.InputSyncAttempts - 1 {
		managed.RecordInputSyncFailure("syncing the input copies failed")
	}
	require.True(t, managed.RecordInputSync(managed.Inputs.Due, now))
	require.Zero(t, managed.Inputs.Failures)
	_, abandoned := managed.RecordInputSyncFailure("syncing the input copies failed")
	require.False(t, abandoned)
}
