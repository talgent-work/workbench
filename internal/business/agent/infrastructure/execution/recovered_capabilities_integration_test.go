//go:build integration

package execution

import (
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"xorm.io/xorm"
)

// The Work's recovery passes its admitted start on again after Work
// persistence, which begins no second execution and rewrites nothing. Empty
// collections are valid catalog facts; storage must not change their
// fingerprint.
func TestRecoveredCapabilitiesReuseOriginalAllocation(t *testing.T) {
	ctx := t.Context()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	executions := NewAgentExecutionRepository(db, time.Now)
	empty, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}}, nil)
	require.NoError(t, err)
	components, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"review"}}, []domain.PluginRelease{{Slug: "review", Source: "owned", Version: "1", RetrievalRef: "archive:review-1", NativeEntry: []byte(`{"name":"review","version":"1"}`), Skills: []domain.SkillComponent{}, Agents: []domain.AgentComponent{}, MCPServers: []domain.MCPComponent{}}})
	require.NoError(t, err)
	for name, snapshot := range map[string]domain.CapabilitySnapshot{"nil plugins": {}, "empty plugins": empty, "empty components": components} {
		t.Run(name, func(t *testing.T) {
			snapshot.Connectors = []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}
			definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
			definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}})
			require.NoError(t, err)
			require.NoError(t, definitions.Save(ctx, definition))
			definition, err = definitions.Get(ctx, definition.ID)
			require.NoError(t, err)
			work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: definition.ID, Capabilities: snapshot, HomeBinding: definition.HomeBinding, WorkspaceReference: uuid.NewString()}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: fixtureCloudID, Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
			require.NoError(t, err)
			works := infrastructure.NewAgentWorkRepository(db, time.Now)
			work.DefinitionVersion = definition.Version
			require.NoError(t, works.Save(ctx, work))
			require.NoError(t, command.NewExecutionCoordination(time.Now, works, executions, &cloudDesks{}, commandtest.NewHarness()).BeginExecution(ctx, work.ID))
			begun, found, err := provisioning.live(ctx, work.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.NoError(t, provisioning.specify(ctx, begun, testMounts))
			original, _, err := provisioning.live(ctx, work.ID)
			require.NoError(t, err)

			reopened, err := xorm.NewEngine("mysql", dsn)
			require.NoError(t, err)
			defer reopened.Close()
			recoveredWorks := infrastructure.NewAgentWorkRepository(reopened, time.Now)
			recovered, err := recoveredWorks.Get(ctx, work.ID)
			require.NoError(t, err)
			require.Equal(t, snapshot.Connectors, recovered.CapabilitySnapshot.Connectors)
			_, err = recovered.RecoverExecution()
			require.NoError(t, err)
			require.NoError(t, command.NewExecutionCoordination(time.Now, recoveredWorks, NewAgentExecutionRepository(reopened, time.Now), &cloudDesks{}, commandtest.NewHarness()).BeginExecution(ctx, work.ID), "recovery must reuse the admitted execution")
			retained, found, err := provisioning.live(ctx, work.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, original, retained, "recovery must not rewrite the execution or its commands")

			changed := recovered.LaunchSpecification()
			changed.Capabilities.Prompt = "a different task configuration"
			again, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: domain.WorkStartAdmitted{WorkID: work.ID, StartAdmittedAt: time.Now(), DeskID: "desk-test"}, Launch: changed, Realization: domain.RealizationCloud})
			require.NoError(t, err)
			require.ErrorIs(t, executions.Save(ctx, again), domain.ErrExecutionAdmission, "a second unretired execution of the Work is refused")
		})
	}
}
