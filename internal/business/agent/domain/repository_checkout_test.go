package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// repositoryWork is a new Work that selected the repositories github:1 and
// github:2, checked out on the branch talgent/work.
func repositoryWork(t *testing.T) *domain.AgentWork {
	t.Helper()
	work := newSession(t)
	work.RepositorySelection = domain.WorkRepositorySelection{OwnerID: "owner", WorkBranch: "talgent/work", Repositories: []domain.WorkRepository{
		{Provider: "github", RepoID: "github:1", RepoName: "acme/api", CloneURL: "https://github.com/acme/api.git", DefaultBranch: "main", BaseBranch: "main"},
		{Provider: "github", RepoID: "github:2", RepoName: "acme/web", CloneURL: "https://github.com/acme/web.git", DefaultBranch: "main", BaseBranch: "main"},
	}}
	return work
}

// boundRepositoryExecution is the bound execution of the Work's current start.
func boundRepositoryExecution(t *testing.T, work *domain.AgentWork) *domain.AgentExecution {
	t.Helper()
	execution := beginOf(t, work, domain.RealizationCloud)
	require.True(t, commandtest.Submit(execution))
	_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "env-1"})
	require.NoError(t, err)
	execution.Events.Drain()
	return execution
}

func checkoutsEnded(execution *domain.AgentExecution) []domain.ExecutionCheckoutsEnded {
	var ended []domain.ExecutionCheckoutsEnded
	for _, event := range execution.Events.Drain() {
		if typed, ok := event.(domain.ExecutionCheckoutsEnded); ok {
			ended = append(ended, typed)
		}
	}
	return ended
}

func TestFirstStartChecksOutEverySelectedRepository(t *testing.T) {
	launch := repositoryWork(t).LaunchSpecification()

	require.Equal(t, []string{"github:1", "github:2"}, launch.RemainingRepositories)
	require.Equal(t, "talgent/work", launch.RepositorySelection.WorkBranch)
}

func TestPreparationRecordsEachRemainingRepositoryOutcomeAndEndsCheckout(t *testing.T) {
	work := repositoryWork(t)
	execution := boundRepositoryExecution(t, work)

	changed, err := execution.RecordPreparation(domain.PreparationReport{Checkouts: []domain.RepositoryCheckout{
		{RepositoryID: "github:2", Reason: domain.CheckoutBranchMissing},
		{RepositoryID: "github:9", CheckedOut: true},
	}})

	require.NoError(t, err)
	require.True(t, changed)
	want := []domain.RepositoryCheckout{
		{RepositoryID: "github:1", Reason: domain.CheckoutIncomplete},
		{RepositoryID: "github:2", Reason: domain.CheckoutBranchMissing},
	}
	require.Equal(t, want, execution.CheckoutResults, "an omitted repository did not finish; one outside the run is ignored")
	require.Equal(t, []domain.ExecutionCheckoutsEnded{{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt, Checkouts: want}}, checkoutsEnded(execution))
	ended, ok := execution.CheckoutsEnded()
	require.True(t, ok, "the report is rebuilt from the execution's facts")
	require.Equal(t, want, ended.Checkouts)
}

func TestPreparationRefusesAnInvalidCheckoutOutcome(t *testing.T) {
	execution := boundRepositoryExecution(t, repositoryWork(t))

	for name, checkouts := range map[string][]domain.RepositoryCheckout{
		"unknown reason":    {{RepositoryID: "github:1", Reason: "fatal: authentication failed"}},
		"checked out cause": {{RepositoryID: "github:1", CheckedOut: true, Reason: domain.CheckoutTimedOut}},
		"no reason":         {{RepositoryID: "github:1"}},
		"twice":             {{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:1", CheckedOut: true}},
	} {
		_, err := execution.RecordPreparation(domain.PreparationReport{Checkouts: checkouts})
		require.ErrorIs(t, err, domain.ErrInvalidExecution, name)
	}
	require.False(t, execution.Process.PreparationEnded)
}

func TestCheckoutOutcomesAreRecordedOnce(t *testing.T) {
	work := repositoryWork(t)
	execution := boundRepositoryExecution(t, work)
	_, err := execution.RecordPreparation(domain.PreparationReport{Checkouts: []domain.RepositoryCheckout{
		{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", Reason: domain.CheckoutTimedOut},
	}})
	require.NoError(t, err)

	changed, err := execution.RecordPreparation(domain.PreparationReport{Checkouts: []domain.RepositoryCheckout{{RepositoryID: "github:2", CheckedOut: true}}})
	require.NoError(t, err)
	require.False(t, changed, "a later report never replaces the run's outcomes")
	ended := checkoutsEnded(execution)
	require.Len(t, ended, 1)

	require.True(t, work.RecordRepositoryCheckouts(ended[0]))
	require.False(t, work.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: work.ID, Checkouts: []domain.RepositoryCheckout{{RepositoryID: "github:2", CheckedOut: true}}}), "Single checkout")
	require.Equal(t, []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", Reason: domain.CheckoutTimedOut}}, work.RepositoryCheckouts)
	require.Empty(t, work.LaunchSpecification().RemainingRepositories, "Resume and Restart never repeat a completed or degraded checkout")
}

func TestNextStartChecksOutWhatAnUnfinishedFirstStartLeft(t *testing.T) {
	work := repositoryWork(t)

	require.True(t, work.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: work.ID, Checkouts: []domain.RepositoryCheckout{
		{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:7", CheckedOut: true},
	}}))

	require.Equal(t, []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}}, work.RepositoryCheckouts, "a repository outside the selection is ignored")
	require.Equal(t, []string{"github:2"}, work.LaunchSpecification().RemainingRepositories)
}

func TestAStartWithNothingLeftToCheckOutEndsNoCheckout(t *testing.T) {
	work := repositoryWork(t)
	require.True(t, work.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: work.ID, Checkouts: []domain.RepositoryCheckout{
		{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", CheckedOut: true},
	}}))
	execution := boundRepositoryExecution(t, work)

	changed, err := execution.RecordPreparation(domain.PreparationReport{})

	require.NoError(t, err)
	require.True(t, changed)
	require.Empty(t, checkoutsEnded(execution))
	_, ok := execution.CheckoutsEnded()
	require.False(t, ok)
}

func TestClosingWorkRecordsNoCheckout(t *testing.T) {
	work := repositoryWork(t)
	work.State = domain.WorkClosing

	require.False(t, work.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: work.ID, Checkouts: []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}}}))
	require.Empty(t, work.RepositoryCheckouts)
}

func TestRepositoryAccessEndsOnceTheWorkCloses(t *testing.T) {
	work := repositoryWork(t)
	require.NoError(t, work.AdmitRepositoryAccess("owner"))
	require.ErrorIs(t, work.AdmitRepositoryAccess("someone-else"), domain.ErrRepositoryPreparationConflict)

	work.State = domain.WorkClosing

	require.ErrorIs(t, work.AdmitRepositoryAccess("owner"), domain.ErrRepositoryPreparationCanceled)
}
