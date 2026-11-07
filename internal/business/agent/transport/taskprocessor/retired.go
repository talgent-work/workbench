package taskprocessor

import (
	"context"
	"log/slog"

	"github.com/go-jimu/components/taskqueue"
)

// Task types Agent no longer enqueues. A task of such a type may still wait in
// a queue from before the deployment that removed it; with no processor the
// worker would retry it without end. It is dropped instead: the periodic sweep
// wakes whatever it was recovering under the current tasks within one interval.
// An entry may go once every deployment has run a version that drops it.
var (
	retiredRecoveryTypes = []taskqueue.TaskType{
		"agent.allocation.reconcile.v1",
		"agent.allocation.reconnect.v1",
		"agent.allocation.observe-sandbox.v1",
		"agent.allocation.deliver-credentials.v1",
		"agent.allocation.sync-inputs.v1",
		"agent.work-creation.recover.v1",
		"agent.continuation.recover.v1",
	}
	retiredPreparationTypes = []taskqueue.TaskType{
		"agent.allocation.prepare.v1",
		"agent.initial-execution.prepare.v1",
	}
)

// retired drops each queued task of a removed type, logging it.
func retired(logger *slog.Logger, kinds []taskqueue.TaskType) []taskqueue.Processor {
	processors := make([]taskqueue.Processor, 0, len(kinds))
	for _, kind := range kinds {
		processors = append(processors, taskqueue.NewProcessor(kind, func(ctx context.Context, _ taskqueue.Task) error {
			execution, _ := taskqueue.ExecutionInfoFromContext(ctx)
			logger.InfoContext(ctx, "task of a removed type dropped", slog.String("task_type", string(kind)), slog.String("task_id", execution.TaskID()))
			return nil
		}))
	}
	return processors
}
