package taskqueue

import (
	"context"
	"io"
	"log/slog"
	"testing"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
)

type runtimeTestPayload struct {
	ID string `json:"id"`
}

// Component processors are registered with the lane's worker in
// internal/pkg/taskqueue, keeping handler registration and middleware out of
// cmd and bounded-context modules.
func TestNewLane_AcceptsComponentProcessors(t *testing.T) {
	processor := componenttaskqueue.NewProcessor("taskqueue.runtime.worker.v1", func(context.Context, componenttaskqueue.Task) error {
		return nil
	})
	lane, err := NewLane(
		"runtime-test",
		Option{RedisAddr: "127.0.0.1:6379", QueueName: "runtime-test", WorkerConcurrency: 1},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		[]componenttaskqueue.Processor{processor},
		nil,
	)
	if err != nil {
		t.Fatalf("new lane: %v", err)
	}
	if lane.Name() != "runtime-test" {
		t.Fatalf("lane name = %q", lane.Name())
	}
	if _, err := NewLane("runtime-test", Option{RedisAddr: "127.0.0.1:6379"}, slog.New(slog.DiscardHandler), []componenttaskqueue.Processor{processor, processor}, nil); err == nil {
		t.Fatal("a lane accepted two processors of one task type")
	}
}

func TestWorkerQueues_UsesMultipleConfiguredQueues(t *testing.T) {
	queues := workerQueues(Option{
		QueueName:  "legacy",
		QueueNames: []string{"scm-maintenance", " preview-convert ", ""},
	})
	if len(queues) != 2 || queues["scm-maintenance"] != 1 || queues["preview-convert"] != 1 {
		t.Fatalf("queues = %#v, want scm-maintenance and preview-convert", queues)
	}
}

func TestWorkerQueues_FallsBackToSingleQueueName(t *testing.T) {
	queues := workerQueues(Option{QueueName: "supervisor"})
	if len(queues) != 1 || queues["supervisor"] != 1 {
		t.Fatalf("queues = %#v, want supervisor", queues)
	}
}
