//go:build integration

package taskqueue

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/hibiken/asynq"
	"go.uber.org/fx/fxtest"
)

const testTaskType componenttaskqueue.TaskType = "taskqueue.test.ping"

var testTaskDefinition = componenttaskqueue.Definition{Type: testTaskType, Queue: DefaultQueueName}

// testTaskCheckInterval lets an idle worker pick up a task, and stop, without
// waiting out the provider's one-second poll.
const testTaskCheckInterval = 10 * time.Millisecond

// A keyed task the queue gave up on and archived keeps its ID but never runs
// again. A later enqueue of the same key must replace it; a key still waiting
// absorbs the enqueue as before. Treating the archived task as covering the
// enqueue would silence every later wake of that key.
func TestTaskqueue_ArchivedKeyIsReplacedWhilePendingKeyAbsorbs(t *testing.T) {
	addr, db := middlewaretest.RedisDB(t)
	cfg := Config{RedisAddr: addr, RedisDB: db, WorkerConcurrency: 1, QueueName: DefaultQueueName}
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
