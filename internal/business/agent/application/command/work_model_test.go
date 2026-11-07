package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

const managedComputer = "01950000-0000-7000-8000-00000000ac01"

var deploymentModel = domain.WorkModelSelection{ConfigurationID: "deployment-default"}

func modelHandler(t *testing.T, store *sessionStore) (*domain.AgentDefinition, *command.CreateWorkHandler) {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}})
	require.NoError(t, err)
	models := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: deploymentModel, ThinkingLevels: []string{"low"}, DefaultThinkingLevel: "low"})
	computers := capacityCheck{others: []command.UsableComputer{{ID: managedComputer}}}
	return agent, command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, models, commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), computers, store.preparation(models))
}

func modelCreation(agentID, computerID string, model domain.WorkModelSelection, thinking string) command.CreateWork {
	return command.CreateWork{AgentID: agentID, SubjectID: "caller", WorkspaceReference: "workspace", ComputerID: computerID, WorkModel: model,
		InitialExecution: &command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/work", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Do the work"}, ThinkingLevel: thinking}}
}

// A Managed Computer's Work uses the machine's own Claude login, which has no
// Model Template: every Harness thinking level is offered and none is the
// default.
func TestAWorkOnAManagedComputerUsesTheMachineLogin(t *testing.T) {
	for _, requested := range []domain.WorkModelSelection{{}, domain.MachineLoginModel()} {
		store := &sessionStore{}
		agent, handler := modelHandler(t, store)
		_, err := handler.Handle(t.Context(), modelCreation(agent.ID, managedComputer, requested, ""))
		require.NoError(t, err)
		require.Equal(t, domain.MachineLoginModel(), store.session.WorkModel)
		require.Equal(t, managedComputer, store.session.ComputerID)
		require.Empty(t, store.session.ThinkingLevel, "the machine's Claude login has no default thinking level")
	}
	store := &sessionStore{}
	agent, handler := modelHandler(t, store)
	_, err := handler.Handle(t.Context(), modelCreation(agent.ID, managedComputer, domain.MachineLoginModel(), "xhigh"))
	require.NoError(t, err)
	require.Equal(t, "xhigh", store.session.ThinkingLevel)
	_, err = handler.Handle(t.Context(), modelCreation(agent.ID, managedComputer, domain.MachineLoginModel(), "extreme"))
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}

// A Work on a Managed Computer has no Session Workspace or read-only mount,
// whatever its creating caller named: its Agent there reads what the mount
// would hold through the platform Connector. A Cloud Work keeps both.
func TestOnlyACloudWorkKeepsTheSessionWorkspaceAndMountsItsCallerNamed(t *testing.T) {
	knowledge := []domain.ReadOnlyMount{{Name: "knowledge", Reference: "project"}}
	for computer, kept := range map[string]string{managedComputer: "", "": "workspace"} {
		store := &sessionStore{}
		agent, handler := modelHandler(t, store)
		creation := modelCreation(agent.ID, computer, deploymentModel, "")
		creation.Subject, creation.ProjectID, creation.ReadOnlyMounts = domain.WorkSubject{Kind: "case", ID: "case"}, "project", knowledge
		_, err := handler.Handle(t.Context(), creation)
		require.NoError(t, err)
		require.Equal(t, kept, store.session.WorkspaceReference)
		if kept == "" {
			require.Empty(t, store.session.ReadOnlyMounts, "a Managed Work has no knowledge mount")
		} else {
			require.Equal(t, knowledge, store.session.ReadOnlyMounts, "a Cloud Work keeps its knowledge mount")
		}
	}
}

// A project-scoped Agent's Works run on a Cloud: a Managed Computer chosen for
// one is refused before any run is seated there, and nothing is created.
func TestThePMCoordinatorRunsOnlyOnACloud(t *testing.T) {
	coordinator, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, AgentName: "Coordinator", Purpose: "project_coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: "project"}, HomeBinding: domain.HomeBinding{OwnerID: "project/project/project_coordination", Namespace: "project_coordination"}})
	require.NoError(t, err)
	models := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: deploymentModel, ThinkingLevels: []string{"low"}, DefaultThinkingLevel: "low"})
	for computer, refused := range map[string]bool{managedComputer: true, "": false} {
		store, seats := &sessionStore{}, &seatCount{capacityCheck: capacityCheck{others: []command.UsableComputer{{ID: managedComputer}}}}
		handler := command.NewCreateWorkHandler(time.Now, agentStore{coordinator}, store, store, models, commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), seats, store.preparation(models))
		_, err := handler.Handle(t.Context(), modelCreation(coordinator.ID, computer, deploymentModel, ""))
		if !refused {
			require.NoError(t, err)
			continue
		}
		require.ErrorIs(t, err, domain.ErrCloudOnlyAgent)
		require.ErrorIs(t, err, domain.ErrComputerNotUsable)
		require.Nil(t, store.session)
		require.Zero(t, seats.admitted, "no run is seated on the Managed Computer")
	}
}

// seatCount counts the runs its Computers were asked to seat.
type seatCount struct {
	capacityCheck
	admitted int
}

func (c *seatCount) AdmitRun(ctx context.Context, run command.RunAdmission) (command.ComputerAnswer, error) {
	c.admitted++
	return c.capacityCheck.AdmitRun(ctx, run)
}

func TestTheMachineLoginIsRejectedOnACloud(t *testing.T) {
	store := &sessionStore{}
	agent, handler := modelHandler(t, store)
	_, err := handler.Handle(t.Context(), modelCreation(agent.ID, "", domain.MachineLoginModel(), ""))
	require.ErrorIs(t, err, domain.ErrMachineLoginNotManaged)
	require.Nil(t, store.session)
}

// A Managed Computer also offers the provided Model Configurations, whose
// credential the Computer adds to each model request.
func TestAModelConfigurationIsAdmittedOnAManagedComputer(t *testing.T) {
	store := &sessionStore{}
	agent, handler := modelHandler(t, store)
	_, err := handler.Handle(t.Context(), modelCreation(agent.ID, managedComputer, deploymentModel, ""))
	require.NoError(t, err)
	require.Equal(t, deploymentModel, store.session.WorkModel)
	require.Equal(t, "low", store.session.ThinkingLevel, "the Model Template's default thinking level applies")
}

func TestAComputerItsStarterCannotUseIsRejected(t *testing.T) {
	store := &sessionStore{}
	agent, handler := modelHandler(t, store)
	_, err := handler.Handle(t.Context(), modelCreation(agent.ID, "01950000-0000-7000-8000-00000000ac02", domain.MachineLoginModel(), ""))
	require.ErrorIs(t, err, domain.ErrComputerNotUsable)
	require.Nil(t, store.session)
}

// No Model Configuration is consulted for a Work on the machine's Claude
// login, so withdrawing every configuration does not refuse its Resume or
// Restart.
func TestAMachineLoginWorkResumesAndRestartsWithNoModelConfiguration(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "restart"}[restart], func(t *testing.T) {
			agents, sessions := startupStores(t)
			session, err := domain.NewAgentWork(domain.WorkInput{WorkModel: domain.MachineLoginModel(), ComputerManaged: true, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), AgentID: agents.agent.ID, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, WorkspaceReference: "workspace"},
				domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agents.agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: fixtureComputer, DeskID: "desk-test"}})
			require.NoError(t, err)
			report(t, session, readyReport)
			require.NoError(t, session.Stop("stop", false))
			if restart {
				require.NoError(t, session.RecordFailure("failed during stop"))
			}
			report(t, session, exitedReport)
			report(t, session, releasedReport)
			settled(session)
			sessions.session = session
			none := agentquery.NewModelConfigurations()
			request := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: session.ID}
			if restart {
				_, err = command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, none, commandtest.UnchangedPlugins{}).Handle(context.Background(), request)
			} else {
				err = command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, none, commandtest.UnchangedPlugins{}).Handle(context.Background(), request)
			}
			require.NoError(t, err)
			require.Equal(t, domain.WorkStarting, sessions.session.State)
			require.Len(t, sessions.admittedStarts(), 1, "the start is admitted for its execution")
			require.Equal(t, domain.MachineLoginModel(), sessions.session.WorkModel)
		})
	}
}

// githubCatalog offers the GitHub Connector beside the required one, enabled
// by the starter.
type githubCatalog struct{ emptyPluginCatalog }

func (githubCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	github := connectorcatalogtest.GitHub()
	return command.ConnectorSelection{Catalog: []domain.CatalogConnector{connectorcatalogtest.Required(), github}, EnabledConnectors: []string{github.ID}}, nil
}

// A Provider-authorized Connector is unavailable on a Managed Computer: the
// Work is created without it rather than refused.
func TestAManagedWorkIsCreatedWithoutProviderAuthorizedConnectors(t *testing.T) {
	store := &sessionStore{}
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}})
	require.NoError(t, err)
	models := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: deploymentModel, ThinkingLevels: []string{"low"}, DefaultThinkingLevel: "low"})
	computers := capacityCheck{others: []command.UsableComputer{{ID: managedComputer}}}
	handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, models, commandtest.StartCatalog(githubCatalog{}, authorizationcatalogtest.Empty{}), computers, store.preparation(models))
	creation := modelCreation(agent.ID, managedComputer, domain.MachineLoginModel(), "")
	creation.CapabilityDifferences.Connectors = []domain.ConnectorChoice{{Name: "github", Include: true}}
	_, err = handler.Handle(t.Context(), creation)
	require.NoError(t, err)
	require.Len(t, store.session.CapabilitySnapshot.Connectors, 1)
	require.Equal(t, "talgent", store.session.CapabilitySnapshot.Connectors[0].Name)
}
