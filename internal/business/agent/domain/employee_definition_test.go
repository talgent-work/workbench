package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// An employee's Agent represents that employee, so every Work of it, a
// caller's or a Standalone Work, rolls up to that person's usage.
func TestAnEmployeeAgentRepresentsTheEmployee(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.EmployeeDefinitionInput("employee-1"))
	require.NoError(t, err)
	require.Equal(t, "employee-1", agent.RepresentedPerson())
}

// Only the employee's `work`-purpose definition represents the employee: a
// definition of the same employee for another purpose represents no person,
// so its Works neither use the employee's personal authorizations nor roll up
// to their usage.
func TestAnEmployeeDefinitionOfAnotherPurposeRepresentsNoPerson(t *testing.T) {
	input := domain.EmployeeDefinitionInput("employee-1")
	input.Purpose = "reviewer"
	agent, err := domain.NewAgentDefinition(input)
	require.NoError(t, err)
	require.Empty(t, agent.RepresentedPerson())
	attributes := agent.WorkTraceAttributes(map[string]string{"talgent.application_id": "a-caller-claim"})
	require.NotContains(t, attributes, "talgent.user_id")
	require.NotContains(t, attributes, "talgent.application_id", "no caller attributes a Work to an Application")
}
