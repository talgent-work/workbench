package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
)

var errVariablesUnreadable = errors.New("personal variables unreadable")

// unreadableVariables fails every read of a person's Personal Variables, as a
// sealing key problem would.
type unreadableVariables struct{ commandtest.PersonalVariables }

func (*unreadableVariables) Get(context.Context, string) (*domain.PersonalVariableSet, error) {
	return nil, errVariablesUnreadable
}

func variableChoiceHandler(t *testing.T, store *sessionStore) (*domain.AgentDefinition, *command.CreateWorkHandler) {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}})
	require.NoError(t, err)
	require.NotEmpty(t, agent.RepresentedPerson())
	models := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: deploymentModel, ThinkingLevels: []string{"low"}, DefaultThinkingLevel: "low"})
	computers := capacityCheck{others: []command.UsableComputer{{ID: managedComputer}}}
	return agent, command.NewCreateWorkHandler(time.Now, domain.BuiltInPlugins{}, &unreadableVariables{}, agentStore{agent}, store, store, models, commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), computers, store.preparation(models))
}

// A new Work of a represented person whose Personal Variables cannot be read
// is not created: the start fails rather than enabling a guessed selection.
// A start that chose none needs no read and creates the Work enabling none.
func TestANewWorkFailsWhenItsPersonsVariablesCannotBeRead(t *testing.T) {
	for _, choice := range []domain.PersonalVariableChoice{{}, {Chosen: true, Names: []string{"API_TOKEN"}}} {
		store := &sessionStore{}
		agent, handler := variableChoiceHandler(t, store)
		creation := modelCreation(agent.ID, "", deploymentModel, "")
		creation.PersonalVariables = choice
		_, err := handler.Handle(t.Context(), creation)
		require.ErrorIs(t, err, errVariablesUnreadable)
		require.Nil(t, store.session, "no Work is created")
	}

	store := &sessionStore{}
	agent, handler := variableChoiceHandler(t, store)
	creation := modelCreation(agent.ID, "", deploymentModel, "")
	creation.PersonalVariables = domain.PersonalVariableChoice{Chosen: true}
	_, err := handler.Handle(t.Context(), creation)
	require.NoError(t, err)
	require.Empty(t, store.session.VariableSelection.Names)
}
