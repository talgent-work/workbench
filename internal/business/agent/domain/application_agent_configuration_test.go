package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

const applicationOwner = "0193a7c2-6f1e-7c2a-9b3d-000000000001"

func applicationAgent(t *testing.T) *domain.AgentDefinition {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.ApplicationDefinitionInput(applicationID, "Support Desk"))
	require.NoError(t, err)
	return agent
}

var (
	theApplication = domain.DefinitionRequester{ID: applicationID}
	itsOwner       = domain.DefinitionRequester{ID: applicationOwner, OwnedApplication: applicationID}
	anotherOwner   = domain.DefinitionRequester{ID: "0193a7c2-6f1e-7c2a-9b3d-000000000002", OwnedApplication: "0193a7c2-6f1e-7c2a-9b3d-000000000003"}
	aStranger      = domain.DefinitionRequester{ID: applicationOwner}
)

// The Application and its Application Owner read and replace the Soul of the
// Application's Agent; an owner of another Application and a person Account
// did not name as its owner do not.
func TestTheApplicationAndItsOwnerConfigureItsSoul(t *testing.T) {
	agent := applicationAgent(t)
	for _, requester := range []domain.DefinitionRequester{theApplication, itsOwner} {
		changed, err := agent.ChangeSoul(requester, "Answer as "+requester.ID)
		require.NoError(t, err)
		require.True(t, changed)
		soul, err := agent.ReadSoul(requester)
		require.NoError(t, err)
		require.Equal(t, "Answer as "+requester.ID, soul)
	}
	for _, requester := range []domain.DefinitionRequester{anotherOwner, aStranger} {
		_, err := agent.ReadSoul(requester)
		require.ErrorIs(t, err, domain.ErrSoulForbidden)
		_, err = agent.ChangeSoul(requester, "Leak.")
		require.ErrorIs(t, err, domain.ErrSoulForbidden)
	}
	require.False(t, agent.IsOwner(applicationOwner), "the Application Owner is no Agent Owner")
}

// Only the Application Owner changes the name and icon of the Application's
// Agent, never the Application itself; a person's Work Agent admits no
// Application Owner.
func TestOnlyTheApplicationOwnerChangesItsAgentsAppearance(t *testing.T) {
	agent := applicationAgent(t)
	_, err := agent.ChangeAppearance(theApplication, "Desk Bot", domain.DefaultAgentIcon)
	require.ErrorIs(t, err, domain.ErrAgentAppearanceForbidden)
	_, err = agent.ChangeAppearance(anotherOwner, "Desk Bot", domain.DefaultAgentIcon)
	require.ErrorIs(t, err, domain.ErrAgentAppearanceForbidden)
	changed, err := agent.ChangeAppearance(itsOwner, "Desk Bot", domain.AgentIcon{Glyph: "rocket", Color: "blue"})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.AgentAppearance{Name: "Desk Bot", Icon: domain.AgentIcon{Glyph: "rocket", Color: "blue"}}, agent.Appearance())

	person, err := domain.NewAgentDefinition(domain.EmployeeDefinitionInput("employee-1"))
	require.NoError(t, err)
	_, err = person.ChangeAppearance(domain.DefinitionRequester{ID: applicationOwner, OwnedApplication: applicationID}, "Mine", domain.DefaultAgentIcon)
	require.ErrorIs(t, err, domain.ErrAgentAppearanceForbidden)
}

var offered = domain.ModelOffer{Provided: true, ThinkingLevels: []string{"low", "medium", "high"}}

// The Application and its Application Owner change its Agent's Work defaults:
// a provided model and a thinking level that model offers.
func TestTheApplicationAndItsOwnerChangeItsWorkDefaults(t *testing.T) {
	agent := applicationAgent(t)
	defaults := domain.WorkDefaults{StartupAgent: " support:triage ", ModelConfigurationID: " fast ", ThinkingLevel: " high "}
	changed, err := agent.ChangeWorkDefaults(itsOwner, defaults, offered)
	require.NoError(t, err)
	require.True(t, changed)
	saved := domain.WorkDefaults{StartupAgent: "support:triage", ModelConfigurationID: "fast", ThinkingLevel: "high"}
	require.Equal(t, saved, agent.WorkDefaults)
	read, err := agent.ReadWorkDefaults(theApplication)
	require.NoError(t, err)
	require.Equal(t, saved, read)

	changed, err = agent.ChangeWorkDefaults(theApplication, domain.WorkDefaults{StartupAgent: "support:triage"}, domain.ModelOffer{Provided: true})
	require.NoError(t, err)
	require.True(t, changed, "clearing the model and thinking level leaves the platform's")
	require.Equal(t, domain.WorkDefaults{StartupAgent: "support:triage"}, agent.WorkDefaults)

	_, err = agent.ChangeWorkDefaults(anotherOwner, defaults, offered)
	require.ErrorIs(t, err, domain.ErrWorkDefaultsForbidden)
	_, err = agent.ReadWorkDefaults(aStranger)
	require.ErrorIs(t, err, domain.ErrWorkDefaultsForbidden)
}

// A default model is one the platform provides, and a default thinking level
// one it offers; otherwise the defaults stay as they were.
func TestWorkDefaultsNameWhatIsOffered(t *testing.T) {
	agent := applicationAgent(t)
	_, err := agent.ChangeWorkDefaults(itsOwner, domain.WorkDefaults{ModelConfigurationID: "withdrawn"}, domain.ModelOffer{})
	require.ErrorIs(t, err, domain.ErrInvalidWorkDefaults)
	_, err = agent.ChangeWorkDefaults(itsOwner, domain.WorkDefaults{ModelConfigurationID: "fast", ThinkingLevel: "max"}, offered)
	require.ErrorIs(t, err, domain.ErrInvalidWorkDefaults)
	_, err = agent.ChangeWorkDefaults(itsOwner, domain.WorkDefaults{ModelConfigurationID: "Not An Id"}, offered)
	require.ErrorIs(t, err, domain.ErrInvalidWorkDefaults)
	require.Equal(t, domain.ApplicationDefinitionInput(applicationID, "").WorkDefaults, agent.WorkDefaults)
}

// Only an Application's Agent holds Work defaults its owner changes, and only
// it holds a default model and thinking level.
func TestOnlyAnApplicationsAgentHoldsAModelDefault(t *testing.T) {
	person, err := domain.NewAgentDefinition(domain.EmployeeDefinitionInput("employee-1"))
	require.NoError(t, err)
	_, err = person.ChangeWorkDefaults(domain.DefinitionRequester{ID: "employee-1"}, domain.WorkDefaults{StartupAgent: "x:y"}, offered)
	require.ErrorIs(t, err, domain.ErrWorkDefaultsForbidden)
	person.WorkDefaults.ModelConfigurationID = "fast"
	require.ErrorIs(t, person.Validate(), domain.ErrInvalidAgentDefinition)
}
