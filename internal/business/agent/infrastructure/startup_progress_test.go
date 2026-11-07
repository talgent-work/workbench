package infrastructure

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

func encodeJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// createdRow is a Work created by the request "creation" that selected the
// repositories github:1 and github:2 and still holds its initial task input,
// with its first start admitted and ready.
func createdRow(t *testing.T) workViewRow {
	selection := workRepositoryRecord{OwnerUserID: "owner", WorkBranch: "talgent/work", Repositories: []selectedRepositoryRecord{
		{Provider: "github", RepoID: "github:1", RepoName: "acme/api", CloneURL: "https://github.com/acme/api.git", DefaultBranch: "main", BaseBranch: "main"},
		{Provider: "github", RepoID: "github:2", RepoName: "acme/web", CloneURL: "https://github.com/acme/web.git", DefaultBranch: "main", BaseBranch: "release"},
	}}
	return workViewRow{ID: "session", State: 2, Version: 3, CreationRequestID: "creation", StartRequestID: "creation", ThinkingLevel: "high",
		Capabilities: encodeJSON(t, snapshotRecord{StartupAgent: "reviewer"}), RepositorySelection: encodeJSON(t, selection), RepositoryCheckouts: "[]",
		HarnessProcess: encodeJSON(t, harnessProcessRecord{}), InitialTaskInput: encodeJSON(t, initialTaskInputRecord{ID: "01a1462f-06ff-79dc-9916-99a8e062c210", Text: "first task"})}
}

// The first start prepares the workspace until every selected repository has
// a checkout outcome on the Work, and hands off the initial task input.
func TestFirstStartShowsWorkspaceUntilEveryRepositoryHasAnOutcome(t *testing.T) {
	row := createdRow(t)
	row.RepositoryCheckouts = encodeJSON(t, []repositoryCheckoutRecord{{RepositoryID: "github:1", CheckedOut: true}})
	progress, err := startupFromRows(row, true, false, 9)
	require.NoError(t, err)
	require.Equal(t, "creation", progress.RequestID)
	require.True(t, progress.Admitted)
	require.Equal(t, "preparing", progress.Status)
	require.Equal(t, []query.StartupStage{{Stage: "admission", Status: "completed"}, {Stage: "sandbox", Status: "completed"}, {Stage: "workspace", Status: "active"}, {Stage: "agent", Status: "pending"}, {Stage: "input", Status: "pending"}}, progress.Stages)

	recovering, err := startupFromRows(row, true, true, 10)
	require.NoError(t, err)
	require.Equal(t, "recovering", recovering.Status, "a step the execution retries shows the start recovering")
	require.Greater(t, recovering.Revision, progress.Revision)

	row.RepositoryCheckouts = encodeJSON(t, []repositoryCheckoutRecord{{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", Reason: "timed_out"}})
	row.HarnessProcess = encodeJSON(t, harnessProcessRecord{Ready: true})
	ready, err := startupFromRows(row, true, false, 11)
	require.NoError(t, err)
	require.Equal(t, "input", ready.Stages[4].Stage)
	require.Equal(t, "active", ready.Stages[4].Status, "a degraded repository ends the workspace stage; the input waits for its hand-off")

	row.InitialTaskInput = ""
	completed, err := startupFromRows(row, true, false, 12)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
}

// A later start shows neither the first start's workspace and input stages
// nor earlier readiness, and orders after the earlier start.
func TestALaterStartShowsOnlyItsOwnStages(t *testing.T) {
	row := createdRow(t)
	row.HarnessProcess = encodeJSON(t, harnessProcessRecord{Ready: true})
	first, err := startupFromRows(row, true, false, 9)
	require.NoError(t, err)

	row.InitialTaskInput = ""
	row.Version++
	row.State = 1
	row.StartRequestID = "resume"
	row.HarnessProcess = encodeJSON(t, harnessProcessRecord{})
	resumed, err := startupFromRows(row, false, false, 1)
	require.NoError(t, err)
	require.Equal(t, "resume", resumed.RequestID)
	require.Equal(t, []query.StartupStage{{Stage: "admission", Status: "completed"}, {Stage: "sandbox", Status: "active"}, {Stage: "agent", Status: "pending"}}, resumed.Stages)
	require.Greater(t, resumed.Revision, first.Revision, "a new start orders after the earlier start whatever its execution's update time")
}

// A Restart that reaches its Startup Deadline shows the Work's own failure on
// the failed stage, while its incomplete first start still shows workspace
// and task stages.
func TestStartupDeadlineFailureOfARestartShowsTheWorkFailure(t *testing.T) {
	reason := "Startup Deadline reached before the execution environment was ready."
	row := createdRow(t)
	row.State, row.StartRequestID, row.Failure = 5, "restart", reason
	progress, err := startupFromRows(row, false, false, 1)
	require.NoError(t, err)
	require.Equal(t, "failed", progress.Status)
	require.Equal(t, reason, progress.Reason)
	require.Equal(t, []query.StartupStage{{Stage: "admission", Status: "completed"}, {Stage: "sandbox", Status: "failed"}, {Stage: "workspace", Status: "pending"}, {Stage: "agent", Status: "pending"}, {Stage: "input", Status: "pending"}}, progress.Stages)
}

// A Work created without a creation request record shows no startup.
func TestAWorkWithoutACreationRequestShowsNoStartupUntilItStarts(t *testing.T) {
	row := createdRow(t)
	row.CreationRequestID, row.StartRequestID, row.HarnessProcess = "", "", "null"
	progress, err := startupFromRows(row, false, false, 1)
	require.NoError(t, err)
	require.Nil(t, progress)
	request, err := startupRequestFromRow(row)
	require.NoError(t, err)
	require.Nil(t, request)
}

// The startup request is derived from the Work: its initial task input while
// held, its thinking level, capabilities and each repository's outcome.
func TestStartupRequestIsDerivedFromTheWorkFacts(t *testing.T) {
	row := createdRow(t)
	row.RepositoryCheckouts = encodeJSON(t, []repositoryCheckoutRecord{{RepositoryID: "github:2", Reason: "branch_unavailable"}})
	request, err := startupRequestFromRow(row)
	require.NoError(t, err)
	require.Equal(t, &query.WorkStartupRequest{RequestID: "creation", InitialMessage: "first task", InitialThinkingLevel: "high", StartupAgent: "reviewer",
		Repositories: []query.WorkStartupRepository{
			{RepositoryID: "github:1", Name: "acme/api", BaseBranch: "main"},
			{RepositoryID: "github:2", Name: "acme/web", BaseBranch: "release", CheckoutStatus: "not_checked_out", CheckoutReason: "branch_unavailable"},
		}}, request)

	row.InitialTaskInput = ""
	row.RepositoryCheckouts = encodeJSON(t, []repositoryCheckoutRecord{{RepositoryID: "github:1", CheckedOut: true}})
	request, err = startupRequestFromRow(row)
	require.NoError(t, err)
	require.Empty(t, request.InitialMessage, "a handed-off input is part of the conversation")
	require.Equal(t, "checked_out", request.Repositories[0].CheckoutStatus)
}
