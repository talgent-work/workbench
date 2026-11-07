//go:build integration

package infrastructure_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// waitingComputer admits every run waiting in its Computer's line.
type waitingComputer struct{ command.ComputerPort }

func (waitingComputer) AdmitRun(context.Context, command.RunAdmission) (command.ComputerAnswer, error) {
	return command.ComputerAnswer{Kind: command.ComputerWaiting}, nil
}

// A start admitted waiting shows its Desk waiting while its execution is not
// begun or Queued, and no longer once the execution is seated.
func TestWorkViewShowsDeskWaitingUntilTheExecutionIsSeated(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db))
	id, err := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, auth, creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), waitingComputer{computertest.Checked(startupAuthority{})}, creationPreparation(f.db, creationModels())).Handle(ctx, f.request)
	require.NoError(t, err)
	views := infrastructure.NewWorkQueryRepository(f.db)
	deskWaiting := func() bool {
		t.Helper()
		view, err := views.GetWorkView(ctx, id)
		require.NoError(t, err)
		return view.DeskWaiting
	}
	require.True(t, deskWaiting(), "a waiting start whose execution is not begun waits for its Desk")
	run := newRuns(t, f.db, f.sessions)
	queued := run.latest(ctx, id)
	require.True(t, queued.Queued())
	require.True(t, deskWaiting(), "a Queued execution waits for its Desk")
	changed, err := queued.Seat(time.Now())
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, run.executions.Save(ctx, queued))
	seated, err := run.executions.Latest(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionStarting, seated.State)
	require.False(t, deskWaiting(), "a seated execution no longer waits for its Desk")
}

// A withdrawn waiting start no longer waits for its release, but its Queued
// execution stays listed for recovery until it retires, so a lost retirement
// request is passed again.
func TestRecoveryListsAWithdrawnStartUntilItsExecutionRetires(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db))
	id, err := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, auth, creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), waitingComputer{computertest.Checked(startupAuthority{})}, creationPreparation(f.db, creationModels())).Handle(ctx, f.request)
	require.NoError(t, err)
	run := newRuns(t, f.db, f.sessions)
	require.True(t, run.latest(ctx, id).Queued())
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.NoError(t, work.Stop("stop", true))
	require.True(t, work.HarnessProcess.ComputeReclaimed)
	// The retirement request recorded here is lost: only the Work is saved.
	require.NoError(t, f.sessions.Save(ctx, work))
	listed := func() bool {
		t.Helper()
		ids, err := infrastructure.NewExecutionQueryRepository(f.db).ListWorkIDs(ctx, "", 100)
		require.NoError(t, err)
		return slices.Contains(ids, id)
	}
	require.True(t, listed(), "an unretired execution keeps its Work in recovery")

	require.Equal(t, domain.WorkClosed, work.State, "nothing else keeps the withdrawn first start in recovery")
	execution, err := run.executions.Latest(ctx, id)
	require.NoError(t, err)
	require.True(t, execution.Queued())
	requested, ok := domain.Event(nil), false
	for _, intent := range work.Intents() {
		if _, ok = intent.(domain.WorkComputeReleaseRequested); ok {
			requested = intent
			break
		}
	}
	require.True(t, ok, "the withdrawn start still asks its execution to retire")
	retired, err := execution.Retire(requested.(domain.WorkComputeReleaseRequested), time.Now())
	require.NoError(t, err)
	require.True(t, retired)
	require.NoError(t, run.executions.Save(ctx, execution))
	require.False(t, listed(), "a retired execution of a stopped-waiting Work is not listed")
}
