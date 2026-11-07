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

// A first-start request for an existing Work confirms it while it asks for
// the repositories the Work retained, and starts nothing.
func TestPreparationConfirmsAnExistingWorkWithItsRetainedRepositories(t *testing.T) {
	_, sessions := startupStores(t)
	sessions.session.RepositorySelection = domain.WorkRepositorySelection{OwnerUserID: "owner", WorkBranch: "talgent/TAL-368", Repositories: []domain.WorkRepository{
		{Provider: "github", RepoID: "github:1", RepoName: "acme/api", CloneURL: "https://github.com/acme/api.git", DefaultBranch: "main", BaseBranch: "main"},
	}}
	handler := command.NewPrepareWorkExecutionHandler(sessions, sessionAccess{}, agentquery.ModelConfigurations{})
	input := repeatedPreparation(sessions)
	input.Selections = []command.RepositorySelection{{RepositoryID: "github:1"}}

	unsupported := input
	unsupported.ThinkingLevel = "not-a-harness-level"
	_, err := handler.Handle(t.Context(), unsupported)
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	changed := input
	changed.Selections = []command.RepositorySelection{{RepositoryID: "github:1", BaseBranch: "release"}}
	_, err = handler.Handle(t.Context(), changed)
	require.ErrorIs(t, err, command.ErrInitialExecutionConflict, "checkout follows the retained selection")
	missing := input
	missing.Selections = nil
	_, err = handler.Handle(t.Context(), missing)
	require.ErrorIs(t, err, command.ErrInitialExecutionConflict)

	id, err := handler.Handle(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, input.WorkID, id)
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
