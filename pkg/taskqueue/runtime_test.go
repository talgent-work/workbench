package taskqueue

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type runtimeTestPayload struct {
	ID string `json:"id"`
}

// The runtime registry must be the component SchemaRegistry so task contracts
// are service-owned and bounded contexts do not need local payload codecs.
func TestNewSchemaRegistry_ReturnsComponentRegistry(t *testing.T) {
	registry := NewSchemaRegistry()
	def := componenttaskqueue.Definition{Type: "taskqueue.runtime.schema.v1", Queue: "runtime-test"}
	if err := registry.Register(def, func() any { return &runtimeTestPayload{} }); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	task, err := registry.NewJSONTask(&runtimeTestPayload{ID: "payload-1"})
	if err != nil {
		t.Fatalf("new json task: %v", err)
	}
	if task.Type() != def.Type {
		t.Fatalf("task type = %q, want %q", task.Type(), def.Type)
	}
}

// Component processors must be registered with the shared worker in
// internal/pkg/taskqueue, keeping handler registration and middleware out of
// cmd and bounded-context modules.
func TestNewWorker_AcceptsComponentProcessors(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	processor := componenttaskqueue.NewProcessor("taskqueue.runtime.worker.v1", func(context.Context, componenttaskqueue.Task) error {
		return nil
	})
	worker, err := NewWorker(
		lc,
		Config{RedisAddr: "127.0.0.1:6379", QueueName: "runtime-test", WorkerConcurrency: 1},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProcessorIn{Processors: []componenttaskqueue.Processor{processor}},
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if worker == nil {
		t.Fatal("worker should be provided")
	}
}

func TestWorkerQueues_UsesMultipleConfiguredQueues(t *testing.T) {
	queues := workerQueues(Config{
		QueueName:  "legacy",
		QueueNames: []string{"scm-maintenance", " preview-convert ", ""},
	})
	if len(queues) != 2 || queues["scm-maintenance"] != 1 || queues["preview-convert"] != 1 {
		t.Fatalf("queues = %#v, want scm-maintenance and preview-convert", queues)
	}
}

func TestWorkerQueues_FallsBackToSingleQueueName(t *testing.T) {
	queues := workerQueues(Config{QueueName: "supervisor"})
	if len(queues) != 1 || queues["supervisor"] != 1 {
		t.Fatalf("queues = %#v, want supervisor", queues)
	}
}

// Component Start is non-blocking and must finish before Fx announces readiness.
// Startup errors must abort the application instead of disappearing in a goroutine.
func TestAppendRunnerLifecyclePropagatesStartupFailure(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	failure := errors.New("scheduler cannot start")
	runner := &lifecycleRunner{startErr: failure}
	appendRunnerLifecycle(lc, runner)
	if err := lc.Start(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("start error = %v, want %v", err, failure)
	}
}

func TestAppendRunnerLifecycleStartsBeforeReadinessAndShutsDown(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	runner := &lifecycleRunner{}
	appendRunnerLifecycle(lc, runner)
	lc.Append(fx.Hook{OnStart: func(context.Context) error {
		if !runner.started {
			return errors.New("application became ready before taskqueue startup")
		}
		return nil
	}})
	lc.RequireStart()
	lc.RequireStop()
	if runner.shutdowns != 1 {
		t.Fatalf("shutdown count = %d, want 1", runner.shutdowns)
	}
}

type lifecycleRunner struct {
	startErr  error
	started   bool
	shutdowns int
}

func (r *lifecycleRunner) Start(context.Context) error {
	r.started = true
	return r.startErr
}

func (r *lifecycleRunner) Shutdown(context.Context) error {
	r.shutdowns++
	return nil
}
