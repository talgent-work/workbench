//go:build integration

package infrastructure_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// A Work's first start is derived from its creation request and its own
// facts, across Restart and reconnect: admitted with the Work, its checkouts
// planned from the repository selection on the branch the caller named, its
// workspace prepared once the repository has an outcome, and its initial task
// input shown until it is handed off.
func TestWorkPreparationIsDerivedFromTheCreationRequest(t *testing.T) {
	ctx := context.Background()
	f := newWorkCreationFixture(t)
	db := f.db
	t.Cleanup(func() { _ = db.Close() })
	id := f.committedCreation(t)
	reader := infrastructure.NewWorkQueryRepository(db)
	initial := f.request.InitialExecution
	preparation, found, err := reader.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, preparation.Checkouts, 1)
	planned := preparation.Checkouts[0]
	require.NotEmpty(t, planned.TargetPath)
	require.NotEmpty(t, planned.AccessID)
	require.Equal(t, agentquery.WorkPreparation{WorkID: id, RequestID: id, SubjectID: "owner", ThinkingLevel: initial.ThinkingLevel,
		WorkBranch: "talgent/TAL-342", InputID: initial.Input.ID, Input: initial.Input.Text, Submitted: true,
		Checkouts: []agentquery.PreparedCheckout{{Provider: "github", RepoID: "123", RepoName: "example/project", CloneURL: "https://github.com/example/project.git",
			WebURL: "https://github.com/example/project", DefaultBranch: "main", BaseBranch: "release", WorkBranch: "talgent/TAL-342", TargetPath: planned.TargetPath, AccessID: planned.AccessID}},
	}, preparation, "first admission and the original input commit together")
	submitted, err := reader.ListSubmittedPreparations(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []agentquery.WorkPreparation{preparation}, submitted)

	// A repeat outside creation is answered from the Work: whatever
	// repository choices it names, it returns the original Work unchanged.
	prepare := command.NewPrepareWorkExecutionHandler(f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), creationModels())
	repeat := *initial
	repeat.WorkID, repeat.RequestID, repeat.Input.ID, repeat.Input.Text = id, uuid.NewString(), uuid.NewString(), "another task"
	confirmed, err := prepare.Handle(ctx, repeat)
	require.NoError(t, err)
	require.Equal(t, id, confirmed)
	repeat.WorkBranch, repeat.Selections = "talgent/other", []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "changed"}}
	reused, err := prepare.Handle(ctx, repeat)
	require.NoError(t, err)
	require.Equal(t, id, reused)
	unchanged, _, err := reader.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.Equal(t, preparation, unchanged, "a reuse cannot replace the original input or selection")

	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	reportExit(t, work, domain.HarnessExit{ExitCode: 1})
	reportReleased(t, work)
	require.NoError(t, f.sessions.Save(ctx, work))
	restart := command.NewRestartWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	_, err = restart.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()})
	require.NoError(t, err)
	restarted, _, err := reader.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.Equal(t, preparation, restarted, "Restart keeps the first start's request, input and checkout grants")

	recordWorkCheckouts(t, f.sessions, id, domain.RepositoryCheckout{RepositoryID: "123", CheckedOut: true})
	checkedOut, _, err := reader.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, checkedOut.InputPrepared, "every selected repository has an outcome")
	require.False(t, checkedOut.InputBuffered)
	require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, id))

	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	reader = infrastructure.NewWorkQueryRepository(db)
	submitted, err = reader.ListSubmittedPreparations(ctx, "", 1)
	require.NoError(t, err)
	require.Len(t, submitted, 1, "first admission remains discoverable after delivery and reconnect")
	require.True(t, submitted[0].InputBuffered)
	require.Empty(t, submitted[0].InputID, "a handed-off input is part of the conversation")
	require.Empty(t, submitted[0].Input)
	require.Equal(t, id, submitted[0].RequestID)
	require.Equal(t, preparation.Checkouts, submitted[0].Checkouts)
	handedOff, found, err := reader.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, submitted[0], handedOff)
	submitted, err = reader.ListSubmittedPreparations(ctx, id, 1)
	require.NoError(t, err)
	require.Empty(t, submitted, "the cursor does not replay its last row")
	_, found, err = reader.GetWorkPreparation(ctx, uuid.NewString())
	require.NoError(t, err)
	require.False(t, found, "an unknown Work has no first start")
}

// Failure and closure keep the Work's first start, its request and its
// checkout plan; closure ends the input the Work held, which no start delivers
// any more.
func TestClosedWorkKeepsItsFirstStart(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := t.Context()
	id := f.committedCreation(t)
	reader := infrastructure.NewWorkQueryRepository(f.db)
	original, found, err := reader.GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	reportExit(t, work, domain.HarnessExit{ExitCode: 1})
	reportReleased(t, work)
	require.NoError(t, work.Close(uuid.NewString(), domain.ClosureFacts{}))
	require.NoError(t, f.sessions.Save(ctx, work))
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	retained, found, err := infrastructure.NewWorkQueryRepository(other).GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, original.RequestID, retained.RequestID)
	require.Equal(t, original.WorkBranch, retained.WorkBranch)
	require.Equal(t, original.Checkouts, retained.Checkouts)
	require.Empty(t, retained.Input, "a closed Work holds no initial task input")
	closed, err := infrastructure.NewAgentWorkRepository(other, time.Now).Get(ctx, id)
	require.NoError(t, err)
	_, pending := closed.PendingInitialTaskInput()
	require.False(t, pending)
}

func TestConcurrentInitialCreationRetainsOneCompleteInput(t *testing.T) {
	f := newWorkDefinitionFixture(t, "owner")
	ctx := t.Context()
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, creationModels()))
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
	// One request establishes the Subject's Work; the others reuse it and are
	// answered with that Work.
	ids := map[string]bool{}
	for r := range results {
		require.NoError(t, r.err)
		ids[r.workID] = true
	}
	require.Len(t, ids, 1, "one request establishes the Work")
	var id string
	for id = range ids {
	}
	preparation, found, err := infrastructure.NewWorkQueryRepository(f.db).GetWorkPreparation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	var expected command.PrepareWorkExecution
	for _, input := range inputs {
		if input.Input.ID == preparation.InputID {
			expected = input
		}
	}
	require.Equal(t, expected.Input.ID, preparation.InputID, "concurrent callers cannot mix input identity and body")
	require.Equal(t, expected.Input.Text, preparation.Input)
}
