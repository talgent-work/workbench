// Package taskprocessor decodes Agent recovery tasks and delegates each to one
// Application recovery use case.
package taskprocessor

import (
	"context"
	"log/slog"

	"github.com/go-jimu/components/taskqueue"
	agenttaskv1 "github.com/talgent-work/workbench/gen/agent/task/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"google.golang.org/protobuf/proto"
)

// RecoveryProcessors serve the recovery queue.
func RecoveryProcessors(recovery *application.Recovery, logger *slog.Logger) []taskqueue.Processor {
	return []taskqueue.Processor{
		processor(logger, agenttask.ReconcileAllocationType, func() *agenttaskv1.ReconcileAllocationTaskV1 { return &agenttaskv1.ReconcileAllocationTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.ReconcileAllocationTaskV1) error {
				return recovery.ReconcileAllocation(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.ReconnectAllocationType, func() *agenttaskv1.ReconnectAllocationTaskV1 { return &agenttaskv1.ReconnectAllocationTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.ReconnectAllocationTaskV1) error {
				return recovery.ReconnectAllocation(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.DeliverCredentialsType, func() *agenttaskv1.DeliverCredentialsTaskV1 { return &agenttaskv1.DeliverCredentialsTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.DeliverCredentialsTaskV1) error {
				return recovery.DeliverCredentials(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.SyncInputsType, func() *agenttaskv1.SyncInputsTaskV1 { return &agenttaskv1.SyncInputsTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.SyncInputsTaskV1) error {
				return recovery.SyncInputs(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.ObserveSandboxType, func() *agenttaskv1.ObserveSandboxTaskV1 { return &agenttaskv1.ObserveSandboxTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.ObserveSandboxTaskV1) error {
				return recovery.ObserveSandbox(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.RecoverWorkType, func() *agenttaskv1.RecoverWorkTaskV1 { return &agenttaskv1.RecoverWorkTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.RecoverWorkTaskV1) error {
				return recovery.RecoverWork(ctx, p.GetWorkId())
			}),
		processor(logger, agenttask.RecoverContinuationType, func() *agenttaskv1.RecoverContinuationTaskV1 { return &agenttaskv1.RecoverContinuationTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.RecoverContinuationTaskV1) error {
				return recovery.RecoverContinuation(ctx, p.GetWorkId())
			}),
		processor(logger, agenttask.RecoverWorkCreationType, func() *agenttaskv1.RecoverWorkCreationTaskV1 { return &agenttaskv1.RecoverWorkCreationTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.RecoverWorkCreationTaskV1) error {
				return recovery.RecoverWorkCreation(ctx, p.GetRequestId())
			}),
		processor(logger, agenttask.RequestWorkspaceReclamationType, func() *agenttaskv1.RequestWorkspaceReclamationTaskV1 {
			return &agenttaskv1.RequestWorkspaceReclamationTaskV1{}
		},
			func(ctx context.Context, p *agenttaskv1.RequestWorkspaceReclamationTaskV1) error {
				return recovery.SendWorkspaceReclamation(ctx, p.GetWorkId(), p.GetWorkspaceReference())
			}),
		processor(logger, agenttask.SweepRecoveryType, func() *agenttaskv1.SweepRecoveryTaskV1 { return &agenttaskv1.SweepRecoveryTaskV1{} },
			func(ctx context.Context, _ *agenttaskv1.SweepRecoveryTaskV1) error { return recovery.Sweep(ctx) }),
	}
}

// PreparationProcessors serve the preparation queue, whose attempts may take
// minutes and so must not hold recovery workers.
func PreparationProcessors(recovery *application.Recovery, logger *slog.Logger) []taskqueue.Processor {
	return []taskqueue.Processor{
		processor(logger, agenttask.PrepareAllocationType, func() *agenttaskv1.PrepareAllocationTaskV1 { return &agenttaskv1.PrepareAllocationTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.PrepareAllocationTaskV1) error {
				return recovery.PrepareAllocation(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.PrepareInitialExecutionType, func() *agenttaskv1.PrepareInitialExecutionTaskV1 { return &agenttaskv1.PrepareInitialExecutionTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.PrepareInitialExecutionTaskV1) error {
				return recovery.PrepareInitialExecution(ctx, p.GetWorkId())
			}),
	}
}

// processor decodes one payload type and delegates once. A malformed payload
// can never succeed, yet it is not archived: an archived task keeps its ID and
// would absorb every later wake for the same object. It is logged and dropped,
// and the object's next wake or the periodic sweep recovers it.
func processor[T proto.Message](logger *slog.Logger, kind taskqueue.TaskType, empty func() T, handle func(context.Context, T) error) taskqueue.Processor {
	return taskqueue.NewProcessor(kind, func(ctx context.Context, task taskqueue.Task) error {
		payload := empty()
		if err := proto.Unmarshal(task.Payload(), payload); err != nil {
			execution, _ := taskqueue.ExecutionInfoFromContext(ctx)
			logger.ErrorContext(ctx, "malformed recovery task dropped", slog.String("task_type", string(kind)), slog.String("task_id", execution.TaskID()), slog.Any("error", err))
			return nil
		}
		return handle(ctx, payload)
	})
}
