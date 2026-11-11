package taskprocessor_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	applicationtask "github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/business/storage/transport/taskprocessor"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
)

type failingRemoval struct{ err error }

func (f failingRemoval) RemoveReclaimedContent(context.Context, applicationtask.RemoveReclaimedContent) error {
	return f.err
}

type queued struct{ task taskqueue.Task }

func (q *queued) Enqueue(_ context.Context, task taskqueue.Task, _ ...taskqueue.EnqueueOption) error {
	q.task = task
	return nil
}

func removalTask(t *testing.T) taskqueue.Task {
	t.Helper()
	owner, err := domain.NewDirectoryOwner([]domain.OwnerFact{{Condition: domain.WorkCondition, Value: "work-1"}})
	require.NoError(t, err)
	queue := &queued{}
	require.NoError(t, applicationtask.RequestContentRemoval(context.Background(), queue, applicationtask.RemoveReclaimedContent{Directory: domain.DirectoryID{Owner: owner, Name: "home"}}, "key"))
	return queue.task
}

func attempt(retried int) context.Context {
	return taskqueue.ContextWithExecutionInfo(context.Background(), taskqueue.NewExecutionInfo(
		taskqueue.WithExecutionTaskID("removal"), taskqueue.WithExecutionRetryCount(retried), taskqueue.WithExecutionMaxRetry(applicationtask.RemovalRetries)))
}

// A removal that keeps failing, or whose Directory a publication still holds,
// ends at its last attempt as a reclamation failure logged as an error that
// names the Work's Directory and the cause.
func TestAContentRemovalEndsAtItsCeilingAsAnObservableFailure(t *testing.T) {
	for name, cause := range map[string]error{
		"failing":   errors.New("disk unavailable"),
		"published": application.ErrPublicationInProgress,
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			processor := taskprocessor.NewRemoveReclaimedContentProcessor(failingRemoval{err: cause}, slog.New(slog.NewJSONHandler(&logs, nil)))

			err := processor.Process(attempt(applicationtask.RemovalRetries-1), removalTask(t))
			require.ErrorIs(t, err, cause)
			require.Equal(t, errors.Is(cause, application.ErrPublicationInProgress), errors.Is(err, pkgtaskqueue.ErrRetryLater))
			require.Empty(t, logs.String(), "a failure before the ceiling is retried")

			err = processor.Process(attempt(applicationtask.RemovalRetries), removalTask(t))
			require.ErrorIs(t, err, cause)
			require.NotErrorIs(t, err, pkgtaskqueue.ErrRetryLater, "the last attempt ends the removal")
			require.Contains(t, logs.String(), `"level":"ERROR","msg":"reclaimed content removal failed"`)
			require.Contains(t, logs.String(), `"work_id":"work-1"`)
			require.Contains(t, logs.String(), `"directory":"home"`)
		})
	}
}
