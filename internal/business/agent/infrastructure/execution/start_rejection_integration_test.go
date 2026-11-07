//go:build integration

package execution

import (
	"net/http"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
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
	connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", command.NewRecordRepositoryCheckoutsHandler(initial))
	require.NoError(t, err)
	steps := newExecutionSteps(t, p, connections, unread{}, nil, time.Now)
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	coordination := command.NewExecutionCoordination(time.Now, works, steps.executions, steps.desks, steps.harness)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Fixture Agent", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}})
	require.NoError(t, err)
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	require.NoError(t, definitions.Save(ctx, definition))
	definition, err = definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: fixtureCloudID, DeskID: "desk-test"}, ModelConfigurationProvided: true}
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: definition.ID, WorkspaceReference: "workspace", HomeBinding: definition.HomeBinding, Capabilities: domain.CapabilitySnapshot{Connectors: request().Capabilities.Connectors, Prompt: "retained task capabilities"}}, admission)
	require.NoError(t, err)
	input := command.PrepareWorkExecution{WorkID: work.ID, SubjectID: "owner", WorkspaceReference: work.WorkspaceReference, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-368", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "original input"}}
	require.NoError(t, works.RegisterWork(ctx, work, command.WorkRegistration{SubjectID: "owner", DefinitionVersion: definition.Version, Request: query.StartRequest{ID: input.RequestID, Kind: query.StartKindStart, WorkID: work.ID}, InitialExecution: &input}))
	require.NoError(t, coordination.BeginExecution(ctx, work.ID), "waiting preparation must not suppress the execution's beginning")
	reconcile := func(id string) error {
		_, err := steps.handler.Reconcile(ctx, id, false)
		return err
	}
	first, found, err := p.live(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, reconcile(first.ID))
	first, _, err = p.live(ctx, work.ID)
	require.NoError(t, err)
	require.False(t, first.Submitted, "the first start's preparation holds its creation back")
	retained, err := initial.Get(ctx, work.ID)
	require.NoError(t, err)
	retained.Rejected, retained.StartFailed = true, true
	retained.FailureReason = "controlled prerequisite rejection"
	require.NoError(t, initial.Save(ctx, retained))
	retained, err = initial.Get(ctx, work.ID)
	require.NoError(t, err)
	require.NoError(t, reconcile(first.ID))
	rejected := current(t, steps, first.ID)
	require.True(t, rejected.Outcome.Rejected)
	require.True(t, reported(rejected, domain.ExecutionStartRejected{WorkID: work.ID, StartAdmittedAt: rejected.StartAdmittedAt}))
	require.NoError(t, coordination.Coordinate(ctx, work.ID))
	require.NoError(t, reconcile(first.ID), "the rejected execution releases without a provider deletion")
	require.NoError(t, coordination.Coordinate(ctx, work.ID))
	work, err = works.Get(ctx, work.ID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkFailed, work.State)
	require.True(t, work.HarnessProcess.StartRejected)
	require.False(t, work.HarnessProcess.Exited)
	require.False(t, work.HarnessProcess.EnvironmentLost)
	require.False(t, work.HoldsProcess())
	require.True(t, work.HarnessProcess.ComputeReclaimed, "the release is confirmed to the Work")
	admission.AdmittedAt = time.Now()
	require.NoError(t, work.Restart(admission))
	restartID := uuid.NewString()
	require.NoError(t, works.CommitStart(ctx, work, query.StartRequest{ID: restartID, Kind: query.StartKindRestart, WorkID: work.ID}))
	after, err := initial.Get(ctx, work.ID)
	require.NoError(t, err)
	require.False(t, after.Rejected)
	require.False(t, after.CleanupConfirmed)
	require.Equal(t, retained.Input, after.Input)
	require.Equal(t, retained.RequestID, after.RequestID)
	require.ErrorIs(t, initial.Save(ctx, retained), domain.ErrConcurrentModification, "old recovery cannot restore its rejection")
	require.NoError(t, coordination.BeginExecution(ctx, work.ID))
	restarted, err := steps.executions.Latest(ctx, work.ID)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, restarted.ID)
	require.Equal(t, domain.ContextRecover, restarted.Launch.ContextMode)
	require.NoError(t, reconcile(restarted.ID))
	require.False(t, current(t, steps, restarted.ID).Outcome.Rejected, "the old rejected execution cannot fail the new start")
	external.mu.Lock()
	require.Zero(t, external.creates)
	require.Zero(t, external.deletes)
	external.mu.Unlock()
	after.Prepared = true // the same prerequisite subsequently succeeds
	require.NoError(t, initial.Save(ctx, after))
	require.NoError(t, reconcile(first.ID))
	external.mu.Lock()
	require.Zero(t, external.creates, "a stale worker cannot submit the released rejected execution")
	external.loseCreateResponse = true
	external.mu.Unlock()
	require.Error(t, reconcile(restarted.ID))
	uncertain, found, err := p.live(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, uncertain.Submitted)
	require.Empty(t, uncertain.SandboxID)
	after, err = initial.Get(ctx, work.ID)
	require.NoError(t, err)
	after.Prepared, after.Rejected = false, true
	require.NoError(t, initial.Save(ctx, after))
	require.NoError(t, reconcile(restarted.ID), "late prerequisite rejection cannot prove an uncertain submission never created compute")
	live, found, err := p.live(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, first.ID, live.ID)
	require.True(t, live.Submitted)
	require.False(t, live.StartRejected)
	require.NotEmpty(t, live.SandboxID)
	require.NotEqual(t, "null", string(live.CredentialPlanJSON))
	external.mu.Lock()
	require.Equal(t, 1, external.creates)
	external.mu.Unlock()
}
