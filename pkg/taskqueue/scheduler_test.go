package taskqueue

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
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
