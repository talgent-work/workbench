package domain_test

import (
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// definitionSessionLookup stands in for what the Application looks up before
// a creation: the Work found for the request's Subject, and the Works a
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
	if request.Subject.Present() {
		facts.SubjectWork = p.session
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

// A Work is located by its definition and Subject alone: an existing Work,
// closed or not, is returned whatever the request's capabilities, and only a
// new Subject establishes a new Work.
func TestDefinitionCreatesAndReusesWorksBySubject(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work",
		UsageScope:   domain.UsageScope{Kind: "employee", Reference: "employee-a"},
		HomeBinding:  domain.HomeBinding{OwnerID: "employee-a", Namespace: "personal"},
		Capabilities: domain.CapabilityConfiguration{Prompt: "Base", StartupAgent: "employees:member"},
	})
	require.NoError(t, err)
	lookup := &definitionSessionLookup{}
	subject := domain.WorkSubject{Kind: "case", ID: "case-a"}
	request := domain.WorkCreationRequest{StartedBy: "owner", Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, WorkModel: domain.WorkModelSelection{ConfigurationID: "deployment-default"}, Subject: subject, ProjectID: "project-a", WorkspaceReference: "workspace-a", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Prompt: "Specific"}}
	unresolved := request
	unresolved.WorkModel = domain.WorkModelSelection{}
	session, isNew, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.True(t, isNew)
	require.Equal(t, domain.WorkStarting, session.State)
	require.Equal(t, agent.ID, session.AgentID)
	require.Equal(t, agent.HomeBinding, session.HomeBinding)
	require.Equal(t, "Specific", session.CapabilitySnapshot.Prompt)
	require.Equal(t, subject, session.Subject)
	require.Equal(t, "project-a", session.ProjectID)

	require.NoError(t, agent.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "Changed", StartupAgent: "other:role"}))
	lookup.session = session
	request.Capabilities = domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, StartupAgent: "missing:role"}
	reused, isNew, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.False(t, isNew, "the existing Work wins before new capabilities are validated")
	require.Same(t, session, reused)
	_, _, err = createWork(agent, unresolved, lookup)
	require.NoError(t, err, "reuse retains the original model without requiring a new selection")
	require.Equal(t, "Specific", reused.CapabilitySnapshot.Prompt)
	require.Equal(t, domain.WorkStarting, reused.State, "lookup must not admit execution")

	session.State = domain.WorkClosed
	closed, isNew, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.False(t, isNew, "a closed Work still holds its Subject")
	require.Same(t, session, closed)

	lookup.session = nil
	request.Subject = domain.WorkSubject{Kind: "case", ID: "case-new"}
	request.Capabilities = domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Prompt: "Changed"}
	fresh, isNew, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.True(t, isNew)
	require.NotEqual(t, session.ID, fresh.ID)
	require.NotEqual(t, session.HarnessSessionReference, fresh.HarnessSessionReference)
	require.Equal(t, "Changed", fresh.CapabilitySnapshot.Prompt)

	request.Subject = domain.WorkSubject{Kind: "milestone", ID: "m"}
	opaque, isNew, err := createWork(agent, request, lookup)
	require.NoError(t, err, "the caller defines the kind; Agent does not interpret it")
	require.True(t, isNew)
	require.Equal(t, request.Subject, opaque.Subject)
	request.Subject = domain.WorkSubject{Kind: "review"}
	_, _, err = createWork(agent, request, lookup)
	require.ErrorIs(t, err, domain.ErrInvalidWorkSubject, "a Subject names its object")

	request.Subject = subject
	lookup.session = &domain.AgentWork{ID: "other-subject", AgentID: agent.ID, Subject: domain.WorkSubject{Kind: "case", ID: "case-other"}}
	other, isNew, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.True(t, isNew, "a Work found for another Subject is never reused")
	require.NotEqual(t, "other-subject", other.ID)
	lookup.session = &domain.AgentWork{ID: "other-definition", AgentID: "other", Subject: subject}
	_, isNew, err = createWork(agent, request, lookup)
	require.NoError(t, err)
	require.True(t, isNew, "nor one of another definition")
}

// A Work without a Subject is never located by one: each creation
// establishes its own Work and keeps the trace attributes its caller
// supplied. Without a Project it is a Personal Work: private, with no
// read-only mount; with one it keeps the mounts its caller supplied.
func TestWorkWithoutSubjectIsNeverReused(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work",
		UsageScope:   domain.UsageScope{Kind: "employee", Reference: "owner"},
		HomeBinding:  domain.HomeBinding{OwnerID: "owner", Namespace: "personal"},
		Capabilities: domain.CapabilityConfiguration{Prompt: "Base", StartupAgent: "employees:member"},
		Visibility:   domain.VisibilityRule{Public: true},
	})
	require.NoError(t, err)
	existing := &domain.AgentWork{ID: "existing"}
	lookup := &definitionSessionLookup{session: existing}
	request := domain.WorkCreationRequest{StartedBy: "owner", Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true},
		WorkModel: domain.WorkModelSelection{ConfigurationID: "deployment-default"}, WorkspaceReference: "workspace-a",
		TraceAttributes: map[string]string{"talgent.case_id": "supplied"},
		Capabilities:    domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}}
	first, isNew, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.True(t, isNew, "no Subject locates the existing Work")
	second, isNew, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.True(t, isNew)
	require.NotEqual(t, first.ID, second.ID)
	require.False(t, first.Subject.Present())
	require.False(t, first.Visibility.Public, "a Work without a Project is private whatever its definition declares")
	_, err = first.ChangeVisibility(agent, "owner", true, time.Now())
	require.ErrorIs(t, err, domain.ErrWorkWithoutProject)
	attributes := follow(second).launch().TraceAttributes
	require.Equal(t, "supplied", attributes["talgent.case_id"], "Agent carries the caller's attributes unchanged")
	require.NotContains(t, attributes, "talgent.subject_kind")
	require.NotContains(t, attributes, "talgent.project_id")

	request.ReadOnlyMounts = []domain.ReadOnlyMount{{Name: "reference", Reference: "space-1"}}
	_, _, err = createWork(agent, request, lookup)
	require.ErrorIs(t, err, domain.ErrInvalidWork, "a Personal Work has no read-only mount")
	request.ProjectID = "project-a"
	withProject, _, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.Equal(t, request.ReadOnlyMounts, withProject.ReadOnlyMounts)

	for _, mounts := range [][]domain.ReadOnlyMount{{{Name: "inputs", Reference: "x"}}, {{Name: "a/b", Reference: "x"}}, {{Name: "a", Reference: ""}}, {{Name: "a", Reference: "x"}, {Name: "a", Reference: "y"}}} {
		request.ReadOnlyMounts = mounts
		_, _, err = createWork(agent, request, lookup)
		require.ErrorIs(t, err, domain.ErrInvalidWork, "mounts %v", mounts)
	}
}

// A project-scoped Agent's Works have no Session Workspace: a Work established
// without a workspace reference starts without one.
func TestWorkWithoutSessionWorkspace(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "project_coordination",
		UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"}, HomeBinding: domain.HomeBinding{OwnerID: "project/project-a/project_coordination", Namespace: "project_coordination"},
		Visibility: domain.VisibilityRule{Public: true}})
	require.NoError(t, err)
	work, _, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: "owner", Subject: domain.WorkSubject{Kind: "project", ID: "project-a"}, ProjectID: "project-a",
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"},
		Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}}, domain.WorkCreationFacts{})
	require.NoError(t, err)
	require.Empty(t, work.WorkspaceReference)
	require.True(t, work.Visibility.Public)
}

func TestDefinitionAppliesSuppliedConfigurationWithoutInterpretingPurpose(t *testing.T) {
	for _, purpose := range []string{"work", "project_manager", "caller-defined"} {
		t.Run(purpose, func(t *testing.T) {
			agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"},
				Purpose:     purpose,
				UsageScope:  domain.UsageScope{Kind: "employee", Reference: "employee"},
				HomeBinding: domain.HomeBinding{OwnerID: "employee", Namespace: "personal"},
			})
			require.NoError(t, err)
			for _, model := range []domain.WorkModelSelection{{ConfigurationID: "default"}, {ConfigurationID: "selected"}} {
				created, _, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, WorkModel: model, WorkspaceReference: "workspace", Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project"}, domain.WorkCreationFacts{})
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
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "a"}, HomeBinding: domain.HomeBinding{OwnerID: "a", Namespace: "personal"}})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, agent.Owners)
	_, err = domain.NewAgentDefinition(domain.AgentDefinitionInput{Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "a"}, HomeBinding: domain.HomeBinding{OwnerID: "a", Namespace: "personal"}})
	require.Error(t, err, "an Agent has at least one Owner")
	_, err = agent.TransferOwners(nil)
	require.Error(t, err)
	_, err = agent.TransferOwners([]string{agent.ID})
	require.Error(t, err, "an Agent never owns itself")
	changed, err := agent.TransferOwners([]string{"b", "a"})
	require.NoError(t, err)
	require.False(t, changed, "the same set is no change")

	start := func(startedBy string) error {
		_, _, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: startedBy, Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkspaceReference: "workspace",
			Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"},
			Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}}, domain.WorkCreationFacts{})
		return err
	}
	require.ErrorIs(t, start("stranger"), domain.ErrControlRequesterNotOwner)
	require.NoError(t, start("b"))
}

// A person's Agent acts for that person; a project-scoped Agent acts as itself,
// never as the human who owns it.
func TestWorkPrincipalIsThePersonOrTheDefinitionItself(t *testing.T) {
	person, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}, HomeBinding: domain.HomeBinding{OwnerID: "alice", Namespace: "personal"}})
	require.NoError(t, err)
	require.Equal(t, "alice", person.WorkPrincipal())
	pm, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "project_coordination",
		UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"}, HomeBinding: domain.HomeBinding{OwnerID: "project/project-a/project_coordination", Namespace: "project_coordination"}})
	require.NoError(t, err)
	require.Equal(t, pm.ID, pm.WorkPrincipal())
	require.False(t, pm.IsOwner(pm.WorkPrincipal()), "a project-scoped Agent's Works never act with its Owner's authority")
}

// A person's Agent runs on any Computer usable to them; a project-scoped Agent's
// Works, of an Agent that represents no person, run only on a Cloud.
func TestOnlyAPersonsAgentRunsOnAManagedComputer(t *testing.T) {
	person, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}, HomeBinding: domain.HomeBinding{OwnerID: "alice", Namespace: "project"}})
	require.NoError(t, err)
	require.NoError(t, person.AdmitComputer(true))
	pm, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "project_coordination",
		UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"}, HomeBinding: domain.HomeBinding{OwnerID: "project/project-a/project_coordination", Namespace: "project_coordination"}})
	require.NoError(t, err)
	require.NoError(t, pm.AdmitComputer(false))
	require.ErrorIs(t, pm.AdmitComputer(true), domain.ErrCloudOnlyAgent)
	_, _, err = pm.CreateWork(domain.WorkCreationRequest{StartedBy: "alice", Subject: domain.WorkSubject{Kind: "project", ID: "project-a"}, ProjectID: "project-a", ComputerManaged: true,
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"},
		Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: pm.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}}, domain.WorkCreationFacts{})
	require.ErrorIs(t, err, domain.ErrComputerNotUsable, "the definition refuses a Managed Work itself")
}
