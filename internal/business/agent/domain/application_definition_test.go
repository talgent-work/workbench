package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

const applicationID = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e"

// An Application's Agent is owned by the Application alone, named after it,
// and represents no person: its Works act as the Application, run only on a
// Cloud and roll up to no person's usage.
func TestAnApplicationsAgentIsTheApplications(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.ApplicationDefinitionInput(" "+applicationID+" ", " Support Desk "))
	require.NoError(t, err)
	require.Equal(t, applicationID, agent.OwningApplication())
	require.Equal(t, []string{applicationID}, agent.Owners)
	require.True(t, agent.IsOwner(applicationID))
	require.Equal(t, "Support Desk", agent.AgentName)
	require.Equal(t, domain.StandaloneWorkPurpose, agent.Purpose)
	require.Equal(t, applicationID, agent.WorkPrincipal(), "its Works act as the Application")
	require.Empty(t, agent.RepresentedPerson())
	require.False(t, agent.RepresentsHuman())
	attributes := agent.WorkTraceAttributes(map[string]string{"talgent.application_id": "someone-else", "ticket": "T-1"})
	require.NotContains(t, attributes, "talgent.user_id")
	require.Equal(t, applicationID, attributes["talgent.application_id"], "its Works' usage rolls up to the Application, whatever a caller supplied")
	require.Equal(t, "T-1", attributes["ticket"])
	require.ErrorIs(t, agent.AdmitComputer(true), domain.ErrCloudOnlyAgent)
	require.Equal(t, domain.EmployeeDefinitionInput("employee-1").WorkDefaults, agent.WorkDefaults, "it starts its Works with the Standalone Work role")
}

// A person's Agent owns no Application, and an Application's Agent keeps the
// Application as its only Owner: its Owners never transfer, and no other
// Owner validates beside it.
func TestAnApplicationsAgentKeepsItsOwner(t *testing.T) {
	person, err := domain.NewAgentDefinition(domain.EmployeeDefinitionInput("employee-1"))
	require.NoError(t, err)
	require.Empty(t, person.OwningApplication())

	agent, err := domain.NewAgentDefinition(domain.ApplicationDefinitionInput(applicationID, "Support Desk"))
	require.NoError(t, err)
	_, err = agent.TransferOwners([]string{"employee-1"})
	require.ErrorIs(t, err, domain.ErrInvalidAgentDefinition)
	require.Equal(t, []string{applicationID}, agent.Owners)

	agent.Owners = []string{applicationID, "employee-1"}
	require.ErrorIs(t, agent.Validate(), domain.ErrInvalidAgentDefinition)
}
