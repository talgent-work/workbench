package application

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// ErrPreviewNotFound means the content has no Preview Copy within retention.
var ErrPreviewNotFound = errors.New("storage: preview copy not found")

// ErrConverterBusy means the converter had no capacity for a rendering now;
// the rendering is retried after a short delay, without counting as failed.
var ErrConverterBusy = errors.New("storage: the converter is busy")

// PreviewCopiesPort keeps Preview Copies and recorded rendering failures by
// content, outside every File Space's directory, so the originals mount never
// shows them. Both lapse at retention, counted from when they were made.
type PreviewCopiesPort interface {
	// PreviewOutcome is what a rendering left for the content and made at or
	// after since.
	PreviewOutcome(ctx context.Context, key domain.PreviewKey, since time.Time) (domain.PreviewOutcome, error)
	// StorePreview keeps a rendering; an existing copy of the content stays.
	StorePreview(ctx context.Context, key domain.PreviewKey, rendering io.Reader) error
	// RecordFailure keeps the reason the converter could not render it.
	RecordFailure(ctx context.Context, key domain.PreviewKey, reason domain.PreviewRefusal) error
	// OpenPreview opens a copy made at or after since.
	OpenPreview(ctx context.Context, key domain.PreviewKey, since time.Time) (OpenedPreview, error)
	// SweepPreviews removes copies and failures made before before, reading
	// only the previews directory.
	SweepPreviews(ctx context.Context, before time.Time) error
}

// OpenedPreview is a readable Preview Copy.
type OpenedPreview struct {
	Content   io.ReadSeekCloser
	SizeBytes int64
	MadeAt    time.Time
}

// ConverterPort renders office content to PDF.
type ConverterPort interface {
	// Render converts content named name. A rendering within pdfLimit comes
	// back with its PDF, which the caller closes; one over it comes back as
	// rendered with its size and no PDF.
	Render(ctx context.Context, name string, content io.Reader, pdfLimit int64) (domain.ConverterResult, io.ReadCloser, error)
}

// RenderingTasksPort tells whether a content's rendering is queued or running.
type RenderingTasksPort interface {
	Rendering(ctx context.Context, key domain.PreviewKey) (bool, error)
}

// SweepPreviewCopies removes the Preview Copies and recorded failures that
// lapsed; their files may still exist, which does not keep them.
func (a *Application) SweepPreviewCopies(ctx context.Context) error {
	return a.previews.SweepPreviews(ctx, a.previewSince())
}

// previewSince is the oldest a Preview Copy or failure may be and still hold.
func (a *Application) previewSince() time.Time {
	return a.now().Add(-a.preview.Retention)
}
