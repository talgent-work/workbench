package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// createdWorld is a Work whose execution bound "environment", running, with
// nothing delivered yet.
func createdWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, startingSession(t))
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
	})
	w.environment.Observation = running("environment")
	return w
}

// Every delivery carries the Identity Token Account issued for the Work's
// fixed facts, and falls due again a day before the token expires (Work
// token issuance).
func TestDeliveryCarriesTheWorksIdentityTokenAndFallsDueADayEarly(t *testing.T) {
	ctx := context.Background()
	w := createdWorld(t)
	work := w.work()
	_, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)

	require.Equal(t, []command.WorkIdentityFacts{{
		WorkID: work.ID, ProjectID: work.ProjectID, AgentID: work.AgentID, Purpose: "test-purpose",
		SubjectKind: string(work.Subject.Kind), SubjectID: work.Subject.ID, RepresentedPerson: work.RepresentedPerson,
	}}, w.accounts.Issued())
	require.Len(t, w.environment.Supplies, 1)
	token := w.environment.Supplies[0].IdentityToken
	require.Equal(t, "agent-token-"+work.ID, token.Token)
	delivered := w.execution(t).Credentials
	require.True(t, delivered.Delivered)
	require.Equal(t, token.ExpiresAt.Add(-24*time.Hour), delivered.ExpiresAt, "redelivered while the old token still works")
}

// An Application's Work's token states the Application that owns it, taken
// from its definition, and represents no person (Fixed facts).
func TestAnApplicationsWorkTokenStatesItsApplication(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	const application = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e"
	w.coordination = command.NewExecutionCoordination(w.now, w.works, commandtest.Definitions{Purpose: domain.StandaloneWorkPurpose, Application: application}, w.executions, w.computers, w.harness)
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
	})
	w.environment.Observation = running("environment")
	_, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)

	issued := w.accounts.Issued()
	require.Len(t, issued, 1)
	require.Equal(t, application, issued[0].OwningApplication)
	require.Equal(t, domain.StandaloneWorkPurpose, issued[0].Purpose)
}

// A token Account does not issue fails the delivery, so the Harness never
// starts; the Startup Deadline then fails the Work.
func TestAFailedTokenIssueLeavesTheHarnessUnstarted(t *testing.T) {
	ctx := context.Background()
	w := createdWorld(t)
	w.accounts.Refuse = true
	_, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.ErrorIs(t, err, commandtest.ErrTokenRefused)
	require.Zero(t, w.environment.Delivered, "nothing reaches the environment without the token")
	require.False(t, w.execution(t).Credentials.Delivered)
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	require.Zero(t, w.environment.Prepared, "no preparation, so no Harness, before the credentials")
}

// A run that outlives its token receives a new one through redelivery once
// the recorded expiry, a day before the token's own, has passed.
func TestARunOutlivingItsTokenReceivesANewOne(t *testing.T) {
	ctx := context.Background()
	w := createdWorld(t)
	_, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	require.Equal(t, "process", w.execution(t).Process.ID)
	w.change(t, func(execution *domain.AgentExecution) {
		_, err := execution.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, w.now())
		require.NoError(t, err)
	})
	require.Equal(t, domain.ExecutionRunning, w.execution(t).State)
	w.environment.Observation.Attached = true
	first := w.environment.Supplies[0].IdentityToken

	w.advance(commandtest.AgentTokenLifetime - 24*time.Hour - time.Minute)
	_, err = w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.Len(t, w.environment.Supplies, 1, "nothing is due before the recorded expiry")

	w.advance(2 * time.Minute)
	_, err = w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.Len(t, w.environment.Supplies, 2)
	second := w.environment.Supplies[1].IdentityToken
	require.True(t, second.ExpiresAt.After(first.ExpiresAt))
	require.True(t, w.now().Before(first.ExpiresAt), "the old token still worked when the new one arrived")
	require.Equal(t, second.ExpiresAt.Add(-24*time.Hour), w.execution(t).Credentials.ExpiresAt)
}

// A Work's token states its Work Labels, as its start fixed them (Fixed
// facts).
func TestAWorksTokenStatesItsWorkLabels(t *testing.T) {
	ctx := context.Background()
	session := startingSession(t)
	labels, err := domain.NewWorkLabels(map[string]string{"user": "u123", "tenant": "acme"})
	require.NoError(t, err)
	session.WorkLabels = labels
	w := newWorld(t, session)
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
	})
	w.environment.Observation = running("environment")
	_, err = w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)

	issued := w.accounts.Issued()
	require.Len(t, issued, 1)
	require.Equal(t, map[string]string{"user": "u123", "tenant": "acme"}, issued[0].WorkLabels)
}
