package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"

	"github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// ErrConverterBusy means the converter had no capacity for a rendering now;
// the rendering is retried after a short delay, without counting as failed.
var ErrConverterBusy = errors.New("storage: the converter is busy")

// FilePreview is how a file is shown, with an Access Link for inline reading
// when the verdict shows bytes. Rendering says a Preview Copy of the content
// is queued or being made.
type FilePreview struct {
	Verdict   domain.PreviewVerdict
	Link      *Link
	Rendering bool
}

// PreviewFile tells how an existing Attachment or Artifact is shown, judged
// afresh from its current content; the owner's context decided the reader
// first. Nothing is rendered by asking.
func (a *Application) PreviewFile(ctx context.Context, at Position) (FilePreview, error) {
	opened, err := a.openForPreview(ctx, at)
	if err != nil {
		return FilePreview{}, err
	}
	return a.filePreview(ctx, opened)
}

// RequestFilePreview asks for the current content's Preview Copy when the
// file needs one, also after the work its Directory serves has ended; a
// content already rendering, rendered or found unrenderable is not rendered
// again.
func (a *Application) RequestFilePreview(ctx context.Context, at Position) (FilePreview, error) {
	opened, err := a.openForPreview(ctx, at)
	if err != nil {
		return FilePreview{}, err
	}
	preview, err := a.filePreview(ctx, opened)
	if err != nil || preview.Verdict.Mode != domain.PreviewRender || preview.Rendering {
		return preview, err
	}
	requested := domain.RequestRendering(opened.key, a.now())
	err = a.previewCopies.Add(ctx, requested)
	if errors.Is(err, domain.ErrConcurrentModification) {
		// Another opening requested it first; its rendering serves this one.
		preview.Rendering = true
		return preview, nil
	}
	if err != nil {
		return FilePreview{}, err
	}
	render := task.RenderPreview{Directory: opened.position.Directory, Name: opened.position.Name, Digest: opened.key.Digest}
	if err := task.RequestRendering(ctx, a.tasks.Renderings, render, a.preview.Rendering, a.now()); err != nil {
		// Never queued, the request holds nothing back from the next opening.
		return FilePreview{}, errors.Join(fmt.Errorf("storage: request rendering: %w", err), a.previewCopies.Remove(context.WithoutCancel(ctx), opened.key))
	}
	preview.Rendering = true
	return preview, nil
}

// RenderPreview renders one requested content. A content whose rendering
// was settled or abandoned meanwhile is left alone, and one no longer at its
// position is abandoned. A busy converter returns ErrConverterBusy so the
// rendering is retried; any other failure abandons it, leaving the next
// opening to request it again.
func (a *Application) RenderPreview(ctx context.Context, render task.RenderPreview) (err error) {
	key, err := domain.NewPreviewKey(render.Digest)
	if err != nil {
		return err
	}
	requested, err := a.previewCopies.Get(ctx, key)
	if errors.Is(err, domain.ErrPreviewCopyNotFound) {
		return nil
	}
	if err != nil || requested.State != domain.PreviewRendering {
		return err
	}
	defer func() {
		if err != nil && !errors.Is(err, ErrConverterBusy) {
			err = errors.Join(err, a.abandon(ctx, requested))
		}
	}()
	settled, pdf, err := a.renderContent(ctx, render, key)
	if err != nil {
		return err
	}
	if pdf != nil {
		defer pdf.Close()
	}
	if settled.Temporary() {
		a.logger.WarnContext(ctx, "preview rendering failed for now; the next opening may try again", "digest", key.Digest)
		return a.abandon(ctx, requested)
	}
	if err := requested.Settle(settled, a.now()); err != nil {
		return err
	}
	if settled.Copy && pdf == nil {
		return a.abandon(ctx, requested)
	}
	return a.previewCopies.Save(ctx, requested, pdf)
}

// renderContent renders the content still at the requested position; one
// replaced or gone settles nothing.
func (a *Application) renderContent(ctx context.Context, render task.RenderPreview, key domain.PreviewKey) (domain.RenderingSettlement, io.ReadCloser, error) {
	position, err := domain.NewFilePosition(render.Directory, render.Name)
	if err != nil {
		return domain.RenderingSettlement{}, nil, err
	}
	_, backend, err := a.readable(ctx, position.Directory)
	if err != nil {
		return domain.RenderingSettlement{}, nil, nothingToRender(err)
	}
	opened, err := backend.Open(ctx, position)
	if err != nil {
		return domain.RenderingSettlement{}, nil, nothingToRender(err)
	}
	defer opened.Content.Close()
	if opened.File.Attributes.ContentDigest != key.Digest {
		return domain.RenderingSettlement{}, nil, nil
	}
	result, pdf, err := a.converter.Render(ctx, position.Name, opened.Content, a.preview.Limits.RenderedPDF)
	if err != nil {
		return domain.RenderingSettlement{}, nil, err
	}
	if result.Outcome == domain.ConverterBusy {
		if pdf != nil {
			pdf.Close()
		}
		return domain.RenderingSettlement{}, nil, ErrConverterBusy
	}
	return domain.SettleRendering(result, a.preview.Limits), pdf, nil
}

// nothingToRender treats a file gone from its position as a temporary
// outcome: nothing is recorded about the content.
func nothingToRender(err error) error {
	if errors.Is(err, ErrFileNotFound) || errors.Is(err, domain.ErrDirectoryNotOpen) || errors.Is(err, domain.ErrDirectoryReclaimed) {
		return nil
	}
	return err
}

// abandon removes a rendering that left no outcome.
func (a *Application) abandon(ctx context.Context, requested *domain.PreviewCopy) error {
	if err := requested.Abandon(); err != nil {
		return err
	}
	return a.previewCopies.Remove(context.WithoutCancel(ctx), requested.Key)
}

// openedForPreview is a file as a preview judges it.
type openedForPreview struct {
	position  domain.FilePosition
	stored    StoredFile
	structure domain.ContentStructure
	key       domain.PreviewKey
}

func (a *Application) openForPreview(ctx context.Context, at Position) (openedForPreview, error) {
	_, backend, position, err := a.existingFile(ctx, at)
	if err != nil {
		return openedForPreview{}, err
	}
	return a.inspectForPreview(ctx, backend, position)
}

func (a *Application) inspectForPreview(ctx context.Context, backend DirectoryBackendPort, position domain.FilePosition) (openedForPreview, error) {
	stored, structure, err := backend.Inspect(ctx, position)
	if err != nil {
		return openedForPreview{}, err
	}
	key, err := domain.NewPreviewKey(stored.Attributes.ContentDigest)
	if err != nil {
		return openedForPreview{}, err
	}
	return openedForPreview{position: position, stored: stored, structure: structure, key: key}, nil
}

func (a *Application) filePreview(ctx context.Context, opened openedForPreview) (FilePreview, error) {
	subject := domain.PreviewSubject{
		Source: domain.PreviewOfSpaceFile, Name: opened.position.Name, SizeBytes: opened.stored.SizeBytes, Structure: opened.structure,
	}
	verdict := domain.AdmitForPreview(subject, a.preview.Limits)
	var current *domain.PreviewCopy
	// Only an admitted office file has an outcome to look up.
	if verdict.Mode == domain.PreviewRender {
		var err error
		if current, err = a.currentPreviewCopy(ctx, opened.key); err != nil {
			return FilePreview{}, err
		}
		if current != nil {
			subject.Outcome = current.Outcome()
			verdict = domain.AdmitForPreview(subject, a.preview.Limits)
		}
	}
	preview := FilePreview{Verdict: verdict}
	switch {
	case verdict.Mode == domain.PreviewRender:
		preview.Rendering = current != nil && current.State == domain.PreviewRendering
	case verdict.Readable():
		link := a.accessLink(AccessGrant{
			Directory: opened.position.Directory, Path: opened.position.Name, ContentDigest: opened.key.Digest,
			PreviewCopy: verdict.Mode == domain.PreviewShowCopy,
		})
		preview.Link = &link
	}
	return preview, nil
}

// currentPreviewCopy is the content's Preview Copy, nil when it has none. A
// lapsed one, or a rendering past its deadline that can no longer be running,
// is removed and holds nothing.
func (a *Application) currentPreviewCopy(ctx context.Context, key domain.PreviewKey) (*domain.PreviewCopy, error) {
	current, err := a.previewCopies.Get(ctx, key)
	if errors.Is(err, domain.ErrPreviewCopyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := a.now()
	if current.Lapsed(now, a.preview.Retention) || current.RenderingAbandoned(now, a.preview.Rendering.Deadline) {
		return nil, a.previewCopies.Remove(ctx, key)
	}
	return current, nil
}

// readPreviewCopy opens the Preview Copy a preview link names, under the
// file's name with a .pdf extension.
func (a *Application) readPreviewCopy(ctx context.Context, grant AccessGrant) (OpenedFile, error) {
	key, err := domain.NewPreviewKey(grant.ContentDigest)
	if err != nil {
		return OpenedFile{}, ErrInvalidLink
	}
	current, err := a.currentPreviewCopy(ctx, key)
	if err != nil {
		return OpenedFile{}, err
	}
	if current == nil || current.State != domain.PreviewRendered {
		return OpenedFile{}, ErrFileNotFound
	}
	content, size, err := a.previewCopies.OpenRendering(ctx, key)
	if err != nil {
		return OpenedFile{}, err
	}
	name := grant.Path[:len(grant.Path)-len(path.Ext(grant.Path))] + ".pdf"
	return OpenedFile{Content: content, File: StoredFile{
		Position: domain.FilePosition{Directory: grant.Directory, Name: name}, SizeBytes: size, ModifiedAt: current.MadeAt,
		Attributes: FileAttributes{ContentType: "application/pdf", ContentDigest: key.Digest},
	}}, nil
}

// LapsePreviewCopies removes every Preview Copy and recorded failure made,
// and every rendering requested, longer ago than the retention period; their
// files may still exist, which does not keep them.
func (a *Application) LapsePreviewCopies(ctx context.Context) error {
	lapsed, err := a.previewQueries.MadeBefore(ctx, a.now().Add(-a.preview.Retention))
	if err != nil {
		return err
	}
	var failures []error
	for _, key := range lapsed {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := a.previewCopies.Get(ctx, key)
		if errors.Is(err, domain.ErrPreviewCopyNotFound) {
			continue
		}
		if err == nil && current.Lapsed(a.now(), a.preview.Retention) {
			err = a.previewCopies.Remove(ctx, key)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
