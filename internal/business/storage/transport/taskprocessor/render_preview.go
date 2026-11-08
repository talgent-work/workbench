// Package taskprocessor runs Storage's internal tasks.
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

// Rendering is the use case the task delegates to.
type Rendering interface {
	RenderPreview(ctx context.Context, render applicationtask.RenderPreview) error
}

// RenderPreviewProcessor renders one requested content. Only a busy
// converter is retried, after the lane's retry delay and within the task's
// deadline, without counting as a failed attempt; any other failure ends
// the task, leaving the next opening to ask again.
type RenderPreviewProcessor struct {
	app    Rendering
	logger *slog.Logger
}

var _ taskqueue.Processor = (*RenderPreviewProcessor)(nil)

func NewRenderPreviewProcessor(app Rendering, logger *slog.Logger) *RenderPreviewProcessor {
	return &RenderPreviewProcessor{app: app, logger: logger}
}

func (*RenderPreviewProcessor) TaskType() taskqueue.TaskType {
	return applicationtask.RenderPreviewType
}

func (p *RenderPreviewProcessor) Process(ctx context.Context, queued taskqueue.Task) error {
	render, err := applicationtask.ParseRenderPreview(queued.Payload())
	if err != nil {
		p.logger.ErrorContext(ctx, "preview rendering task is unreadable", slog.Any("error", err))
		return taskqueue.ErrSkipRetry
	}
	err = p.app.RenderPreview(ctx, render)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, application.ErrConverterBusy) && ctx.Err() == nil:
		return fmt.Errorf("%w: %w", pkgtaskqueue.ErrRetryLater, err)
	}
	p.logger.WarnContext(ctx, "preview rendering ended without a result", slog.String("digest", render.Digest), slog.Any("error", err))
	return taskqueue.ErrSkipRetry
}
