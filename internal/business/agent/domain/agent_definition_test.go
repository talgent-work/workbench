package domain_test

import (
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// definitionSessionLookup stands in for what the Application looks up before
// a creation: the Work found for the request's Work Key, and the Works a
// starting context may name.
type definitionSessionLookup struct {
	session *domain.AgentWork
	works   []*domain.AgentWork
}

// createWork creates a Work with what the lookup found for its request.
func createWork(definition *domain.AgentDefinition, request domain.WorkCreationRequest, lookup *definitionSessionLookup) (*domain.AgentWork, bool, error) {
	return definition.CreateWork(request, lookup.facts(request))
}

func (p *definitionSessionLookup) facts(request domain.WorkCreationRequest) domain.WorkCreationFacts {
	facts := domain.WorkCreationFacts{}
	if request.WorkKey.Present() {
		facts.KeyWork = p.session
	}
	if source := request.StartingContext; source != nil {
		for _, work := range p.works {
			if work.ID == source.WorkID {
				facts.SourceWork = work
			}
		}
	}
	return facts
}

// A project-scoped Agent's Works have a Session Workspace like every Cloud
// Work, which no caller opts out of.
func TestProjectWorkHasSessionWorkspace(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "project_coordination",
		UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"},
		Visibility: domain.VisibilityRule{Public: true}})
	require.NoError(t, err)
	work, _, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: "owner", WorkCaller: domain.WorkCaller{Kind: "project", ID: "project-a"}, WorkKey: domain.WorkKey{Kind: "project", ID: "project-a"},
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"},
		Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}}, domain.WorkCreationFacts{})
	require.NoError(t, err)
	workspace, ok := work.LocatedLayout.Workspace()
	require.True(t, ok)
	require.Equal(t, "workspace", workspace.Workspace.Name)
	require.True(t, work.Visibility.Public)
}

func TestDefinitionAppliesSuppliedConfigurationWithoutInterpretingPurpose(t *testing.T) {
	for _, purpose := range []string{"work", "project_manager", "caller-defined"} {
		t.Run(purpose, func(t *testing.T) {
			agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"},
				Purpose:    purpose,
				UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"},
			})
			require.NoError(t, err)
			for _, model := range []domain.WorkModelSelection{{ConfigurationID: "default"}, {ConfigurationID: "selected"}} {
				created, _, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}, WorkModel: model, WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case"}}, domain.WorkCreationFacts{})
				require.NoError(t, err)
				require.Equal(t, model, created.WorkModel)
			}
		})
	}
}

// Only one of the Agent's Owners starts its Works; Owners are a normalized,
// non-empty set that never names the Agent itself.
func TestOnlyAnAgentOwnerStartsItsWorks(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{" b ", "a", "b"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "a"}})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, agent.Owners)
	_, err = domain.NewAgentDefinition(domain.AgentDefinitionInput{Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "a"}})
	require.Error(t, err, "an Agent has at least one Owner")
	_, err = agent.TransferOwners(nil)
	require.Error(t, err)
	_, err = agent.TransferOwners([]string{agent.ID})
	require.Error(t, err, "an Agent never owns itself")
	changed, err := agent.TransferOwners([]string{"b", "a"})
	require.NoError(t, err)
	require.False(t, changed, "the same set is no change")

	start := func(startedBy string) error {
		_, _, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: startedBy, WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case"},
			Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"},
			Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}}, domain.WorkCreationFacts{})
		return err
	}
	require.ErrorIs(t, start("stranger"), domain.ErrControlRequesterNotOwner)
	require.NoError(t, start("b"))
}

// A person's Agent acts for that person; a project-scoped Agent acts as itself,
// never as the human who owns it.
func TestWorkPrincipalIsThePersonOrTheDefinitionItself(t *testing.T) {
	person, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}})
	require.NoError(t, err)
	require.Equal(t, "alice", person.WorkPrincipal())
	pm, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "project_coordination",
		UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"}})
	require.NoError(t, err)
	require.Equal(t, pm.ID, pm.WorkPrincipal())
	require.False(t, pm.IsOwner(pm.WorkPrincipal()), "a project-scoped Agent's Works never act with its Owner's authority")
}

// A person's Agent runs on any Computer usable to them; a project-scoped Agent's
// Works, of an Agent that represents no person, run only on a Cloud.
func TestOnlyAPersonsAgentRunsOnAManagedComputer(t *testing.T) {
	person, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}})
	require.NoError(t, err)
	require.NoError(t, person.AdmitComputer(true))
	pm, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "project_coordination",
		UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"}})
	require.NoError(t, err)
	require.NoError(t, pm.AdmitComputer(false))
	require.ErrorIs(t, pm.AdmitComputer(true), domain.ErrCloudOnlyAgent)
	_, _, err = pm.CreateWork(domain.WorkCreationRequest{StartedBy: "alice", WorkCaller: domain.WorkCaller{Kind: "project", ID: "project-a"}, WorkKey: domain.WorkKey{Kind: "project", ID: "project-a"}, ComputerManaged: true,
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"},
		Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: pm.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}}, domain.WorkCreationFacts{})
	require.ErrorIs(t, err, domain.ErrComputerNotUsable, "the definition refuses a Managed Work itself")
}
