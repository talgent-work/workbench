//go:build integration

package taskqueue

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
	"go.uber.org/fx/fxtest"
)

// A keyed task reads as pending until it completes, and a task that cannot
// run yet is retried without counting as a failed attempt: with one retry
// allowed it still runs a third time. The provider checks exhaustion before
// failure, so a task needs at least one retry for this to apply.
func TestKeyedTaskPendingAndRetryLater(t *testing.T) {
	addr, db := middlewaretest.RedisDB(t)
	cfg := Config{RedisAddr: addr, RedisDB: db, WorkerConcurrency: 1, QueueName: DefaultQueueName, RetryDelayMin: Duration(100 * time.Millisecond), RetryDelayMax: Duration(200 * time.Millisecond), taskCheckInterval: testTaskCheckInterval}
	lc := fxtest.NewLifecycle(t)
	var attempts atomic.Int32
	release := make(chan struct{})
	processor := componenttaskqueue.NewProcessor(testTaskType, func(context.Context, componenttaskqueue.Task) error {
		if attempts.Add(1) < 3 {
			return fmt.Errorf("%w: downstream busy", ErrRetryLater)
		}
		<-release
		return nil
	})
	enqueuer := NewEnqueuer(lc, cfg, slog.Default())
	tasks := NewKeyedTasks(lc, cfg)
	_, err := NewWorker(lc, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)), ProcessorIn{Processors: []componenttaskqueue.Processor{processor}})
	require.NoError(t, err)
	lc.RequireStart()
	defer lc.RequireStop()

	pending, err := tasks.Pending(DefaultQueueName, "content-1")
	require.NoError(t, err)
	require.False(t, pending, "no task holds the key yet")
	task, err := componenttaskqueue.New(testTaskDefinition, []byte("{}"), componenttaskqueue.WithID("content-1"))
	require.NoError(t, err)
	require.NoError(t, enqueuer.Enqueue(t.Context(), task, componenttaskqueue.WithMaxRetry(1), componenttaskqueue.WithDeadline(time.Now().Add(time.Minute))))
	require.Eventually(t, func() bool { return attempts.Load() >= 3 }, 15*time.Second, 10*time.Millisecond, "retry-later attempts are not failures, so they run again")
	pending, err = tasks.Pending(DefaultQueueName, "content-1")
	require.NoError(t, err)
	require.True(t, pending, "a running task holds its key")
	close(release)
	require.Eventually(t, func() bool {
		pending, err := tasks.Pending(DefaultQueueName, "content-1")
		return err == nil && !pending
	}, 10*time.Second, 10*time.Millisecond, "a completed task holds nothing")
}
