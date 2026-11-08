package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// checkoutWorld is a Work that selected github:1 and github:2, whose
// execution's environment is bound and running.
func checkoutWorld(t *testing.T) *world {
	t.Helper()
	work := startingSession(t)
	work.RepositorySelection = domain.WorkRepositorySelection{OwnerUserID: "owner", WorkBranch: "talgent/work", Repositories: []domain.WorkRepository{
		{Provider: "github", RepoID: "github:1", RepoName: "acme/api", CloneURL: "https://github.com/acme/api.git", DefaultBranch: "main", BaseBranch: "main"},
		{Provider: "github", RepoID: "github:2", RepoName: "acme/web", CloneURL: "https://github.com/acme/web.git", DefaultBranch: "main", BaseBranch: "main"},
	}}
	w := newWorld(t, work)
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
	})
	w.environment.Observation = running("environment")
	return w
}

// The long step records every remaining repository's checkout with the end
// of the preparation, before the Harness launches; a failed preparation
// launches nothing.
func TestTheHarnessLaunchesOnlyOnceEveryRepositoryHasAnOutcome(t *testing.T) {
	ctx := context.Background()
	w := checkoutWorld(t)
	failure := errors.New("preparation interrupted")
	w.environment.PrepareFunc = func(context.Context) error { return failure }
	require.ErrorIs(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID), failure)
	require.Empty(t, w.execution(t).CheckoutResults)
	require.Zero(t, w.environment.Launched, "no Harness before checkout ended")

	w.environment.PrepareFunc = nil
	w.environment.Checkouts = []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", Reason: domain.CheckoutTimedOut}}
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))

	require.Equal(t, w.environment.Checkouts, w.execution(t).CheckoutResults)
	require.Equal(t, 1, w.environment.Launched)
}

// The Work records its execution's checkout outcomes once (Record
// checkouts), and its next start checks out nothing again.
func TestTheWorkRecordsItsExecutionsCheckoutOutcomesOnce(t *testing.T) {
	ctx := context.Background()
	w := checkoutWorld(t)
	w.environment.Checkouts = []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}}
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))

	require.NoError(t, w.coordination.RecordCheckouts(ctx, w.work().ID))
	saves := w.works.saves
	require.NoError(t, w.coordination.RecordCheckouts(ctx, w.work().ID))

	require.Equal(t, saves, w.works.saves, "recorded outcomes are not recorded again")
	require.Equal(t, []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", Reason: domain.CheckoutIncomplete}}, w.work().RepositoryCheckouts)
	require.Empty(t, w.work().RemainingRepositories())
}

// A Work that missed the checkout report records the outcomes when it
// applies its execution's reports, the release confirmation among them, so
// its next start never checks them out again.
func TestApplyingExecutionReportsCarriesTheMissedCheckoutOutcomes(t *testing.T) {
	ctx := context.Background()
	w := checkoutWorld(t)
	w.environment.Checkouts = []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", CheckedOut: true}}
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	require.Empty(t, w.work().RepositoryCheckouts, "the checkout report was missed")
	w.exit(t, 1)
	w.release(t)

	require.Equal(t, domain.WorkFailed, w.work().State)
	require.Len(t, w.work().RepositoryCheckouts, 2)
	require.Empty(t, w.work().RemainingRepositories())
}

// A first start that ended before its checkout ended leaves every repository
// to the next start.
func TestAnUnfinishedCheckoutIsLeftToTheNextStart(t *testing.T) {
	ctx := context.Background()
	w := checkoutWorld(t)
	w.change(t, func(execution *domain.AgentExecution) {
		retired, err := execution.Retire(domain.WorkComputeReleaseRequested{WorkID: execution.WorkID, StartAdmittedAt: execution.StartAdmittedAt, Cause: domain.RetiredUnreached}, w.now())
		require.NoError(t, err)
		require.True(t, retired)
	})
	require.NoError(t, w.coordination.RecordCheckouts(ctx, w.work().ID))

	require.Empty(t, w.work().RepositoryCheckouts)
	require.Equal(t, []string{"github:1", "github:2"}, w.work().LaunchSpecification().RemainingRepositories)
}
