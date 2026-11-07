//go:build integration

package execution

import (
	"context"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

const usedAuthorization = "01900000-0000-7000-8000-0000000000a1"

func executionDatabase(t *testing.T) *xorm.Engine {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// begin saves a new execution of the Work and loads it back.
func begin(t *testing.T, repository *AgentExecutionRepository, workID string, admittedAt time.Time, realization domain.ExecutionRealization) *domain.AgentExecution {
	t.Helper()
	launch := request()
	launch.WorkID = workID
	launch.AuthorizationSelection = domain.WorkAuthorizationSelection{OwnerUserID: launch.HomeBinding.OwnerID, Authorizations: []domain.WorkServiceAuthorization{{AuthorizationID: usedAuthorization, Provider: "github", Service: "github", SupportedTargets: []string{"github-api"}}}}
	run, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: domain.WorkStartAdmitted{WorkID: workID, StartAdmittedAt: admittedAt, DeskID: "desk-" + workID}, Launch: launch, Realization: realization})
	require.NoError(t, err)
	require.NoError(t, repository.Save(context.Background(), run))
	stored, err := repository.Get(context.Background(), run.ID)
	require.NoError(t, err)
	return stored
}

// retire retires the execution as withdrawn and, when released, confirms its
// release with no environment observed.
func retire(t *testing.T, repository *AgentExecutionRepository, run *domain.AgentExecution, at time.Time, released bool) {
	t.Helper()
	changed, err := run.Retire(domain.WorkComputeReleaseRequested{WorkID: run.WorkID, StartAdmittedAt: run.StartAdmittedAt, Cause: domain.RetiredWithdrawn}, at)
	require.NoError(t, err)
	require.True(t, changed)
	if released {
		require.True(t, run.RecordDeskLeft(at))
		changed, err = run.ConfirmRelease(domain.EnvironmentObservation{}, at)
		require.NoError(t, err)
		require.True(t, changed)
	}
	require.NoError(t, repository.Save(context.Background(), run))
}

func TestAgentExecutionRepositoryRoundTripsUnderItsVersion(t *testing.T) {
	ctx := context.Background()
	db := executionDatabase(t)
	now := time.UnixMilli(time.Now().UnixMilli()).UTC()
	repository := NewAgentExecutionRepository(db, func() time.Time { return now })

	_, err := repository.Get(ctx, "01900000-0000-7000-8000-00000000ffff")
	require.ErrorIs(t, err, domain.ErrExecutionNotFound)
	_, err = repository.Latest(ctx, "no-execution")
	require.ErrorIs(t, err, domain.ErrExecutionNotFound)

	launch := request()
	launch.WorkID = "round-trip"
	run, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: domain.WorkStartAdmitted{WorkID: launch.WorkID, StartAdmittedAt: now, DeskID: "desk-round-trip"}, Launch: launch, Realization: domain.RealizationManaged})
	require.NoError(t, err)
	require.NoError(t, repository.Save(ctx, run))
	stored, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.Version)
	require.Equal(t, run.ID, stored.ID)
	require.Equal(t, run.WorkID, stored.WorkID)
	require.True(t, run.StartAdmittedAt.Equal(stored.StartAdmittedAt))
	require.Equal(t, run.Realization, stored.Realization)
	require.Equal(t, run.Desk.ID, stored.Desk.ID)
	require.True(t, run.Desk.SeatedAt.Equal(stored.Desk.SeatedAt))
	require.Equal(t, domain.ExecutionStarting, stored.State)
	require.Equal(t, run.Launch.WorkID, stored.Launch.WorkID)
	require.Equal(t, run.Launch.Capabilities, stored.Launch.Capabilities)
	require.Equal(t, run.Launch.HomeBinding, stored.Launch.HomeBinding)
	require.True(t, run.Launch.PreparationBound.Equal(stored.Launch.PreparationBound))
	latest, err := repository.Latest(ctx, launch.WorkID)
	require.NoError(t, err)
	require.Equal(t, run.ID, latest.ID)

	// Every fact the execution records survives the round trip.
	stale, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	require.True(t, stored.RecordSubmission())
	changed, err := stored.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "sandbox-round-trip"})
	require.NoError(t, err)
	require.True(t, changed)
	_, err = stored.RecordLaunch("process-round-trip")
	require.NoError(t, err)
	require.True(t, stored.RecordCredentialDelivery(domain.CredentialDelivery{Delivered: true, Digest: "digest", ExpiresAt: now.Add(time.Hour)}))
	require.True(t, stored.MarkInputsChanged())
	require.True(t, stored.MarkInputsChanged())
	require.True(t, stored.RecordInputSync(1, now))
	_, err = stored.RequestExit(domain.WorkStopRequested{WorkID: launch.WorkID, StartAdmittedAt: now, StopRequestID: "stop-round-trip", CancelWaitingTurn: true})
	require.NoError(t, err)
	current, exited := stored.RecordExit("process-round-trip", domain.HarnessExit{ExitCode: 3, Reason: "fixture"})
	require.True(t, current)
	require.True(t, exited)
	require.NoError(t, repository.Save(ctx, stored))
	loaded, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, 2, loaded.Version)
	require.True(t, loaded.Creation.Submitted)
	require.Equal(t, "sandbox-round-trip", loaded.Environment)
	require.Equal(t, "process-round-trip", loaded.Process.ID)
	require.True(t, loaded.Process.Exited)
	require.Equal(t, domain.HarnessExit{ExitCode: 3, Reason: "fixture"}, loaded.Process.Exit)
	require.Equal(t, domain.ExitRequest{StopRequestID: "stop-round-trip", CancelWaitingTurn: true}, loaded.ExitRequest)
	require.True(t, loaded.Credentials.Delivered)
	require.Equal(t, "digest", loaded.Credentials.Digest)
	require.True(t, now.Add(time.Hour).Equal(loaded.Credentials.ExpiresAt))
	require.Equal(t, int64(1), loaded.Inputs.Due)
	require.True(t, now.Equal(loaded.Inputs.SyncedAt))

	require.ErrorIs(t, repository.Save(ctx, stale), domain.ErrConcurrentModification, "an execution loaded before the last save cannot overwrite it")
	unchanged, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, loaded, unchanged)

	retire(t, repository, loaded, now, false)
	retired, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionRetired, retired.State)
	require.Equal(t, domain.RetiredWithdrawn, retired.Retirement)
	require.True(t, now.Equal(retired.RetiredAt))
	require.True(t, retired.ReleasedAt.IsZero())
}

func TestAgentExecutionRepositoryAdmitsOneUnretiredExecutionPerWork(t *testing.T) {
	ctx := context.Background()
	db := executionDatabase(t)
	now := time.UnixMilli(time.Now().UnixMilli()).UTC()
	repository := NewAgentExecutionRepository(db, func() time.Time { return now })
	first := begin(t, repository, "admission", now, domain.RealizationCloud)

	launch := request()
	launch.WorkID = "admission"
	second, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: domain.WorkStartAdmitted{WorkID: launch.WorkID, StartAdmittedAt: now.Add(time.Second), DeskID: "desk-second"}, Launch: launch, Realization: domain.RealizationCloud})
	require.NoError(t, err)
	require.ErrorIs(t, repository.Save(ctx, second), domain.ErrExecutionAdmission)
	latest, err := repository.Latest(ctx, launch.WorkID)
	require.NoError(t, err)
	require.Equal(t, first.ID, latest.ID, "a refused admission records nothing")

	// A retired execution, released or not, leaves room for the next start.
	retire(t, repository, first, now, false)
	require.NoError(t, repository.Save(ctx, second))
	latest, err = repository.Latest(ctx, launch.WorkID)
	require.NoError(t, err)
	require.Equal(t, second.ID, latest.ID)
}

func TestExecutionQueryRepositoryFindsTheExecutionsAChangeConcerns(t *testing.T) {
	ctx := context.Background()
	db := executionDatabase(t)
	now := time.UnixMilli(time.Now().UnixMilli()).UTC()
	repository := NewAgentExecutionRepository(db, func() time.Time { return now })
	lookups := infrastructure.NewExecutionQueryRepository(db)

	live := begin(t, repository, "lookup-live", now, domain.RealizationCloud)
	retired := begin(t, repository, "lookup-retired", now, domain.RealizationCloud)
	retire(t, repository, retired, now, false)
	released := begin(t, repository, "lookup-released", now, domain.RealizationCloud)
	retire(t, repository, released, now, true)
	managed := begin(t, repository, "lookup-managed", now, domain.RealizationManaged)
	managedRetired := begin(t, repository, "lookup-managed-retired", now, domain.RealizationManaged)
	retire(t, repository, managedRetired, now, false)
	for _, id := range []string{live.ID, managed.ID, managedRetired.ID} {
		_, err := db.Exec("UPDATE execution_allocation SET input_owner = ? WHERE id = ?", "input-owner", id)
		require.NoError(t, err)
	}

	t.Run("outstanding executions page unretired and unreleased ones in id order", func(t *testing.T) {
		want := []string{live.ID, retired.ID, managed.ID, managedRetired.ID}
		slices.Sort(want)
		all, err := lookups.ListOutstandingExecutions(ctx, "", 10)
		require.NoError(t, err)
		require.Equal(t, want, all, "a released execution is history")
		var paged []string
		after := ""
		for {
			page, err := lookups.ListOutstandingExecutions(ctx, after, 3)
			require.NoError(t, err)
			require.LessOrEqual(t, len(page), 3)
			paged = append(paged, page...)
			if len(page) < 3 {
				break
			}
			after = page[len(page)-1]
		}
		require.Equal(t, want, paged)
	})

	t.Run("an authorization concerns the unretired executions using it", func(t *testing.T) {
		using, err := lookups.ListExecutionsUsingAuthorization(ctx, usedAuthorization)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{live.ID, managed.ID}, using)
		unused, err := lookups.ListExecutionsUsingAuthorization(ctx, "01900000-0000-7000-8000-0000000000a2")
		require.NoError(t, err)
		require.Empty(t, unused)
		blank, err := lookups.ListExecutionsUsingAuthorization(ctx, " ")
		require.NoError(t, err)
		require.Empty(t, blank)
	})

	t.Run("an input source concerns the unretired managed executions reading it", func(t *testing.T) {
		reading, err := lookups.ListExecutionsReadingInputs(ctx, "input-owner")
		require.NoError(t, err)
		require.Equal(t, []string{managed.ID}, reading, "a cloud or retired execution syncs no inputs")
		other, err := lookups.ListExecutionsReadingInputs(ctx, "other-owner")
		require.NoError(t, err)
		require.Empty(t, other)
		blank, err := lookups.ListExecutionsReadingInputs(ctx, "")
		require.NoError(t, err)
		require.Empty(t, blank)
	})
}

// The checkout outcomes a run's preparation recorded survive the round trip;
// a run that checked nothing out has none.
func TestAgentExecutionRepositoryRoundTripsCheckoutResults(t *testing.T) {
	ctx := context.Background()
	db := executionDatabase(t)
	now := time.UnixMilli(time.Now().UnixMilli()).UTC()
	repository := NewAgentExecutionRepository(db, func() time.Time { return now })
	launch := request()
	launch.WorkID = "checkout-round-trip"
	launch.RepositorySelection, launch.RemainingRepositories = checkoutSelection(), []string{"github:1", "github:2"}
	run, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: domain.WorkStartAdmitted{WorkID: launch.WorkID, StartAdmittedAt: now, DeskID: "desk-checkout"}, Launch: launch, Realization: domain.RealizationCloud})
	require.NoError(t, err)
	require.NoError(t, repository.Save(ctx, run))
	stored, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Empty(t, stored.CheckoutResults, "no outcome before its checkout ended")
	require.Equal(t, []string{"github:1", "github:2"}, stored.Launch.RemainingRepositories)
	_, ok := stored.CheckoutsEnded()
	require.False(t, ok)

	require.True(t, stored.RecordSubmission())
	_, err = stored.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "sandbox-checkout"})
	require.NoError(t, err)
	changed, err := stored.RecordPreparation(domain.PreparationReport{Checkouts: []domain.RepositoryCheckout{
		{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", Reason: domain.CheckoutBranchMissing},
	}})
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, repository.Save(ctx, stored))

	loaded, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	want := []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", Reason: domain.CheckoutBranchMissing}}
	require.Equal(t, want, loaded.CheckoutResults)
	require.True(t, loaded.Process.PreparationEnded)
	ended, ok := loaded.CheckoutsEnded()
	require.True(t, ok, "a Work that missed the report rebuilds it from the stored outcomes")
	require.Equal(t, domain.ExecutionCheckoutsEnded{WorkID: launch.WorkID, StartAdmittedAt: loaded.StartAdmittedAt, Checkouts: want}, ended)
}
