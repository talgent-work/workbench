//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/pkg/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"xorm.io/xorm"
)

// Recovery must register the same allocation after Work persistence. Empty
// collections are valid catalog facts; storage must not change their fingerprint.
func TestRecoveredCapabilitiesReuseOriginalAllocation(t *testing.T) {
	ctx := t.Context()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
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
			delivery := &allocationRegistration{provisioning: provisioning}
			pending := infrastructure.NewExecutionRequestsFactory(delivery)()
			work, err := domain.NewAgentWork(ctx, domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: definition.ID, Capabilities: snapshot, HomeBinding: definition.HomeBinding, WorkspaceReference: uuid.NewString()}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, pending)
			require.NoError(t, err)
			works := infrastructure.NewAgentWorkRepository(db, time.Now)
			require.NoError(t, works.RegisterWork(ctx, work, command.WorkRegistration{SubjectID: "owner", RequestID: uuid.NewString(), DefinitionVersion: definition.Version}))
			require.NoError(t, pending.Send(ctx))
			original, found, err := provisioning.live(ctx, work.ID)
			require.NoError(t, err)
			require.True(t, found)
			reopened, err := xorm.NewEngine("mysql", dsn)
			require.NoError(t, err)
			defer reopened.Close()
			recovered, err := infrastructure.NewAgentWorkRepository(reopened, time.Now).Get(ctx, work.ID)
			require.NoError(t, err)
			require.Equal(t, snapshot.Connectors, recovered.CapabilitySnapshot.Connectors)
			pending = infrastructure.NewExecutionRequestsFactory(delivery)()
			_, err = recovered.RecoverExecution(ctx, pending)
			require.NoError(t, err)
			require.NoError(t, pending.Send(ctx), "recovery must reuse the admitted allocation")
			retained, found, err := provisioning.live(ctx, work.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, original, retained, "recovery must not rewrite the allocation or its commands")
			changed := delivery.request
			changed.Capabilities.Prompt = "a different task configuration"
			require.ErrorIs(t, provisioning.Register(ctx, changed, testMounts), errEnvironmentConflict, "real configuration changes still conflict")
		})
	}
}

// Buffering, Work persistence, recovery and provisioning are production code.
// This bridge exercises durable registration without starting external compute.
type allocationRegistration struct {
	domain.ExecutionPort
	provisioning *Provisioning
	request      domain.StartExecutionRequest
}

func (p *allocationRegistration) StartExecution(ctx context.Context, request domain.StartExecutionRequest) error {
	p.request = request
	// Storage grants each read-only mount the Work names.
	mounts := testMounts
	for _, mount := range request.ReadOnlyMounts {
		if mounts.ReadOnly == nil {
			mounts.ReadOnly = map[string]domain.MountGrant{}
		}
		mounts.ReadOnly[mount.Name] = domain.MountGrant{ClaimName: "inputs", SubPath: "references/" + mount.Reference, ReadOnly: true}
	}
	return p.provisioning.Register(ctx, request, mounts)
}
