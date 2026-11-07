package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func TestWorkUsesOneHumanForPluginsHomeAndAuthorization(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"},
		Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "owner"},
		HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"},
	})
	require.NoError(t, err)
	store := &sessionStore{}
	handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store,
		query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}}),
		commandtest.StartCatalog(personalCatalog{}, ownerAuthorizationCatalog{}), capacityCheck{}, store.preparation(query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}})))
	input := command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: uuid.NewString(), AgentID: agent.ID,
		SubjectID: "owner", StartingUserID: "starter", WorkspaceReference: "workspace"}
	_, err = handler.Handle(t.Context(), input)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.Nil(t, store.session)
	input.RequestID = uuid.NewString()
	input.StartingUserID = "owner"
	id, err := handler.Handle(t.Context(), input)
	require.NoError(t, err)
	work, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "owner", work.HomeBinding.OwnerID)
	require.Equal(t, domain.WorkAuthorizationSelection{OwnerUserID: "owner", Authorizations: []domain.WorkServiceAuthorization{{AuthorizationID: "01940000-0000-7000-8000-000000000382", Provider: "github", Service: "github"}}}, work.AuthorizationSelection)
	require.NoError(t, store.AuthorizeWork(t.Context(), "owner", id))
	require.Len(t, work.CapabilitySnapshot.Plugins, 1)
	require.Equal(t, "owner-plugin", work.CapabilitySnapshot.Plugins[0].Release.Slug)
	require.Equal(t, "owner", work.CapabilitySnapshot.Plugins[0].Configuration["language"])

	input.StartingUserID = "owner"
	reused, err := handler.Handle(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, id, reused)
	work, err = store.Get(t.Context(), reused)
	require.NoError(t, err)
	require.Equal(t, "owner-plugin", work.CapabilitySnapshot.Plugins[0].Release.Slug)
}

type personalCatalog struct{ emptyPluginCatalog }

func (personalCatalog) ListPersonalPlugins(_ context.Context, user string) ([]domain.PersonalPlugin, error) {
	return []domain.PersonalPlugin{{Slug: user + "-plugin", Enabled: true, Configuration: pluginconfig.Configuration{"language": user}}}, nil
}

func (personalCatalog) ResolvePlugin(_ context.Context, _, slug string) (domain.PluginRelease, error) {
	return domain.PluginRelease{Slug: slug, Source: "fixture", Version: "1", RetrievalRef: "fixture@1", NativeEntry: []byte(`{"name":"fixture"}`),
		Parameters: pluginconfig.ParameterDeclarations{"language": {Type: "string", Title: "Language", Description: "Output language", Default: "default"}}}, nil
}

func TestAutomatedWorkUsesRequiredPluginsWithoutPersonalDefaults(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"triggering-human"},
		Purpose: "project_coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: "project"},
		HomeBinding:  domain.HomeBinding{OwnerID: "project-home", Namespace: "coordination"},
		Capabilities: domain.CapabilityConfiguration{RequiredPlugins: []string{"runtime"}},
	})
	require.NoError(t, err)
	store := &sessionStore{}
	handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store,
		query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}}),
		commandtest.StartCatalog(personalCatalog{}, ownerAuthorizationCatalog{}), capacityCheck{}, store.preparation(query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}})))
	id, err := handler.Handle(t.Context(), command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: uuid.NewString(),
		AgentID: agent.ID, SubjectID: "triggering-human", WorkspaceReference: "workspace"})
	require.NoError(t, err)
	work, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	require.Len(t, work.CapabilitySnapshot.Plugins, 1)
	require.Equal(t, "runtime", work.CapabilitySnapshot.Plugins[0].Release.Slug)
	require.Equal(t, "default", work.CapabilitySnapshot.Plugins[0].Configuration["language"])
	require.Equal(t, domain.WorkAuthorizationSelection{}, work.AuthorizationSelection)
}

// Marketplace supplies different records for each authenticated personal owner.
type ownerAuthorizationCatalog struct{}

func (ownerAuthorizationCatalog) ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error) {
	return nil, nil
}

func (ownerAuthorizationCatalog) ListPersonalAuthorizations(_ context.Context, owner string) ([]domain.PersonalAuthorization, error) {
	id := "01940000-0000-7000-8000-000000000383"
	if owner == "owner" {
		id = "01940000-0000-7000-8000-000000000382"
	}
	return []domain.PersonalAuthorization{{OwnerUserID: owner, AuthorizationID: id, Provider: "github", Service: "github"}}, nil
}

func (personalCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}
