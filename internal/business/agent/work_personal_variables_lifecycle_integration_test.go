//go:build integration

package agent_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// keepVariables saves the person's Personal Variables as they set them.
func keepVariables(t *testing.T, m *lifecycleModule, person string, change func(*domain.PersonalVariableSet)) {
	t.Helper()
	sets := personalVariableSets(t, m)
	set, err := sets.Get(m.ctx, person)
	if err != nil {
		require.ErrorIs(t, err, domain.ErrPersonalVariableSetNotFound)
		set, err = domain.NewPersonalVariableSet(person)
		require.NoError(t, err)
	}
	change(set)
	require.NoError(t, sets.Save(m.ctx, set))
}

// Each start of a Work takes the current values of its represented person's
// Personal Variables and keeps them sealed, apart from its LaunchSpecification;
// a started run keeps them whatever the person changes, and the next start
// takes the edits and the deletions.
func TestEachStartTakesTheRepresentedPersonsCurrentPersonalVariables(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	stopped := seedWork(t, m.db, definition, workSeed{subject: "stopped", state: domain.WorkStopped})
	failed := seedWork(t, m.db, definition, workSeed{subject: "failed", state: domain.WorkFailed})
	m.start()
	keepVariables(t, m, workOwner, func(set *domain.PersonalVariableSet) {
		for _, variable := range []domain.PersonalVariable{{Name: "API_TOKEN", Value: "first-secret"}, {Name: "ANTHROPIC_MODEL", Value: "person-model"}} {
			_, err := set.SetVariable(workOwner, variable)
			require.NoError(t, err)
		}
	})

	require.NoError(t, m.resume(workOwner, stopped.ID))
	resumed := m.runs.await(t, m.ctx, stopped.ID, func(e *domain.AgentExecution) bool { return e.ID != stopped.execution.ID }, "the Resume begins its execution")
	require.Equal(t, []domain.PersonalVariable{{Name: "ANTHROPIC_MODEL", Value: "person-model"}, {Name: "API_TOKEN", Value: "first-secret"}}, resumed.PersonalVariables)
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
	require.Equal(t, []domain.PersonalVariable{{Name: "API_TOKEN", Value: "second-secret"}}, restarted.PersonalVariables, "the next start takes the edits and deletions")
}
