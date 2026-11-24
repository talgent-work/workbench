//go:build integration

package taskqueue

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// lockedBuffer is a log sink the worker's goroutines write to.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A task that keeps failing stops at its retry ceiling: it runs once plus its
// retries, and the worker logs its end as an error naming the task and its
// last cause.
func TestATaskThatKeepsFailingEndsAtItsCeilingWithAnErrorLog(t *testing.T) {
	addr, db := middlewaretest.RedisDB(t)
	cfg := Option{RedisAddr: addr, RedisDB: db, WorkerConcurrency: 1, QueueName: DefaultQueueName, RetryDelayMin: Duration(50 * time.Millisecond), RetryDelayMax: Duration(100 * time.Millisecond), taskCheckInterval: testTaskCheckInterval}
	lc := fxtest.NewLifecycle(t)
	var attempts atomic.Int32
	processor := componenttaskqueue.NewProcessor(testTaskType, func(context.Context, componenttaskqueue.Task) error {
		attempts.Add(1)
		return errors.New("downstream refused")
	})
	logs := &lockedBuffer{}
	enqueuer := NewEnqueuer(cfg, slog.Default())
	lane, err := NewLane(cfg.QueueName, cfg, slog.New(slog.NewJSONHandler(logs, nil)), []componenttaskqueue.Processor{processor}, nil)
	if err == nil {
		lc.Append(fx.StartStopHook(lane.Start, lane.Stop))
	}
	require.NoError(t, err)
	lc.RequireStart()
	defer lc.RequireStop()

	task, err := componenttaskqueue.New(testTaskDefinition, []byte("{}"), componenttaskqueue.WithID("exhausted-1"))
	require.NoError(t, err)
	require.NoError(t, enqueuer.Enqueue(t.Context(), task, componenttaskqueue.WithMaxRetry(2)))
	require.Eventually(t, func() bool { return bytes.Contains([]byte(logs.String()), []byte("task retries exhausted")) }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, int32(3), attempts.Load(), "one attempt and two retries, no more")
	require.Contains(t, logs.String(), `"level":"ERROR"`)
	require.Contains(t, logs.String(), `"task_id":"exhausted-1"`)
	require.Contains(t, logs.String(), `"error":"downstream refused"`)
}
