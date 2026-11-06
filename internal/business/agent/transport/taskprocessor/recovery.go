// Package taskprocessor decodes Agent recovery tasks and delegates each to one
// Application recovery use case.
package taskprocessor

import (
	"context"
	"log/slog"

	"github.com/go-jimu/components/taskqueue"
	agenttaskv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/task/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"google.golang.org/protobuf/proto"
)

// RecoveryProcessors serve the recovery queue.
func RecoveryProcessors(recovery *application.Recovery, logger *slog.Logger) []taskqueue.Processor {
	return []taskqueue.Processor{
		processor(logger, agenttask.ReconcileAllocationType, func() *agenttaskv1.ReconcileAllocationTask { return &agenttaskv1.ReconcileAllocationTask{} },
			func(ctx context.Context, p *agenttaskv1.ReconcileAllocationTask) error {
				return recovery.ReconcileAllocation(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.ReconnectAllocationType, func() *agenttaskv1.ReconnectAllocationTask { return &agenttaskv1.ReconnectAllocationTask{} },
			func(ctx context.Context, p *agenttaskv1.ReconnectAllocationTask) error {
				return recovery.ReconnectAllocation(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.DeliverCredentialsType, func() *agenttaskv1.DeliverCredentialsTask { return &agenttaskv1.DeliverCredentialsTask{} },
			func(ctx context.Context, p *agenttaskv1.DeliverCredentialsTask) error {
				return recovery.DeliverCredentials(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.SyncInputsType, func() *agenttaskv1.SyncInputsTask { return &agenttaskv1.SyncInputsTask{} },
			func(ctx context.Context, p *agenttaskv1.SyncInputsTask) error {
				return recovery.SyncInputs(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.ObserveSandboxType, func() *agenttaskv1.ObserveSandboxTask { return &agenttaskv1.ObserveSandboxTask{} },
			func(ctx context.Context, p *agenttaskv1.ObserveSandboxTask) error {
				return recovery.ObserveSandbox(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.RecoverWorkType, func() *agenttaskv1.RecoverWorkTask { return &agenttaskv1.RecoverWorkTask{} },
			func(ctx context.Context, p *agenttaskv1.RecoverWorkTask) error {
				return recovery.RecoverWork(ctx, p.GetWorkId())
			}),
		processor(logger, agenttask.RecoverContinuationType, func() *agenttaskv1.RecoverContinuationTask { return &agenttaskv1.RecoverContinuationTask{} },
			func(ctx context.Context, p *agenttaskv1.RecoverContinuationTask) error {
				return recovery.RecoverContinuation(ctx, p.GetWorkId())
			}),
		processor(logger, agenttask.RecoverWorkCreationType, func() *agenttaskv1.RecoverWorkCreationTask { return &agenttaskv1.RecoverWorkCreationTask{} },
			func(ctx context.Context, p *agenttaskv1.RecoverWorkCreationTask) error {
				return recovery.RecoverWorkCreation(ctx, p.GetRequestId())
			}),
		processor(logger, agenttask.RequestWorkspaceReclamationType, func() *agenttaskv1.RequestWorkspaceReclamationTask {
			return &agenttaskv1.RequestWorkspaceReclamationTask{}
		},
			func(ctx context.Context, p *agenttaskv1.RequestWorkspaceReclamationTask) error {
				return recovery.SendWorkspaceReclamation(ctx, p.GetWorkId(), p.GetWorkspaceReference())
			}),
		processor(logger, agenttask.SweepRecoveryType, func() *agenttaskv1.SweepRecoveryTask { return &agenttaskv1.SweepRecoveryTask{} },
			func(ctx context.Context, _ *agenttaskv1.SweepRecoveryTask) error { return recovery.Sweep(ctx) }),
	}
}

// PreparationProcessors serve the preparation queue, whose attempts may take
// minutes and so must not hold recovery workers.
func PreparationProcessors(recovery *application.Recovery, logger *slog.Logger) []taskqueue.Processor {
	return []taskqueue.Processor{
		processor(logger, agenttask.PrepareAllocationType, func() *agenttaskv1.PrepareAllocationTask { return &agenttaskv1.PrepareAllocationTask{} },
			func(ctx context.Context, p *agenttaskv1.PrepareAllocationTask) error {
				return recovery.PrepareAllocation(ctx, p.GetAllocationId())
			}),
		processor(logger, agenttask.PrepareInitialExecutionType, func() *agenttaskv1.PrepareInitialExecutionTask { return &agenttaskv1.PrepareInitialExecutionTask{} },
			func(ctx context.Context, p *agenttaskv1.PrepareInitialExecutionTask) error {
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
