//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"xorm.io/xorm"
)

// A Work is located by its definition and Subject across a reconnect; a
// closed Work keeps its Subject, and storage refuses a second Work for it.
func TestWorkSubjectLocatesTheSameWorkForGood(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work",
		UsageScope:   domain.UsageScope{Kind: "employee", Reference: "owner"},
		Capabilities: domain.CapabilityConfiguration{Prompt: "Original", StartupAgent: "employees:member", RequiredPlugins: []string{"employees"}},
	})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, agent))
	agent, err = agents.Get(ctx, agent.ID)
	require.NoError(t, err)
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}
	capabilities, err := command.ResolveStartupCapabilities(ctx, commandtest.StartCatalog(&repositoryCatalog{}, creationConnections{}), "owner", domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, BasePrompt: agent.Capabilities.Prompt, RequiredPlugins: agent.Capabilities.RequiredPlugins, DefaultStartupAgent: agent.Capabilities.StartupAgent})
	require.NoError(t, err)
	subject := domain.WorkSubject{Kind: "case", ID: "Work-A"}
	creation, isNew, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: "owner", Admission: admission, Capabilities: capabilities, WorkModel: domain.WorkModelSelection{ConfigurationID: "deployment-default"}, Subject: subject, ProjectID: "project-a"}, subjectWork(t, sessions, agent.ID, subject))
	require.NoError(t, err)
	require.True(t, isNew)
	require.NoError(t, sessions.Save(ctx, creation))
	originalID, originalContext := creation.ID, creation.HarnessSessionReference
	require.NoError(t, agent.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "Changed", StartupAgent: "employees:architect", RequiredPlugins: []string{"employees"}}))
	require.NoError(t, agents.Save(ctx, agent))
	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	agents = infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions = infrastructure.NewAgentWorkRepository(db, time.Now)
	agent, err = agents.Get(ctx, agent.ID)
	require.NoError(t, err)
	retained, isNew, err := agent.CreateWork(domain.WorkCreationRequest{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, Subject: subject, ProjectID: "project-a"}, subjectWork(t, sessions, agent.ID, subject))
	require.NoError(t, err)
	require.False(t, isNew)
	require.Equal(t, originalID, retained.ID)
	require.Equal(t, originalContext, retained.HarnessSessionReference)
	require.Equal(t, subject, retained.Subject)
	require.Equal(t, "project-a", retained.ProjectID)
	require.Equal(t, "Original", retained.CapabilitySnapshot.Prompt)
	for _, other := range []domain.WorkSubject{{Kind: "case", ID: "work-a"}, {Kind: "case", ID: "Work-B"}, {Kind: "review", ID: "Work-A"}, {Kind: "project", ID: "project-a"}} {
		located, lookupErr := sessions.FindBySubject(ctx, agent.ID, other)
		require.NoError(t, lookupErr)
		require.Nil(t, located, "another kind or identity is another Subject")
	}
	other, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "other"}, Capabilities: agent.Capabilities})
	require.NoError(t, err)
	located, err := sessions.FindBySubject(ctx, other.ID, subject)
	require.NoError(t, err)
	require.Nil(t, located, "lookup must remain scoped to the definition")

	_, err = db.Exec("UPDATE agent_session SET state = 7 WHERE id = ?", originalID)
	require.NoError(t, err)
	closed, err := sessions.FindBySubject(ctx, agent.ID, subject)
	require.NoError(t, err)
	require.Equal(t, originalID, closed.ID, "a closed Work still holds its Subject")
	duplicate, isNew, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: "owner", Admission: admission, Capabilities: capabilities, WorkModel: domain.WorkModelSelection{ConfigurationID: "deployment-default"}, Subject: subject, ProjectID: "project-a"}, domain.WorkCreationFacts{})
	require.NoError(t, err)
	require.True(t, isNew)
	require.ErrorIs(t, sessions.Save(ctx, duplicate), domain.ErrConcurrentModification, "storage keeps one Work per definition and Subject")
}
