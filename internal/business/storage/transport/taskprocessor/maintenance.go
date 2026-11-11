package taskprocessor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/go-jimu/components/taskqueue"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	applicationtask "github.com/talgent-work/workbench/internal/business/storage/application/task"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
)

// ContentRemoval is the use case the removal task delegates to.
type ContentRemoval interface {
	RemoveReclaimedContent(ctx context.Context, removal applicationtask.RemoveReclaimedContent) error
}

// RemoveReclaimedContentProcessor removes one reclaimed Directory's content.
// A publication still copying from it retries the removal after the lane's
// retry delay without counting as a failure; any other failure retries too,
// within the removal's ceiling and deadline. The last attempt's failure ends
// the reclamation of that Directory's content, logged as an error.
type RemoveReclaimedContentProcessor struct {
	app    ContentRemoval
	logger *slog.Logger
}

var _ taskqueue.Processor = (*RemoveReclaimedContentProcessor)(nil)

func NewRemoveReclaimedContentProcessor(app ContentRemoval, logger *slog.Logger) *RemoveReclaimedContentProcessor {
	return &RemoveReclaimedContentProcessor{app: app, logger: logger}
}

func (*RemoveReclaimedContentProcessor) TaskType() taskqueue.TaskType {
	return applicationtask.RemoveReclaimedContentType
}

func (p *RemoveReclaimedContentProcessor) Process(ctx context.Context, queued taskqueue.Task) error {
	removal, err := applicationtask.ParseRemoveReclaimedContent(queued.Payload())
	if err != nil {
		p.logger.ErrorContext(ctx, "content removal task is unreadable", slog.Any("error", err))
		return taskqueue.ErrSkipRetry
	}
	err = p.app.RemoveReclaimedContent(ctx, removal)
	if err == nil {
		return nil
	}
	if pkgtaskqueue.LastAttempt(ctx) {
		work, _ := removal.Directory.Owner.Work()
		execution, _ := taskqueue.ExecutionInfoFromContext(ctx)
		p.logger.ErrorContext(ctx, "reclaimed content removal failed", slog.String("work_id", work), slog.String("directory", removal.Directory.Name), slog.String("task_id", execution.TaskID()), slog.Any("error", err))
		return err
	}
	if errors.Is(err, application.ErrPublicationInProgress) {
		return fmt.Errorf("%w: %w", pkgtaskqueue.ErrRetryLater, err)
	}
	return err
}

// MappedViewRepair is the use case the repair task delegates to.
type MappedViewRepair interface {
	RepairMappedView(ctx context.Context) error
}

// RepairMappedViewProcessor repairs the Mapped View; a failed run is
// replaced by the next hour's.
type RepairMappedViewProcessor struct {
	app MappedViewRepair
}

var _ taskqueue.Processor = (*RepairMappedViewProcessor)(nil)

func NewRepairMappedViewProcessor(app MappedViewRepair) *RepairMappedViewProcessor {
	return &RepairMappedViewProcessor{app: app}
}

func (*RepairMappedViewProcessor) TaskType() taskqueue.TaskType {
	return applicationtask.RepairMappedViewType
}

func (p *RepairMappedViewProcessor) Process(ctx context.Context, _ taskqueue.Task) error {
	return p.app.RepairMappedView(ctx)
}

// PreviewLapse is the use case the lapse task delegates to.
type PreviewLapse interface {
	LapsePreviewCopies(ctx context.Context) error
}

// LapsePreviewCopiesProcessor removes lapsed Preview Copies; a failed run is
// replaced by the next hour's.
type LapsePreviewCopiesProcessor struct {
	app PreviewLapse
}

var _ taskqueue.Processor = (*LapsePreviewCopiesProcessor)(nil)

func NewLapsePreviewCopiesProcessor(app PreviewLapse) *LapsePreviewCopiesProcessor {
	return &LapsePreviewCopiesProcessor{app: app}
}

func (*LapsePreviewCopiesProcessor) TaskType() taskqueue.TaskType {
	return applicationtask.LapsePreviewCopiesType
}

func (p *LapsePreviewCopiesProcessor) Process(ctx context.Context, _ taskqueue.Task) error {
	return p.app.LapsePreviewCopies(ctx)
}
