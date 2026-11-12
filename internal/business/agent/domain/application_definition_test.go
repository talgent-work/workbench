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
	require.NotContains(t, agent.WorkTraceAttributes(nil), "talgent.user_id")
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

// Removed Application admits nothing: once Agent records the removal, the
// Application is admitted as no Owner, and only Agent itself may close its
// Works for it. A person's Agent has no Application to lose.
func TestARemovedApplicationActsAsNoOwner(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.ApplicationDefinitionInput(applicationID, "Support Desk"))
	require.NoError(t, err)
	require.False(t, agent.AdmitsRemovedApplicationClose(applicationID))

	changed, err := agent.RecordApplicationRemoved()
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, agent.IsOwner(applicationID))
	require.Empty(t, agent.AdmittedOwners())
	require.Equal(t, []string{applicationID}, agent.Owners, "the Agent stays the Application's")
	require.True(t, agent.AdmitsRemovedApplicationClose(applicationID))
	require.False(t, agent.AdmitsRemovedApplicationClose("0193a7c2-6f1e-7c2a-9b3d-000000000000"))
	require.NoError(t, agent.Validate())
	changed, err = agent.RecordApplicationRemoved()
	require.NoError(t, err)
	require.False(t, changed, "repeating it changes nothing")

	person, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"0193a7c2-6f1e-7c2a-9b3d-0000000000aa"}, Purpose: domain.StandaloneWorkPurpose, UsageScope: domain.UsageScope{Kind: "employee", Reference: "0193a7c2-6f1e-7c2a-9b3d-0000000000aa"}})
	require.NoError(t, err)
	_, err = person.RecordApplicationRemoved()
	require.ErrorIs(t, err, domain.ErrInvalidAgentDefinition)
}
