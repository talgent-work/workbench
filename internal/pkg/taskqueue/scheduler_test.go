package taskqueue

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/hibiken/asynq"
	"go.uber.org/fx/fxtest"
)

// Periodic scheduler wiring must use the component PeriodicTaskScheduler
// contract so duplicate semantic names fail before startup creates ambiguity.
func TestNewPeriodicTaskScheduler_RejectsDuplicatePeriodicTaskNames(t *testing.T) {
	periodic := newRuntimePeriodicTask(t, "taskqueue.periodic.duplicate")

	_, err := NewPeriodicTaskScheduler(
		fxtest.NewLifecycle(t),
		Config{RedisAddr: "127.0.0.1:6379", QueueName: "maintenance"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		PeriodicTaskIn{PeriodicTasks: []componenttaskqueue.PeriodicTask{periodic, periodic}},
	)

	if !errors.Is(err, componenttaskqueue.ErrDuplicatePeriodicTask) {
		t.Fatalf("error = %v, want ErrDuplicatePeriodicTask", err)
	}
}

// Periodic scheduler wiring should accept component PeriodicTask values without
// exposing asynq scheduler types or provider schedule strings to callers.
func TestNewPeriodicTaskScheduler_AcceptsComponentPeriodicTasks(t *testing.T) {
	periodic := newRuntimePeriodicTask(t, "taskqueue.periodic.accepted")

	scheduler, err := NewPeriodicTaskScheduler(
		fxtest.NewLifecycle(t),
		Config{RedisAddr: "127.0.0.1:6379", QueueName: "maintenance"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		PeriodicTaskIn{PeriodicTasks: []componenttaskqueue.PeriodicTask{periodic}},
	)

	if err != nil {
		t.Fatalf("new periodic task scheduler: %v", err)
	}
	if scheduler == nil {
		t.Fatal("scheduler should be provided")
	}
}

// A periodic enqueue the provider refuses, such as one whose key an earlier,
// unprocessed fire still holds, is logged with its task type, queue and cause,
// at INFO since every other replica meets it on each tick; any other refusal is
// an ERROR:
// asynq otherwise drops the error and the periodic task stops without a trace.
// The scheduler's options are checked rather than a live fire, because asynq
// fires at most once a second and calling the handler is asynq's own contract.
func TestPeriodicSchedulerLogsRefusedEnqueue(t *testing.T) {
	for name, tc := range map[string]struct {
		opts  []asynq.Option
		queue string
	}{
		"named queue":   {opts: []asynq.Option{asynq.Unique(time.Minute), asynq.Queue("maintenance")}, queue: "maintenance"},
		"default queue": {opts: []asynq.Option{asynq.Unique(time.Minute)}, queue: "default"},
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := schedulerOpts(slog.New(slog.NewTextHandler(&logs, nil))).EnqueueErrorHandler
			if handler == nil {
				t.Fatal("the scheduler has no handler for refused enqueues")
			}
			handler(asynq.NewTask("taskqueue.periodic.v1", nil), tc.opts, asynq.ErrDuplicateTask)
			handler(asynq.NewTask("taskqueue.periodic.v1", nil), tc.opts, errors.New("redis down"))
			line := logs.String()
			for _, part := range []string{
				"level=INFO", `msg="periodic task enqueue skipped: its uniqueness lock is held"`, asynq.ErrDuplicateTask.Error(),
				"level=ERROR", `msg="periodic task enqueue failed"`, "redis down",
				"task_type=taskqueue.periodic.v1", "queue=" + tc.queue,
			} {
				if !strings.Contains(line, part) {
					t.Fatalf("refused enqueue log lacks %q: %s", part, line)
				}
			}
		})
	}
}

func newRuntimePeriodicTask(t *testing.T, name string) componenttaskqueue.PeriodicTask {
	t.Helper()
	task, err := componenttaskqueue.New(
		componenttaskqueue.Definition{Type: "taskqueue.periodic.v1", Queue: "maintenance"},
		[]byte(`{"id":"scan"}`),
		componenttaskqueue.WithHeader("trace-id", "trace-1"),
	)
	if err != nil {
		t.Fatalf("new component task: %v", err)
	}
	schedule, err := componenttaskqueue.IntervalSchedule(5 * time.Minute)
	if err != nil {
		t.Fatalf("interval schedule: %v", err)
	}
	periodic, err := componenttaskqueue.NewPeriodicTask(
		name,
		schedule,
		task,
		componenttaskqueue.WithUnique(10*time.Minute),
		componenttaskqueue.WithMaxRetry(1),
		componenttaskqueue.WithTimeout(time.Minute),
	)
	if err != nil {
		t.Fatalf("new periodic task: %v", err)
	}
	return periodic
}
