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
)

// A task that cannot run yet is retried without counting as a failed
// attempt: with one retry allowed it still runs a third time. The provider
// checks exhaustion before failure, so a task needs at least one retry for
// this to apply.
func TestRetryLaterIsNotAFailedAttempt(t *testing.T) {
	addr, db := middlewaretest.RedisDB(t)
	cfg := Option{RedisAddr: addr, RedisDB: db, WorkerConcurrency: 1, QueueName: DefaultQueueName, RetryDelayMin: Duration(100 * time.Millisecond), RetryDelayMax: Duration(200 * time.Millisecond), taskCheckInterval: testTaskCheckInterval}
	var attempts atomic.Int32
	done := make(chan struct{})
	processor := componenttaskqueue.NewProcessor(testTaskType, func(context.Context, componenttaskqueue.Task) error {
		if attempts.Add(1) < 3 {
			return fmt.Errorf("%w: downstream busy", ErrRetryLater)
		}
		close(done)
		return nil
	})
	lane, err := NewLane(cfg.QueueName, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)), []componenttaskqueue.Processor{processor}, nil)
	require.NoError(t, err)
	require.NoError(t, lane.Start(t.Context()))
	defer func() { require.NoError(t, lane.Stop(context.Background())) }()

	task, err := componenttaskqueue.New(testTaskDefinition, []byte("{}"), componenttaskqueue.WithID("content-1"))
	require.NoError(t, err)
	require.NoError(t, NewEnqueuer(cfg, slog.Default()).Enqueue(t.Context(), task, componenttaskqueue.WithMaxRetry(1), componenttaskqueue.WithDeadline(time.Now().Add(time.Minute))))
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("retry-later attempts are not failures, so the task runs a third time; ran %d", attempts.Load())
	}
}
