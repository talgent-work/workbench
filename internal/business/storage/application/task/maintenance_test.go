package task

import (
	"context"
	"testing"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// The Mapped View is repaired every hour, at half past, UTC, on the
// maintenance lane.
func TestTheMappedViewIsRepairedEveryHour(t *testing.T) {
	periodic, err := NewRepairMappedView()
	require.NoError(t, err)
	require.NoError(t, periodic.Validate())
	require.Equal(t, "30 * * * *", periodic.Schedule().Spec())
	require.Equal(t, "UTC", periodic.Schedule().Location())
	require.Equal(t, RepairMappedViewType, periodic.Task().Type())
	require.Equal(t, MaintenanceQueue, periodic.Task().Queue())
}

type recordedEnqueue struct {
	task    taskqueue.Task
	options taskqueue.EnqueueOptions
}

func (r *recordedEnqueue) Enqueue(_ context.Context, task taskqueue.Task, options ...taskqueue.EnqueueOption) error {
	r.task, r.options = task, taskqueue.NewEnqueueOptions(options...)
	return nil
}

// A removal is retried at most RemovalRetries times and, however long a
// publication holds the Directory, never past its deadline.
func TestAContentRemovalIsBoundedByACeilingAndADeadline(t *testing.T) {
	owner, err := domain.NewDirectoryOwner([]domain.OwnerFact{{Condition: domain.WorkCondition, Value: "work-1"}})
	require.NoError(t, err)
	queue := &recordedEnqueue{}
	before := time.Now()
	require.NoError(t, RequestContentRemoval(context.Background(), queue, RemoveReclaimedContent{Directory: domain.DirectoryID{Owner: owner, Name: "home"}}, "key"))
	retries, set := queue.options.MaxRetry()
	require.True(t, set)
	require.Equal(t, RemovalRetries, retries)
	require.WithinDuration(t, before.Add(RemovalDeadline), queue.options.Deadline(), time.Second)
}
