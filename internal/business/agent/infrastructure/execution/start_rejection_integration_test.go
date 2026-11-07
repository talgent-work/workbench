//go:build integration

package execution

import (
	"os"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"xorm.io/xorm"
)

func TestRejectedPreparationConvergesWithoutCreatingComputeAndRestartsRetainedInput(t *testing.T) {
	ctx := t.Context()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	initial := infrastructure.NewInitialExecutionRequests(db, time.Now)
	provider, _, external := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, initial, nil)
	dispatcher := NewDispatcherControl(nil, "http://unused.invalid", "unused")
	observations, err := NewProcessObservations(p, dispatcher)
	require.NoError(t, err)
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	resources, err := NewEnvironments(p, NewRemovals(db, time.Now), observations)
	require.NoError(t, err)
	// Domain delivery is buffered; direct provisioning below exercises the
	// registration boundary without requiring native UID ownership in this test.
	environment, err := NewEnvironment(resources, dispatcher, directoryMounts{root: t.TempDir()}, cloudComputers{})
	require.NoError(t, err)
	factory := environment
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Fixture Agent", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}})
	require.NoError(t, err)
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	require.NoError(t, definitions.Save(ctx, definition))
	definition, err = definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	work, err := domain.NewAgentWork(ctx, domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: definition.ID, WorkspaceReference: "workspace", HomeBinding: definition.HomeBinding, Capabilities: domain.CapabilitySnapshot{Connectors: request().Capabilities.Connectors, Prompt: "retained task capabilities"}}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, commandtest.Hold(factory))
	require.NoError(t, err)
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	input := command.PrepareWorkExecution{WorkID: work.ID, SubjectID: "owner", WorkspaceReference: work.WorkspaceReference, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-368", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "original input"}}
	require.NoError(t, works.RegisterWork(ctx, work, command.WorkRegistration{SubjectID: "owner", DefinitionVersion: definition.Version, RequestID: input.RequestID, InitialExecution: &input}))
	start := request()
	start.WorkID, start.AgentID, start.ContextMode = work.ID, work.AgentID, domain.ContextFresh
	start.Capabilities, start.HomeBinding, start.HarnessSessionReference = work.CapabilitySnapshot.Clone(), work.HomeBinding, work.HarnessSessionReference
	require.NoError(t, p.Register(ctx, start, testMounts), "waiting preparation must not suppress durable registration")
	require.NoError(t, submitPending(ctx, p))
	first, found, err := p.live(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, first.Submitted)
	retained, err := initial.Get(ctx, work.ID)
	require.NoError(t, err)
	retained.Rejected, retained.StartFailed = true, true
	retained.FailureReason = "controlled prerequisite rejection"
	require.NoError(t, initial.Save(ctx, retained))
	retained, err = initial.Get(ctx, work.ID)
	require.NoError(t, err)
	require.NoError(t, submitPending(ctx, p))
	observation, err := observations.ObserveProcess(ctx, work.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProcessStartRejected, observation.State)
	work, err = works.Get(ctx, work.ID)
	require.NoError(t, err)
	outbound := commandtest.Hold(factory)
	require.NoError(t, work.ReconcileProcess(ctx, observation.ProcessObservation, outbound))
	require.NoError(t, works.Save(ctx, work))
	require.NoError(t, outbound.Send(ctx))
	require.NoError(t, resources.Delete(ctx, work.ID), "retirement is idempotent without a provider deletion")
	work, err = works.Get(ctx, work.ID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkFailed, work.State)
	require.True(t, work.HarnessProcess.StartRejected)
	require.False(t, work.HarnessProcess.Exited)
	require.False(t, work.HarnessProcess.EnvironmentLost)
	require.False(t, work.HoldsProcess())
	observation, err = observations.ObserveProcess(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, observation.ComputeReclaimed)
	require.NoError(t, work.ConfirmComputeReclaimed())
	require.NoError(t, works.Save(ctx, work))
	work, err = works.Get(ctx, work.ID)
	require.NoError(t, err)
	require.NoError(t, work.Restart(ctx, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, commandtest.Hold(factory)))
	restartID := uuid.NewString()
	require.NoError(t, works.CommitStart(ctx, work, query.AdmittedStart{WorkID: work.ID, Kind: query.StartKindRestart}, restartID))
	after, err := initial.Get(ctx, work.ID)
	require.NoError(t, err)
	require.False(t, after.Rejected)
	require.False(t, after.CleanupConfirmed)
	require.Equal(t, retained.Input, after.Input)
	require.Equal(t, retained.RequestID, after.RequestID)
	require.ErrorIs(t, initial.Save(ctx, retained), domain.ErrConcurrentModification, "old recovery cannot restore its rejection")
	start.ContextMode = domain.ContextRecover
	require.NoError(t, p.Register(ctx, start, testMounts))
	observation, err = observations.ObserveProcess(ctx, work.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProcessPending, observation.State, "the old rejected allocation cannot fail the new start")
	require.NoError(t, submitPending(ctx, p))
	external.mu.Lock()
	require.Zero(t, external.creates)
	require.Zero(t, external.deletes)
	external.mu.Unlock()
	after.Prepared = true // the same prerequisite subsequently succeeds
	require.NoError(t, initial.Save(ctx, after))
	require.ErrorIs(t, p.reconcile(ctx, first), errEnvironmentConflict, "a stale worker cannot submit the retired rejected allocation")
	external.mu.Lock()
	external.loseCreateResponse = true
	external.mu.Unlock()
	require.Error(t, submitPending(ctx, p))
	uncertain, found, err := p.live(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, uncertain.Submitted)
	require.Empty(t, uncertain.SandboxID)
	after, err = initial.Get(ctx, work.ID)
	require.NoError(t, err)
	after.Prepared, after.Rejected = false, true
	require.NoError(t, initial.Save(ctx, after))
	require.NoError(t, submitPending(ctx, p), "late prerequisite rejection cannot prove an uncertain submission never created compute")
	current, found, err := p.live(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, first.ID, current.ID)
	require.True(t, current.Submitted)
	require.False(t, current.StartRejected)
	require.NotEmpty(t, current.SandboxID)
	require.NotEqual(t, "null", string(current.CredentialPlanJSON))
	external.mu.Lock()
	require.Equal(t, 1, external.creates)
	external.mu.Unlock()
}
