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
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestReadyHarnessWaitsForReportedCheckoutOutcomes(t *testing.T) {
	ctx := context.Background()
	_, sessions := startupStores(t)
	store := &initialRequestStore{}
	prepare := command.NewPrepareWorkExecutionHandler(store, sessions, sessionAccess{}, nil, agentquery.ModelConfigurations{})
	input := command.PrepareWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "OP-8", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "work on the repository"}}
	input.WorkspaceReference = sessions.session.WorkspaceReference
	store.request = command.InitialExecutionRequest{PrepareWorkExecution: input, Version: 1, Submitted: true}
	_, err := prepare.Handle(ctx, input)
	require.NoError(t, err)
	store.request.Checkouts = []command.RepositoryCheckout{{RepoID: "github:1210538796", RepoName: "example/talgent", CloneURL: "https://github.com/example/talgent.git", TargetPath: "/workspace/repos/talgent", AccessID: "grant-1"}}
	received := &commandtest.InputBuffer{}
	// Harness readiness can be observed before the checkout receipt commits;
	// it must not manufacture a failed checkout outcome.
	w := worldOver(t, sessions)
	w.ready(t)
	recover := command.NewRecoverInitialExecutionHandler(store, sessions, prepare, command.NewInputAcceptance(sessions, received))
	require.ErrorIs(t, recover.Handle(ctx, input.WorkID), command.ErrInitialExecutionPending)
	receipt, err := store.Get(ctx, input.WorkID)
	require.NoError(t, err)
	require.Empty(t, receipt.CheckoutResults)
	require.False(t, receipt.InputPrepared)
	require.True(t, receipt.InputBuffered)
	require.Equal(t, "work on the repository", received.Inputs[0].Text)
}

func TestReportedCheckoutOutcomesAreKeptOnceAndFollowThePlan(t *testing.T) {
	ctx := context.Background()
	plan := []command.RepositoryCheckout{
		{RepoID: "github:1", TargetPath: "/workspace/repos/one"},
		{RepoID: "github:2", TargetPath: "/workspace/repos/two"},
	}
	store := &initialRequestStore{request: command.InitialExecutionRequest{PrepareWorkExecution: command.PrepareWorkExecution{WorkID: uuid.NewString()}, Prepared: true, Checkouts: plan, Version: 1}}
	record := command.NewRecordRepositoryCheckoutsHandler(store)
	for _, inconsistent := range [][]command.RepositoryCheckoutResult{
		{{RepositoryID: "github:3", Prepared: true}},
		{{RepositoryID: "github:1", Prepared: true}, {RepositoryID: "github:1", Prepared: true}},
		{{RepositoryID: "github:1", Reason: "fatal: could not read Username"}},
		{{RepositoryID: "github:1", TargetPath: "/workspace/elsewhere", Prepared: true}},
	} {
		require.Error(t, record.Handle(ctx, command.RecordRepositoryCheckouts{WorkID: store.request.WorkID, Results: inconsistent}))
		require.False(t, store.request.InputPrepared)
	}
	require.NoError(t, record.Handle(ctx, command.RecordRepositoryCheckouts{WorkID: store.request.WorkID, Results: []command.RepositoryCheckoutResult{
		{RepositoryID: "github:2", Reason: command.CheckoutTimedOut},
	}}))
	require.True(t, store.request.InputPrepared)
	require.Equal(t, []command.RepositoryCheckoutResult{
		{RepositoryID: "github:1", TargetPath: "/workspace/repos/one", Reason: command.CheckoutIncomplete},
		{RepositoryID: "github:2", TargetPath: "/workspace/repos/two", Reason: command.CheckoutTimedOut},
	}, store.request.CheckoutResults, "a repository without an outcome did not finish checking out")
	require.NoError(t, record.Handle(ctx, command.RecordRepositoryCheckouts{WorkID: store.request.WorkID, Results: []command.RepositoryCheckoutResult{
		{RepositoryID: "github:1", Prepared: true}, {RepositoryID: "github:2", Prepared: true},
	}}))
	require.Equal(t, command.CheckoutIncomplete, store.request.CheckoutResults[0].Reason, "Resume and Restart never replace a recorded checkout outcome")
}

func TestCurrentCheckoutPlanChecksOutOnlyUntilTheFirstStartsCheckoutEnds(t *testing.T) {
	planned := []command.RepositoryCheckout{{RepoID: "github:1", TargetPath: "/workspace/repos/a"}}
	plan, pending := command.InitialExecutionRequest{Prepared: true, Checkouts: planned}.CurrentCheckoutPlan()
	require.False(t, pending)
	require.Equal(t, planned, plan)

	plan, pending = command.InitialExecutionRequest{Prepared: true}.CurrentCheckoutPlan()
	require.False(t, pending)
	require.NotNil(t, plan, "a first start without repositories still ends its checkout step")
	require.Empty(t, plan)

	plan, pending = command.InitialExecutionRequest{Checkouts: planned}.CurrentCheckoutPlan()
	require.True(t, pending, "the plan is fixed before checkout, so preparation waits")
	require.Nil(t, plan)

	for _, ended := range []command.InitialExecutionRequest{{Prepared: true, InputPrepared: true, Checkouts: planned}, {Prepared: true, Rejected: true, Checkouts: planned}} {
		plan, pending = ended.CurrentCheckoutPlan()
		require.False(t, pending)
		require.Nil(t, plan, "a later start keeps existing checkouts")
	}
}

func TestRepositoriesUnfinishedAtThePreparationBoundAreTimedOut(t *testing.T) {
	ctx := context.Background()
	plan := []command.RepositoryCheckout{{RepoID: "github:1", TargetPath: "/workspace/repos/one"}, {RepoID: "github:2", TargetPath: "/workspace/repos/two"}}
	for unfinished, want := range map[string]string{command.CheckoutTimedOut: command.CheckoutTimedOut, command.CheckoutIncomplete: command.CheckoutIncomplete, "": command.CheckoutIncomplete, "anything else": command.CheckoutIncomplete} {
		store := &initialRequestStore{request: command.InitialExecutionRequest{PrepareWorkExecution: command.PrepareWorkExecution{WorkID: uuid.NewString()}, Prepared: true, Checkouts: plan, Version: 1}}
		require.NoError(t, command.NewRecordRepositoryCheckoutsHandler(store).Handle(ctx, command.RecordRepositoryCheckouts{WorkID: store.request.WorkID, Unfinished: unfinished, Results: []command.RepositoryCheckoutResult{
			{RepositoryID: "github:1", TargetPath: "/workspace/repos/one", Prepared: true},
		}}))
		require.Equal(t, []command.RepositoryCheckoutResult{
			{RepositoryID: "github:1", TargetPath: "/workspace/repos/one", Prepared: true},
			{RepositoryID: "github:2", TargetPath: "/workspace/repos/two", Reason: want},
		}, store.request.CheckoutResults, "unfinished %q", unfinished)
	}
}

func TestRestartCompletesAFirstStartThatFailedBeforeItsCheckout(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	store := &initialRequestStore{}
	prepare := command.NewPrepareWorkExecutionHandler(store, sessions, sessionAccess{}, nil, agentquery.ModelConfigurations{})
	input := command.PrepareWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "OP-8", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "work on the repository"}}
	input.WorkspaceReference = sessions.session.WorkspaceReference
	store.request = command.InitialExecutionRequest{PrepareWorkExecution: input, Version: 1, Submitted: true}
	_, err := prepare.Handle(ctx, input)
	require.NoError(t, err)
	store.request.Checkouts = []command.RepositoryCheckout{{RepoID: "github:1", TargetPath: "/workspace/repos/talgent"}}
	received := &commandtest.InputBuffer{}
	recover := command.NewRecoverInitialExecutionHandler(store, sessions, prepare, command.NewInputAcceptance(sessions, received))

	// The sandbox never became ready: the first start fails before its checkout.
	w := worldOver(t, sessions)
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) { require.True(t, execution.RecordSubmission()) })
	reconcileAt(t, w, w.execution(t).StartupDeadlineAt())
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.ErrorIs(t, recover.Handle(ctx, input.WorkID), command.ErrInitialExecutionPending, "the obligation waits for the next start")
	require.False(t, store.request.InputPrepared, "the checkout of the first start is still outstanding")

	// Nothing was launched: the execution is abandoned, retired and released.
	reconcileAt(t, w, w.execution(t).StartupDeadlineAt().Add(time.Minute))
	w.release(t)
	require.False(t, sessions.session.HoldsProcess())
	sessions.starts = map[string]agentquery.AdmittedStart{}
	restart := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	_, err = restart.Handle(ctx, command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: input.WorkID})
	require.NoError(t, err)
	// The Restart's preparation checks out the outstanding plan before its Harness.
	require.NoError(t, command.NewRecordRepositoryCheckoutsHandler(store).Handle(ctx, command.RecordRepositoryCheckouts{WorkID: input.WorkID, Results: []command.RepositoryCheckoutResult{{RepositoryID: "github:1", Prepared: true}}}))
	w.ready(t)

	require.NoError(t, recover.Handle(ctx, input.WorkID))
	require.NoError(t, recover.Handle(ctx, input.WorkID))
	receipt, err := store.Get(ctx, input.WorkID)
	require.NoError(t, err)
	require.True(t, receipt.InputBuffered)
	require.Equal(t, []command.RepositoryCheckoutResult{{RepositoryID: "github:1", TargetPath: "/workspace/repos/talgent", Prepared: true}}, receipt.CheckoutResults)
	require.Equal(t, "work on the repository", received.Inputs[0].Text)
}
