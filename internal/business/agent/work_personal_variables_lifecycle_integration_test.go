//go:build integration

package agent_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// keepVariables saves the person's Personal Variables as they set them.
func keepVariables(t *testing.T, m *lifecycleModule, person string, change func(*domain.PersonalVariableSet)) {
	t.Helper()
	sets := personalVariableSets(t, m)
	set, err := sets.Get(m.ctx, person)
	require.NoError(t, err)
	change(set)
	require.NoError(t, sets.Save(m.ctx, set))
}

// Each start of a Work takes the current values of the Personal Variables its
// Work Variable Selection names and keeps them sealed, apart from its
// LaunchSpecification; a started run keeps them whatever the person changes.
// The Work keeps its selection on Resume and Restart: a variable its person
// adds later stays out, the next start takes the edits, and a deleted
// selected variable is skipped without failing the start.
func TestEachStartTakesTheCurrentValuesOfItsSelectedPersonalVariables(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	selected := []string{"ANTHROPIC_MODEL", "API_TOKEN"}
	stopped := seedWork(t, m.db, definition, workSeed{subject: "stopped", state: domain.WorkStopped, variables: selected})
	failed := seedWork(t, m.db, definition, workSeed{subject: "failed", state: domain.WorkFailed, variables: selected})
	m.start()
	keepVariables(t, m, workOwner, func(set *domain.PersonalVariableSet) {
		for _, variable := range []domain.PersonalVariable{{Name: "API_TOKEN", Value: "first-secret"}, {Name: "ANTHROPIC_MODEL", Value: "person-model"}, {Name: "LATER_ADDED", Value: "later"}} {
			_, err := set.SetVariable(workOwner, variable)
			require.NoError(t, err)
		}
	})

	require.NoError(t, m.resume(workOwner, stopped.ID))
	resumed := m.runs.await(t, m.ctx, stopped.ID, func(e *domain.AgentExecution) bool { return e.ID != stopped.execution.ID }, "the Resume begins its execution")
	require.Equal(t, []domain.PersonalVariable{{Name: "ANTHROPIC_MODEL", Value: "person-model"}, {Name: "API_TOKEN", Value: "first-secret"}}, resumed.PersonalVariables, "a variable added later stays out of the Work")
	var row struct {
		PersonalVariables []byte `xorm:"personal_variables"`
		RequestJSON       string `xorm:"request_json"`
	}
	found, err := m.db.SQL("SELECT personal_variables, request_json FROM agent_execution WHERE id = ?", resumed.ID).Get(&row)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, row.PersonalVariables)
	require.NotContains(t, string(row.PersonalVariables), "first-secret", "the run's values are sealed at rest")
	require.NotContains(t, row.RequestJSON, "first-secret", "the LaunchSpecification carries no value")
	require.NotContains(t, row.RequestJSON, "API_TOKEN")

	keepVariables(t, m, workOwner, func(set *domain.PersonalVariableSet) {
		_, err := set.SetVariable(workOwner, domain.PersonalVariable{Name: "API_TOKEN", Value: "second-secret"})
		require.NoError(t, err)
		_, err = set.RemoveVariable(workOwner, "ANTHROPIC_MODEL")
		require.NoError(t, err)
	})
	kept := m.runs.latest(t, m.ctx, stopped.ID)
	require.Equal(t, resumed.ID, kept.ID)
	require.Equal(t, resumed.PersonalVariables, kept.PersonalVariables, "a started run keeps the values it took")

	require.NoError(t, m.restartWork(workOwner, failed.ID))
	restarted := m.runs.await(t, m.ctx, failed.ID, func(e *domain.AgentExecution) bool { return e.ID != failed.execution.ID }, "the Restart begins its execution")
	require.Equal(t, []domain.PersonalVariable{{Name: "API_TOKEN", Value: "second-secret"}}, restarted.PersonalVariables, "the next start takes the edits and skips the deleted variable")
	work, err := m.app.Works.Get(m.ctx, failed.ID)
	require.NoError(t, err)
	require.Equal(t, selected, work.VariableSelection.Names, "the Work keeps its selection, a deleted name included")
}

// A new Work enables the Personal Variables its start chose among those its
// person keeps, through either start: none chosen enables all, an empty
// choice none, and a name the person does not keep is ignored. A creation
// that reuses a Work by its Work Key keeps that Work's selection.
func TestANewWorkEnablesThePersonalVariablesItsStartChose(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	m.start()
	keepVariables(t, m, workOwner, func(set *domain.PersonalVariableSet) {
		for _, name := range []string{"A_TOKEN", "B_TOKEN"} {
			_, err := set.SetVariable(workOwner, domain.PersonalVariable{Name: name, Value: strings.ToLower(name)})
			require.NoError(t, err)
		}
	})
	created := func(subject string, selection *agentpublicv1.PersonalVariableSelection) string {
		t.Helper()
		request := subjectCreation(definition.ID, subject)
		request.PersonalVariables = selection
		response, err := m.client.CreateWork(m.control(), request)
		require.NoError(t, err)
		return response.GetWorkId()
	}
	firstRun := func(workID string) []domain.PersonalVariable {
		t.Helper()
		return m.runs.await(t, m.ctx, workID, func(*domain.AgentExecution) bool { return true }, "the new Work begins its execution").PersonalVariables
	}
	selection := func(workID string) []string {
		t.Helper()
		work, err := m.app.Works.Get(m.ctx, workID)
		require.NoError(t, err)
		return work.VariableSelection.Names
	}

	all := created("all", nil)
	require.Equal(t, []domain.PersonalVariable{{Name: "A_TOKEN", Value: "a_token"}, {Name: "B_TOKEN", Value: "b_token"}}, firstRun(all))
	some := created("some", &agentpublicv1.PersonalVariableSelection{Names: []string{"B_TOKEN", "NOT_KEPT"}})
	require.Equal(t, []domain.PersonalVariable{{Name: "B_TOKEN", Value: "b_token"}}, firstRun(some))
	require.Equal(t, []string{"B_TOKEN"}, selection(some))
	none := created("none", &agentpublicv1.PersonalVariableSelection{})
	require.Empty(t, firstRun(none))
	require.Empty(t, selection(none))

	keepVariables(t, m, workOwner, func(set *domain.PersonalVariableSet) {
		_, err := set.SetVariable(workOwner, domain.PersonalVariable{Name: "C_TOKEN", Value: "c"})
		require.NoError(t, err)
	})
	require.Equal(t, some, created("some", nil), "the Work Key reuses its Work")
	require.Equal(t, []string{"B_TOKEN"}, selection(some), "a reused Work keeps its selection")

	person := m.as("user", uuid.NewString())
	require.NoError(t, person.setVariable("OWN_TOKEN", "own"))
	require.NoError(t, person.setVariable("OTHER_TOKEN", "other"))
	response, err := person.works.StartWork(person.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Draft my plan",
		PersonalVariables: &agentpublicv1.PersonalVariableSelection{Names: []string{"OWN_TOKEN"}}})
	require.NoError(t, err)
	require.Equal(t, []domain.PersonalVariable{{Name: "OWN_TOKEN", Value: "own"}}, firstRun(response.GetWorkId()))
}
