package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
)

func TestCreationCapturesOwnersRepositoryAndReuseIgnoresLaterBindingChanges(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "owner"}})
	require.NoError(t, err)
	store := &sessionStore{}
	catalog := &repositoryCatalog{}
	handler := command.NewCreateWorkHandler(time.Now, domain.BuiltInPlugins{}, agentStore{agent}, store, store,
		query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}}),
		commandtest.StartCatalog(emptyPluginCatalog{}, catalog), capacityCheck{}, store.preparation(query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}})))
	input := command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", AgentID: agent.ID, SubjectID: "owner", StartingUserID: "owner",
		InitialExecution: &command.PrepareWorkExecution{RequestID: uuid.NewString(),
			WorkBranch: "talgent/" + "TAL-382", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Prepare repositories"},
			Selections: []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "release"}}}}
	id, err := handler.Handle(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, "owner", catalog.owner)
	want := domain.WorkRepositorySelection{OwnerID: "owner", WorkBranch: "talgent/TAL-382", Repositories: []domain.WorkRepository{{Provider: "github", RepoID: "123", RepoName: "owner/project", CloneURL: "https://github.com/owner/project.git", DefaultBranch: "main", BaseBranch: "release"}}}
	require.Equal(t, want, store.session.RepositorySelection)
	catalog.err = errors.New("personal binding no longer exists")
	retained := *input.InitialExecution
	retained.Selections = []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "changed"}}
	changed := input
	changed.InitialExecution = &retained
	repeated, err := handler.Handle(t.Context(), changed)
	require.NoError(t, err)
	require.Equal(t, id, repeated, "a repeated request identity returns its Work, whose selection stays")
	reused, err := handler.Handle(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, id, reused)
	require.Equal(t, want, store.session.RepositorySelection)
}

type repositoryCatalog struct {
	authorizationcatalogtest.Empty
	owner string
	err   error
}

func (c *repositoryCatalog) ListAccessibleRepositories(_ context.Context, owner string) ([]domain.AccessibleRepository, error) {
	c.owner = owner
	return []domain.AccessibleRepository{{OwnerID: owner, Provider: "github", RepoID: "123",
		RepoName: owner + "/project", CloneURL: "https://github.com/" + owner + "/project.git", DefaultBranch: "main"}}, c.err
}
