//go:build integration

package taskqueue

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
)

// A change committed while an attempt runs, after it read the state, wakes
// its object during that attempt. The wake's task meets the running attempt
// on a second worker and hands its wake over, so the object is reconciled
// again right after the attempt instead of waiting for a periodic sweep, and
// the two attempts never run together.
func TestCoalescedWakeDuringAnAttemptReconcilesAgainAfterIt(t *testing.T) {
	addr, db := middlewaretest.RedisDB(t)
	cfg := Option{RedisAddr: addr, RedisDB: db, WorkerConcurrency: 2, QueueName: DefaultQueueName, RetryDelayMin: Duration(100 * time.Millisecond), RetryDelayMax: Duration(200 * time.Millisecond), taskCheckInterval: testTaskCheckInterval}
	logger := slog.New(slog.DiscardHandler)
	coalescing := NewCoalescing(cfg, NewEnqueuer(cfg, logger), logger)
	const object = "taskqueue.integration.object"
	wake := func(ctx context.Context) error {
		task, err := componenttaskqueue.New(testTaskDefinition, []byte("{}"))
		require.NoError(t, err)
		return coalescing.Wake(ctx, object, task, componenttaskqueue.WithMaxRetry(3), componenttaskqueue.WithTimeout(10*time.Second))
	}

	var attempts, running, overlapped atomic.Int32
	firstRead := make(chan struct{})
	release := make(chan struct{})
	reconciled := make(chan struct{})
	processor := componenttaskqueue.NewProcessor(testTaskType, func(ctx context.Context, _ componenttaskqueue.Task) error {
		return coalescing.Run(ctx, object, func(context.Context) error {
			if running.Add(1) > 1 {
				overlapped.Add(1)
			}
			defer running.Add(-1)
			switch attempts.Add(1) {
			case 1:
				close(firstRead)
				<-release
			case 2:
				close(reconciled)
			}
			return nil
		}, wake)
	})
	lane, err := NewLane(cfg.QueueName, cfg, logger, []componenttaskqueue.Processor{processor}, nil)
	require.NoError(t, err)
	require.NoError(t, lane.Start(t.Context()))
	defer func() { require.NoError(t, lane.Stop(context.Background())) }()

	require.NoError(t, wake(t.Context()))
	<-firstRead
	require.NoError(t, wake(t.Context()), "the change committed after the first attempt read the state")
	inspect := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer inspect.Close()
	require.Eventually(t, func() bool {
		return inspect.Exists(t.Context(), coalescingKeys(object).rerun).Val() == 1
	}, 10*time.Second, 10*time.Millisecond, "the wake's task meets the running attempt and hands its wake over")
	require.Equal(t, int32(1), attempts.Load(), "it does not run beside the holder")
	close(release)

	select {
	case <-reconciled:
	case <-time.After(10 * time.Second):
		t.Fatalf("the object is reconciled again after the attempt; attempts = %d", attempts.Load())
	}
	require.Zero(t, overlapped.Load(), "attempts for one object never run together")
}
