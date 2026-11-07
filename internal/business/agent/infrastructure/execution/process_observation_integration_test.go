//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// startTwice releases a Work's first execution and begins its second,
// launched as current-process; it answers the first and the second.
func startTwice(t *testing.T, db *xorm.Engine) (*AgentExecutionRepository, *domain.AgentExecution, *domain.AgentExecution) {
	t.Helper()
	ctx := context.Background()
	provider, _, _ := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	executions := NewAgentExecutionRepository(db, time.Now)
	launched := func(processID string) *domain.AgentExecution {
		record, err := register(ctx, p, request(), testMounts)
		require.NoError(t, err)
		run, err := executions.Get(ctx, record.ID)
		require.NoError(t, err)
		require.True(t, run.RecordSubmission())
		run.Environment = sandboxID
		_, err = run.RecordLaunch(processID)
		require.NoError(t, err)
		require.NoError(t, executions.Save(ctx, run))
		run, err = executions.Get(ctx, record.ID)
		require.NoError(t, err)
		return run
	}
	first := launched("old-process")
	first.RecordExit("old-process", domain.HarnessExit{})
	_, err := first.Retire(domain.WorkComputeReleaseRequested{WorkID: first.WorkID, StartAdmittedAt: first.StartAdmittedAt, Cause: domain.RetiredByExit}, time.Now())
	require.NoError(t, err)
	first.RecordDeskLeft(time.Now())
	_, err = first.ConfirmRelease(domain.EnvironmentObservation{}, time.Now())
	require.NoError(t, err)
	require.NoError(t, executions.Save(ctx, first))
	time.Sleep(2 * time.Millisecond) // the next start is admitted later
	second := launched("current-process")
	return executions, first, second
}

// A released earlier execution is not evidence that the process of a newly
// admitted start exited, and only an exit fact naming the current
// execution's process is recorded on it.
func TestProcessObservationDistinguishesRetiredEnvironmentFromCurrentStart(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	executions, first, second := startTwice(t, db)

	latest, err := executions.Latest(ctx, second.WorkID)
	require.NoError(t, err)
	require.Equal(t, second.ID, latest.ID, "the Work's current execution is its latest one")
	require.False(t, latest.Process.Exited, "the released earlier run says nothing of the current process")
	require.NotEqual(t, first.ID, latest.ID)

	current, changed := latest.RecordExit("old-process", domain.HarnessExit{})
	require.False(t, current, "the earlier process's late exit fact is not the current one's")
	require.False(t, changed)
	current, changed = latest.RecordExit("current-process", domain.HarnessExit{})
	require.True(t, current)
	require.True(t, changed)
	require.NoError(t, executions.Save(ctx, latest))
	exited, err := executions.Get(ctx, second.ID)
	require.NoError(t, err)
	require.True(t, exited.Process.Exited)
	current, changed = exited.RecordExit("current-process", domain.HarnessExit{})
	require.True(t, current, "a redelivered exit fact is acknowledged again")
	require.False(t, changed)
}

// A delayed request to release the Work's earlier run, carried out after the
// Work resumed, cannot retire the current execution.
func TestLateComputeRequestCannotDeleteRunningCurrentProcess(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	executions, first, second := startTwice(t, db)
	retired, err := second.Retire(domain.WorkComputeReleaseRequested{WorkID: second.WorkID, StartAdmittedAt: first.StartAdmittedAt, Cause: domain.RetiredUnreached}, time.Now())
	require.NoError(t, err)
	require.False(t, retired, "an old request cannot retire the current execution")
	live, err := executions.Get(ctx, second.ID)
	require.NoError(t, err)
	require.False(t, live.Retired())
	pending, err := unreleased(ctx, db)
	require.NoError(t, err)
	require.Empty(t, pending, "an old request must not leave a release outstanding for the current run")
}
