//go:build integration

package taskqueue

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// Identical task type names in separate runtime owners must neither collide
// during registration nor dispatch through a different owner's processor.
func TestNamedModulesIsolateWorkersAndSchemas(t *testing.T) {
	addr, cleanup := startRedis(t)
	defer cleanup()
	type receipt struct{ owner, payload string }
	processed := make(chan receipt, 3)
	clients := make(map[string]componenttaskqueue.Enqueuer)
	registries := make(map[string]*componenttaskqueue.SchemaRegistry)
	options := []fx.Option{fx.NopLogger, fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	for _, name := range []string{"storage", "execution", "sandbox"} {
		tag := fmt.Sprintf(`name:%q`, name)
		options = append(options,
			fx.Supply(fx.Annotate(Config{RedisAddr: addr, QueueName: name, WorkerConcurrency: 1}, fx.ResultTags(tag))),
			NamedModule(name),
			fx.Module("business."+name,
				Bind(name),
				fx.Provide(fx.Annotate(func() componenttaskqueue.Processor {
					return componenttaskqueue.NewProcessor(testTaskType, func(_ context.Context, task componenttaskqueue.Task) error {
						processed <- receipt{owner: name, payload: string(task.Payload())}
						return nil
					})
				}, fx.ResultTags(fmt.Sprintf(`group:%q`, "taskqueue.processors."+name)))),
				fx.Invoke(func(client componenttaskqueue.Enqueuer, registry *componenttaskqueue.SchemaRegistry) error {
					clients[name] = client
					registries[name] = registry
					return registry.Register(componenttaskqueue.Definition{Type: testTaskType, Queue: name}, func() any { return &runtimeTestPayload{} })
				}),
			),
		)
	}
	app := fxtest.New(t, options...)
	app.RequireStart()
	defer app.RequireStop()
	for name, client := range clients {
		task, err := registries[name].NewJSONTask(&runtimeTestPayload{ID: name})
		if err != nil {
			t.Fatalf("create %s task: %v", name, err)
		}
		if task.Queue() != name {
			t.Fatalf("%s schema resolved to queue %s", name, task.Queue())
		}
		if err := client.Enqueue(context.Background(), task); err != nil {
			t.Fatalf("enqueue %s: %v", name, err)
		}
	}
	seen := make(map[string]bool)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for range 3 {
		select {
		case got := <-processed:
			want := fmt.Sprintf(`{"id":%q}`, got.owner)
			if got.payload != want || seen[got.owner] {
				t.Fatalf("task reached the wrong worker or was repeated: %+v; seen=%v", got, seen)
			}
			seen[got.owner] = true
		case <-deadline.C:
			t.Fatalf("workers did not process all queues: %v", seen)
		}
	}
}
