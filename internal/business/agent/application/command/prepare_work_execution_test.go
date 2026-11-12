package command_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func repeatedPreparation(sessions *feedbackSessionStore) command.PrepareWorkExecution {
	return command.PrepareWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString(), WorkBranch: "talgent/TAL-368", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "original task"}}
}

// A first-start request for an existing Work returns the original Work and
// starts nothing: its repository choices are creation-specific, so other
// choices neither refuse the request nor replace the retained selection.
func TestPreparationReturnsTheOriginalWorkWhateverRepositoriesItAsksFor(t *testing.T) {
	_, sessions := startupStores(t)
	retained := domain.WorkRepositorySelection{OwnerID: "owner", WorkBranch: "talgent/TAL-368", Repositories: []domain.WorkRepository{
		{Provider: "github", RepoID: "github:1", RepoName: "acme/api", CloneURL: "https://github.com/acme/api.git", DefaultBranch: "main", BaseBranch: "main"},
	}}
	sessions.session.RepositorySelection = retained
	handler := command.NewPrepareWorkExecutionHandler(sessions, sessionAccess{}, agentquery.ModelConfigurations{})
	input := repeatedPreparation(sessions)
	input.Selections = []command.RepositorySelection{{RepositoryID: "github:1"}}

	unsupported := input
	unsupported.ThinkingLevel = "not-a-harness-level"
	_, err := handler.Handle(t.Context(), unsupported)
	require.ErrorIs(t, err, domain.ErrInvalidWork)

	changed := input
	changed.WorkBranch = "talgent/other"
	changed.Selections = []command.RepositorySelection{{RepositoryID: "github:1", BaseBranch: "release"}, {RepositoryID: "github:2"}}
	missing := input
	missing.Selections = nil
	for _, request := range []command.PrepareWorkExecution{input, changed, missing} {
		id, err := handler.Handle(t.Context(), request)
		require.NoError(t, err)
		require.Equal(t, input.WorkID, id)
	}
	require.Equal(t, retained, sessions.session.RepositorySelection, "checkout follows the retained selection")
	require.Zero(t, sessions.saves, "a repeated first start admits nothing")
	require.Empty(t, sessions.admittedStarts())
}

func TestPreparationForClosedSessionDoesNotAcquireResources(t *testing.T) {
	_, sessions := startupStores(t)
	report(t, sessions.session, func(workID string, start time.Time) domain.Event {
		return domain.ExecutionExited{WorkID: workID, StartAdmittedAt: start, Exit: domain.HarnessExit{ExitCode: 1}}
	})
	report(t, sessions.session, releasedReport)
	require.NoError(t, sessions.session.Close("already-closed", domain.ClosureFacts{}))
	prepare := command.NewPrepareWorkExecutionHandler(sessions, sessionAccess{}, agentquery.ModelConfigurations{})
	_, err := prepare.Handle(t.Context(), repeatedPreparation(sessions))
	require.ErrorIs(t, err, domain.ErrWorkTransition)
	require.Equal(t, domain.WorkClosed, sessions.session.State)
	require.Zero(t, sessions.saves)
}
