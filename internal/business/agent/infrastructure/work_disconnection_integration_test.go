//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// A Work's Disconnection survives reload and shows on its view, and the
// Works running on a Computer are found for its Presence changes until their
// run ends.
func TestWorkDisconnectionPersistsAndRunningWorksAreFoundByComputer(t *testing.T) {
	ctx := context.Background()
	f := newWorkCreationFixture(t)
	handler := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db),
		creationModels(), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	id, err := handler.Handle(ctx, f.request.Creation)
	require.NoError(t, err)
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.NoError(t, work.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, work.ConfirmStarted(ctx, time.Now(), nil))
	lost := time.Now().Add(time.Second).UTC().Truncate(time.Millisecond)
	changed, err := work.RecordDisconnection(lost)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, f.sessions.Save(ctx, work))

	work, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, lost, work.DisconnectedSince)
	view, err := infrastructure.NewWorkQueryRepository(f.db).GetWorkView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, lost, view.DisconnectedSince)

	works := infrastructure.NewExecutionQueryRepository(f.db)
	running, err := works.ListRunningWorkIDs(ctx, work.ComputerID, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{id}, running)
	other, err := works.ListRunningWorkIDs(ctx, "01a19bc3-075a-7c57-b42b-7fdf73b611ff", "", 10)
	require.NoError(t, err)
	require.Empty(t, other)
	after, err := works.ListRunningWorkIDs(ctx, work.ComputerID, id, 10)
	require.NoError(t, err)
	require.Empty(t, after, "pages continue after the last identity")

	require.True(t, work.EndDisconnection())
	require.NoError(t, f.sessions.Save(ctx, work))
	work, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, work.DisconnectedSince.IsZero())

	// A removal finds the Work until its environment is lost, Failed or not.
	held, err := works.ListHeldWorkIDs(ctx, work.ComputerID, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{id}, held)
	require.NoError(t, work.RecordFailure("model unavailable"))
	require.NoError(t, f.sessions.Save(ctx, work))
	held, err = works.ListHeldWorkIDs(ctx, work.ComputerID, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{id}, held, "a Failed Work's process may still run")
	work, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	changed, err = work.ApplyComputerRemoval(ctx)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, f.sessions.Save(ctx, work))
	held, err = works.ListHeldWorkIDs(ctx, work.ComputerID, "", 10)
	require.NoError(t, err)
	require.Empty(t, held, "a lost environment holds nothing")
}

// A Resume refused while the Work's Managed Computer was away marks the
// Stopped Work across reload, and its Computer's reconnection and the
// recovery sweep find it until the mark is spent.
func TestAReachResumeMarkPersistsAndIsFoundByComputer(t *testing.T) {
	ctx := context.Background()
	f := newWorkCreationFixture(t)
	handler := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db),
		creationModels(), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	id, err := handler.Handle(ctx, f.request.Creation)
	require.NoError(t, err)
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.NoError(t, work.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, work.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, work.Stop(ctx, "stop-1", executionPort{}))
	require.NoError(t, work.RecordExit(ctx, domain.HarnessExit{}, executionPort{}))
	require.NoError(t, work.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, work.State)
	require.NoError(t, f.sessions.Save(ctx, work))

	works := infrastructure.NewExecutionQueryRepository(f.db)
	marked, err := works.ListReachResumeWorkIDs(ctx, work.ComputerID, "", 10)
	require.NoError(t, err)
	require.Empty(t, marked, "an unmarked Work is not resumed on reconnection")
	recovering, err := works.ListWorkIDs(ctx, "", 100)
	require.NoError(t, err)
	require.NotContains(t, recovering, id, "a Stopped Work owes its recovery nothing")

	work, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, work.RefuseResume(domain.ErrComputerUnreachable))
	require.NoError(t, f.sessions.Save(ctx, work))
	work, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, work.ReachResumeDue)
	marked, err = works.ListReachResumeWorkIDs(ctx, work.ComputerID, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{id}, marked)
	recovering, err = works.ListWorkIDs(ctx, "", 100)
	require.NoError(t, err)
	require.Contains(t, recovering, id, "the recovery retries the Resume a lost reconnection owed")
	other, err := works.ListReachResumeWorkIDs(ctx, "01a19bc3-075a-7c57-b42b-7fdf73b611ff", "", 10)
	require.NoError(t, err)
	require.Empty(t, other)

	_, spent := work.ResumeOnReconnection(false)
	require.True(t, spent)
	require.NoError(t, f.sessions.Save(ctx, work))
	marked, err = works.ListReachResumeWorkIDs(ctx, work.ComputerID, "", 10)
	require.NoError(t, err)
	require.Empty(t, marked)
}
