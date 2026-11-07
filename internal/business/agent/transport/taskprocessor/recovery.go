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
	return append([]taskqueue.Processor{
		processor(logger, agenttask.ReconcileExecutionType, func() *agenttaskv1.ReconcileExecutionTaskV1 { return &agenttaskv1.ReconcileExecutionTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.ReconcileExecutionTaskV1) error {
				return recovery.ReconcileExecution(ctx, p.GetExecutionId(), p.GetObserveEnvironments())
			}),
		processor(logger, agenttask.RecoverWorkType, func() *agenttaskv1.RecoverWorkTaskV1 { return &agenttaskv1.RecoverWorkTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.RecoverWorkTaskV1) error {
				return recovery.RecoverWork(ctx, p.GetWorkId())
			}),
		processor(logger, agenttask.RecoverContinuationType, func() *agenttaskv1.RecoverContinuationTaskV1 { return &agenttaskv1.RecoverContinuationTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.RecoverContinuationTaskV1) error {
				return recovery.RecoverContinuation(ctx, p.GetWorkId())
			}),
		processor(logger, agenttask.RequestWorkspaceReclamationType, func() *agenttaskv1.RequestWorkspaceReclamationTaskV1 {
			return &agenttaskv1.RequestWorkspaceReclamationTaskV1{}
		},
			func(ctx context.Context, p *agenttaskv1.RequestWorkspaceReclamationTaskV1) error {
				return recovery.SendWorkspaceReclamation(ctx, p.GetWorkId(), p.GetWorkspaceReference())
			}),
		processor(logger, agenttask.SweepRecoveryType, func() *agenttaskv1.SweepRecoveryTaskV1 { return &agenttaskv1.SweepRecoveryTaskV1{} },
			func(ctx context.Context, _ *agenttaskv1.SweepRecoveryTaskV1) error { return recovery.Sweep(ctx) }),
	}, retired(logger, retiredRecoveryTypes)...)
}

// PreparationProcessors serve the preparation queue, whose attempts may take
// minutes and so must not hold recovery workers: an execution's long step and
// the initial execution's preparation.
func PreparationProcessors(recovery *application.Recovery, logger *slog.Logger) []taskqueue.Processor {
	return append([]taskqueue.Processor{
		processor(logger, agenttask.RunLongExecutionStepType, func() *agenttaskv1.RunLongExecutionStepTaskV1 { return &agenttaskv1.RunLongExecutionStepTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.RunLongExecutionStepTaskV1) error {
				return recovery.RunLongExecutionStep(ctx, p.GetExecutionId())
			}),
		processor(logger, agenttask.PrepareInitialExecutionType, func() *agenttaskv1.PrepareInitialExecutionTaskV1 { return &agenttaskv1.PrepareInitialExecutionTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.PrepareInitialExecutionTaskV1) error {
				return recovery.PrepareInitialExecution(ctx, p.GetWorkId())
			}),
	}, retired(logger, retiredPreparationTypes)...)
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
