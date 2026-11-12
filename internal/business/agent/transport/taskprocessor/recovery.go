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
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
	"google.golang.org/protobuf/proto"
)

// RecoveryProcessors serve the recovery queue.
func RecoveryProcessors(recovery *application.Recovery, logger *slog.Logger) []taskqueue.Processor {
	return append([]taskqueue.Processor{
		processor(logger, agenttask.ReconcileExecutionType, func() *agenttaskv1.ReconcileExecutionTaskV1 { return &agenttaskv1.ReconcileExecutionTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.ReconcileExecutionTaskV1) error {
				return recovery.ReconcileExecution(ctx, p.GetExecutionId())
			}),
		processor(logger, agenttask.RecoverWorkType, func() *agenttaskv1.RecoverWorkTaskV1 { return &agenttaskv1.RecoverWorkTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.RecoverWorkTaskV1) error {
				return recovery.RecoverWork(ctx, p.GetWorkId())
			}),
		processor(logger, agenttask.RequestWorkReclamationType, func() *agenttaskv1.RequestWorkspaceReclamationTaskV1 {
			return &agenttaskv1.RequestWorkspaceReclamationTaskV1{}
		},
			func(ctx context.Context, p *agenttaskv1.RequestWorkspaceReclamationTaskV1) error {
				err := recovery.SendWorkReclamation(ctx, p.GetWorkId())
				if err != nil && pkgtaskqueue.LastAttempt(ctx) {
					// Agent keeps no reclamation obligation: the request
					// ends here, and only this error shows it.
					execution, _ := taskqueue.ExecutionInfoFromContext(ctx)
					logger.ErrorContext(ctx, "work reclamation failed", slog.String("work_id", p.GetWorkId()), slog.String("task_id", execution.TaskID()), slog.Any("error", err))
				}
				return err
			}),
		processor(logger, agenttask.CloseRemovedApplicationWorkType, func() *agenttaskv1.CloseRemovedApplicationWorkTaskV1 {
			return &agenttaskv1.CloseRemovedApplicationWorkTaskV1{}
		},
			func(ctx context.Context, p *agenttaskv1.CloseRemovedApplicationWorkTaskV1) error {
				err := recovery.CloseRemovedApplicationWork(ctx, p.GetApplicationId(), p.GetWorkId())
				if err != nil && pkgtaskqueue.LastAttempt(ctx) {
					// Agent keeps no obligation for the close: it ends here,
					// the Work stops at its idle timeout, and only this error
					// shows it.
					execution, _ := taskqueue.ExecutionInfoFromContext(ctx)
					logger.ErrorContext(ctx, "removed application work close failed", slog.String("work_id", p.GetWorkId()),
						slog.String("application_id", p.GetApplicationId()), slog.String("task_id", execution.TaskID()), slog.Any("error", err))
				}
				return err
			}),
		processor(logger, agenttask.SweepRecoveryType, func() *agenttaskv1.SweepRecoveryTaskV1 { return &agenttaskv1.SweepRecoveryTaskV1{} },
			func(ctx context.Context, _ *agenttaskv1.SweepRecoveryTaskV1) error { return recovery.Sweep(ctx) }),
	}, retired(logger, retiredRecoveryTypes)...)
}

// PreparationProcessors serve the preparation queue, whose attempts may take
// minutes and so must not hold recovery workers: an execution's long step.
func PreparationProcessors(recovery *application.Recovery, logger *slog.Logger) []taskqueue.Processor {
	return append([]taskqueue.Processor{
		processor(logger, agenttask.RunLongExecutionStepType, func() *agenttaskv1.RunLongExecutionStepTaskV1 { return &agenttaskv1.RunLongExecutionStepTaskV1{} },
			func(ctx context.Context, p *agenttaskv1.RunLongExecutionStepTaskV1) error {
				return recovery.RunLongExecutionStep(ctx, p.GetExecutionId())
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
