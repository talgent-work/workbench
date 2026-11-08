//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"xorm.io/xorm"
)

// A creation answered through another connection keeps its represented
// human's personal capabilities.
func TestCreationRetainsPersonalOwner(t *testing.T) {
	f := newWorkDefinitionFixture(t, "owner")
	otherDB, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = otherDB.Close() })
	works := infrastructure.NewAgentWorkRepository(otherDB, time.Now)
	handler := command.NewCreateWorkHandler(time.Now, f.agents, works, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(otherDB)), deploymentModels(), commandtest.StartCatalog(&identityCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(otherDB, deploymentModels()))
	id, err := handler.Handle(t.Context(), f.request)
	require.NoError(t, err)
	work, err := f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "owner", work.HomeBinding.OwnerID)
	require.Len(t, work.CapabilitySnapshot.Connectors, 1)
	require.Equal(t, "talgent", work.CapabilitySnapshot.Connectors[0].Name)
	require.Equal(t, "work_identity", work.CapabilitySnapshot.Connectors[0].Authentication)
	require.True(t, work.CapabilitySnapshot.Connectors[0].Required)
	require.Len(t, work.CapabilitySnapshot.Plugins, 1)
	require.Equal(t, "owner-plugin", work.CapabilitySnapshot.Plugins[0].Release.Slug)
}

type identityCatalog struct{ creationCatalog }

func (*identityCatalog) ListPersonalPlugins(_ context.Context, user string) ([]domain.PersonalPlugin, error) {
	return []domain.PersonalPlugin{{Slug: user + "-plugin", Enabled: true}}, nil
}

func TestWorkAuthorizationSelectionSurvivesPersistenceAndBindingReuse(t *testing.T) {
	f := newWorkDefinitionFixture(t, "owner")
	agent, err := f.agents.Get(t.Context(), f.request.AgentID)
	require.NoError(t, err)
	selectedID, otherID := uuid.NewString(), uuid.NewString()
	input := domain.WorkCreationRequest{StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}},
		Admission:          domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true},
		WorkModel:          domain.WorkModelSelection{ConfigurationID: "fixture"},
		WorkspaceReference: "workspace", Subject: f.request.Subject, ProjectID: f.request.ProjectID,
		AuthorizationInput: domain.WorkAuthorizationInput{OwnerUserID: "owner", Available: []domain.PersonalAuthorization{
			{OwnerUserID: "owner", AuthorizationID: selectedID, Provider: "github", Service: "github"},
			{OwnerUserID: "owner", AuthorizationID: otherID, Provider: "other", Service: "other"},
		}},
	}
	work, created, err := agent.CreateWork(input, subjectWork(t, f.sessions, agent.ID, input.Subject))
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, f.sessions.Save(t.Context(), work))
	retained, err := f.sessions.Get(t.Context(), work.ID)
	require.NoError(t, err)
	want := domain.WorkAuthorizationSelection{OwnerUserID: "owner", Authorizations: []domain.WorkServiceAuthorization{{AuthorizationID: selectedID, Provider: "github", Service: "github"}, {AuthorizationID: otherID, Provider: "other", Service: "other"}}}
	require.Equal(t, want, retained.AuthorizationSelection)
	// A reused binding keeps the original selection even when available facts change.
	input.AuthorizationInput.Available = input.AuthorizationInput.Available[:1]
	input.AuthorizationInput.Available[0].AuthorizationID = uuid.NewString()
	reused, created, err := agent.CreateWork(input, subjectWork(t, f.sessions, agent.ID, input.Subject))
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, work.ID, reused.ID)
	require.Equal(t, want, reused.AuthorizationSelection)
}

func (*identityCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}
