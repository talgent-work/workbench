//go:build integration

package taskqueue

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/hibiken/asynq"
	"go.uber.org/fx/fxtest"
)

const testTaskType componenttaskqueue.TaskType = "taskqueue.test.ping"

var testTaskDefinition = componenttaskqueue.Definition{Type: testTaskType, Queue: DefaultQueueName}

func startRedis(t *testing.T) (string, func()) { t.Helper(); return middlewaretest.Redis(t), func() {} }

// capturedLogs records log lines for assertions.
type capturedLogs struct {
	mu    sync.Mutex
	lines []string
}

func (c *capturedLogs) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, string(p))
	return len(p), nil
}

func (c *capturedLogs) contains(parts ...string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range c.lines {
		found := true
		for _, part := range parts {
			found = found && strings.Contains(line, part)
		}
		if found {
			return true
		}
	}
	return false
}

// A periodic task the provider refuses to enqueue is logged, never dropped
// silently, so a refused fire never stops a periodic task without a trace.
// The uniqueness lock of the first, unprocessed fire refuses the next one.
func TestTaskqueue_PeriodicScheduler_LogsRefusedEnqueue(t *testing.T) {
	addr, cleanup := startRedis(t)
	defer cleanup()
	cfg := Config{RedisAddr: addr, WorkerConcurrency: 1, QueueName: DefaultQueueName}
	logs := &capturedLogs{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	lc := fxtest.NewLifecycle(t)

	schedule, err := componenttaskqueue.IntervalSchedule(time.Second)
	if err != nil {
		t.Fatalf("interval schedule: %v", err)
	}
	task, err := componenttaskqueue.New(testTaskDefinition, []byte(`{}`))
	if err != nil {
		t.Fatalf("new task: %v", err)
	}
	periodic, err := componenttaskqueue.NewPeriodicTask("taskqueue.integration.refused", schedule, task, componenttaskqueue.WithUnique(time.Minute))
	if err != nil {
		t.Fatalf("new periodic task: %v", err)
	}
	if _, err := NewPeriodicTaskScheduler(lc, cfg, logger, PeriodicTaskIn{PeriodicTasks: []componenttaskqueue.PeriodicTask{periodic}}); err != nil {
		t.Fatalf("new periodic task scheduler: %v", err)
	}
	lc.RequireStart()
	defer lc.RequireStop()

	deadline := time.Now().Add(10 * time.Second)
	for !logs.contains("periodic task enqueue failed", string(testTaskType), DefaultQueueName) {
		if time.Now().After(deadline) {
			t.Fatalf("refused periodic enqueue was not logged; logs: %v", logs.lines)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A keyed task the queue gave up on and archived keeps its ID but never runs
// again. A later enqueue of the same key must replace it; a key still waiting
// absorbs the enqueue as before. Treating the archived task as covering the
// enqueue would silence every later wake of that key.
func TestTaskqueue_ArchivedKeyIsReplacedWhilePendingKeyAbsorbs(t *testing.T) {
	addr, cleanup := startRedis(t)
	defer cleanup()
	cfg := Config{RedisAddr: addr, WorkerConcurrency: 1, QueueName: DefaultQueueName}
	lc := fxtest.NewLifecycle(t)
	enqueuer := NewEnqueuer(lc, cfg, slog.Default())
	lc.RequireStart()
	defer lc.RequireStop()
	inspector := asynq.NewInspector(redisConn(cfg))
	defer inspector.Close()
	keyed := func(payload string) componenttaskqueue.Task {
		task, err := componenttaskqueue.New(testTaskDefinition, []byte(payload), componenttaskqueue.WithID("taskqueue.integration.mailbox"))
		if err != nil {
			t.Fatalf("new task: %v", err)
		}
		return task
	}
	pendingPayloads := func() []string {
		tasks, err := inspector.ListPendingTasks(DefaultQueueName)
		if err != nil {
			t.Fatalf("list pending: %v", err)
		}
		payloads := make([]string, 0, len(tasks))
		for _, task := range tasks {
			payloads = append(payloads, string(task.Payload))
		}
		return payloads
	}

	if err := enqueuer.Enqueue(context.Background(), keyed(`{"wake":1}`)); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := enqueuer.Enqueue(context.Background(), keyed(`{"wake":2}`)); err != nil {
		t.Fatalf("enqueue while pending: %v", err)
	}
	if got := pendingPayloads(); len(got) != 1 || got[0] != `{"wake":1}` {
		t.Fatalf("a pending key absorbs the enqueue: pending = %v", got)
	}

	if err := inspector.ArchiveTask(DefaultQueueName, "taskqueue.integration.mailbox"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := enqueuer.Enqueue(context.Background(), keyed(`{"wake":3}`)); err != nil {
		t.Fatalf("enqueue after archive: %v", err)
	}
	if got := pendingPayloads(); len(got) != 1 || got[0] != `{"wake":3}` {
		t.Fatalf("an archived key is replaced by the new enqueue: pending = %v", got)
	}
	queue, err := inspector.GetQueueInfo(DefaultQueueName)
	if err != nil {
		t.Fatalf("queue info: %v", err)
	}
	if queue.Archived != 0 {
		t.Fatalf("the replaced archived task remains: archived = %d", queue.Archived)
	}
}
