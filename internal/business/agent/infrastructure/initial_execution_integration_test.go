//go:build integration

package infrastructure_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

func TestInitialExecutionReceiptPreservesInputAndRestartAcrossReconnect(t *testing.T) {
	ctx := context.Background()
	f := newWorkCreationFixture(t)
	db := f.db
	t.Cleanup(func() { _ = db.Close() })
	id := f.committedCreation(t)
	store := infrastructure.NewInitialExecutionRequests(db, time.Now)
	reserved, err := store.Get(ctx, id)
	require.NoError(t, err)
	input := *f.request.InitialExecution
	input.WorkID, input.WorkspaceReference = id, f.request.WorkspaceReference
	require.Equal(t, input, reserved.PrepareWorkExecution)
	require.Equal(t, 1, reserved.Version)
	require.True(t, reserved.Submitted, "first admission and original input commit together")
	pending, err := store.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []string{id}, pending)
	submitted, err := store.ListSubmittedPreparations(ctx, "", 100)
	require.NoError(t, err)
	require.Len(t, submitted, 1)
	require.Equal(t, input.Input.Text, submitted[0].Input)
	reserved.Prepared = true
	reserved.Checkouts = []command.RepositoryCheckout{{RepoID: "123", CloneURL: "https://example.test/project.git", BaseBranch: "release", WorkBranch: "talgent/TAL-342", TargetPath: "code/project", AccessID: "original-grant"}}
	require.NoError(t, store.Save(ctx, reserved))
	require.Equal(t, 1, reserved.Version, "saved receipt must be reloaded for another write")
	require.ErrorIs(t, store.Save(ctx, reserved), domain.ErrConcurrentModification)
	reconnect := func() {
		t.Helper()
		require.NoError(t, db.Close())
		db, err = xorm.NewEngine("mysql", f.dsn)
		require.NoError(t, err)
		store = infrastructure.NewInitialExecutionRequests(db, time.Now)
		f.sessions = infrastructure.NewAgentWorkRepository(db, time.Now)
		f.agents = infrastructure.NewAgentDefinitionRepository(db, time.Now)
	}
	reconnect()
	retained, err := store.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, input, retained.PrepareWorkExecution)
	require.Equal(t, reserved.Checkouts, retained.Checkouts)
	require.True(t, retained.Prepared)
	prepare := command.NewPrepareWorkExecutionHandler(store, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db)), creationModels())
	conflict := input
	conflict.RequestID, conflict.Input.ID, conflict.Input.Text = uuid.NewString(), uuid.NewString(), "another task"
	_, err = prepare.Handle(ctx, conflict)
	require.ErrorIs(t, err, command.ErrInitialExecutionConflict)
	retained.Rejected, retained.StartFailed = true, true
	require.NoError(t, store.Save(ctx, retained))
	stale, err := store.Get(ctx, id)
	require.NoError(t, err)
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	reportExit(t, work, domain.HarnessExit{ExitCode: 1})
	reportReleased(t, work)
	require.NoError(t, f.sessions.Save(ctx, work))
	restart := command.NewRestartWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db)), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	_, err = restart.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()})
	require.NoError(t, err)
	next, err := store.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, input, next.PrepareWorkExecution)
	require.Equal(t, reserved.Checkouts, next.Checkouts, "Restart keeps frozen SCM grants and checkout selections")
	require.False(t, next.Rejected)
	require.False(t, next.StartFailed)
	stale.CleanupConfirmed = true
	require.ErrorIs(t, store.Save(ctx, stale), domain.ErrConcurrentModification, "late cleanup cannot erase the retained retry")
	pending, err = store.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []string{id}, pending)
	require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, id))
	pending, err = store.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []string{id}, pending, "the first start still owes its checkout after its input is handed off")
	if next, err = store.Get(ctx, id); err != nil {
		t.Fatal(err)
	}
	next.InputPrepared = true
	require.NoError(t, store.Save(ctx, next))
	pending, err = store.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, pending)
	reconnect()
	submitted, err = store.ListSubmittedPreparations(ctx, "", 1)
	require.NoError(t, err)
	require.Len(t, submitted, 1, "first admission remains discoverable after delivery and reconnect")
	require.True(t, submitted[0].InputBuffered)
	require.Equal(t, input.RequestID, submitted[0].RequestID)
	preparation, found, err := store.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, submitted[0], preparation)
	submitted, err = store.ListSubmittedPreparations(ctx, id, 1)
	require.NoError(t, err)
	require.Empty(t, submitted, "the cursor does not replay its last row")
}

func TestInitialExecutionCleanupPreservesOriginalInput(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := t.Context()
	id := f.committedCreation(t)
	store := infrastructure.NewInitialExecutionRequests(f.db, time.Now)
	original, err := store.Get(ctx, id)
	require.NoError(t, err)
	original.Rejected, original.StartFailed = true, true
	require.NoError(t, store.Save(ctx, original))
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	reportExit(t, work, domain.HarnessExit{ExitCode: 1})
	reportReleased(t, work)
	require.NoError(t, work.Close(uuid.NewString(), domain.ClosureFacts{}))
	require.NoError(t, f.sessions.Save(ctx, work))
	pending, err := store.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []string{id}, pending, "failure and closure do not acknowledge input cleanup")
	cleanup, err := store.Get(ctx, id)
	require.NoError(t, err)
	cleanup.CleanupConfirmed = true
	require.NoError(t, store.Save(ctx, cleanup))
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	reopened := infrastructure.NewInitialExecutionRequests(other, time.Now)
	pending, err = reopened.ListPendingInitialExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, pending)
	retained, err := reopened.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, original.PrepareWorkExecution, retained.PrepareWorkExecution)
	require.True(t, retained.CleanupConfirmed)
	replacement := retained
	replacement.RequestID, replacement.Input.ID, replacement.Input.Text = uuid.NewString(), uuid.NewString(), "replacement"
	require.ErrorIs(t, reopened.Save(ctx, replacement), domain.ErrConcurrentModification, "cleanup does not permit replacing the initial request")
	actual, err := reopened.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, retained, actual)
}

func TestConcurrentInitialCreationRetainsOneCompleteInput(t *testing.T) {
	f := newWorkDefinitionFixture(t, "owner")
	ctx := t.Context()
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, creationModels()))
	type result struct {
		workID string
		err    error
	}
	results := make(chan result, 4)
	inputs := make(map[string]command.PrepareWorkExecution)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := range 4 {
		creation := f.request
		input := *creation.InitialExecution
		input.RequestID, input.Input.ID, input.Input.Text = uuid.NewString(), uuid.NewString(), fmt.Sprintf("request-%d", i)
		inputs[input.RequestID] = input
		creation.RequestID, creation.InitialExecution = input.RequestID, &input
		group.Go(func() {
			<-start
			id, err := create.Handle(ctx, creation)
			results <- result{id, err}
		})
	}
	close(start)
	group.Wait()
	close(results)
	// One request establishes the Subject's Work; the others reuse it with
	// another initial task input, which its first start refuses.
	id := ""
	for r := range results {
		if r.err != nil {
			require.ErrorIs(t, r.err, command.ErrInitialExecutionConflict)
			continue
		}
		require.Empty(t, id, "one request establishes the Work")
		id = r.workID
	}
	require.NotEmpty(t, id)
	store := infrastructure.NewInitialExecutionRequests(f.db, time.Now)
	retained, err := store.Get(ctx, id)
	require.NoError(t, err)
	expected, found := inputs[retained.RequestID]
	require.True(t, found)
	expected.WorkID, expected.WorkspaceReference = id, f.request.WorkspaceReference
	require.Equal(t, expected, retained.PrepareWorkExecution, "concurrent callers cannot mix input identity, body and selections")
	rows, err := f.db.QueryString("SELECT id FROM agent_initial_execution_request WHERE deleted_at=0")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	admissions, err := f.db.QueryString("SELECT id, session_id FROM agent_start_request WHERE deleted_at=0")
	require.NoError(t, err)
	require.Equal(t, []map[string]string{{"id": retained.RequestID, "session_id": id}}, admissions)
}
