package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// An employee's Agent represents that employee, so every Work of it, a
// caller's or a Personal Work, rolls up to that person's usage.
func TestAnEmployeeAgentRepresentsTheEmployee(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.EmployeeDefinitionInput("employee-1"))
	require.NoError(t, err)
	require.Equal(t, "employee-1", agent.PersonalAuthorizationOwner())
}
