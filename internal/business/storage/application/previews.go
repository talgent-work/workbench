package application

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

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
func (a *Application) PreviewFile(ctx context.Context, owner string, kind domain.FileKind, name string) (FilePreview, error) {
	opened, err := a.openForPreview(ctx, owner, kind, name)
	if err != nil {
		return FilePreview{}, err
	}
	return a.filePreview(ctx, opened)
}

// RequestFilePreview asks for the current content's Preview Copy when the
// file needs one, also after the work the space holds has ended; a content
// already rendering, rendered or found unrenderable is not rendered again.
func (a *Application) RequestFilePreview(ctx context.Context, owner string, kind domain.FileKind, name string) (FilePreview, error) {
	opened, err := a.openForPreview(ctx, owner, kind, name)
	if err != nil {
		return FilePreview{}, err
	}
	preview, err := a.filePreview(ctx, opened)
	if err != nil || preview.Verdict.Mode != domain.PreviewRender || preview.Rendering {
		return preview, err
	}
	render := task.RenderPreview{
		Owner: opened.position.Owner, Kind: string(opened.position.Kind),
		Name: opened.position.Name, Digest: opened.key.Digest,
	}
	if err := task.RequestRendering(ctx, a.enqueuer, render, a.preview.Rendering, a.now()); err != nil {
		return FilePreview{}, fmt.Errorf("storage: request rendering: %w", err)
	}
	preview.Rendering = true
	return preview, nil
}

// RenderPreview renders one requested content. A content that meanwhile has
// a Preview Copy or a recorded failure, or that is no longer at its position,
// is left alone. A busy converter returns ErrConverterBusy so the rendering
// is retried; any other temporary failure leaves nothing behind.
func (a *Application) RenderPreview(ctx context.Context, render task.RenderPreview) error {
	key, err := domain.NewPreviewKey(render.Digest)
	if err != nil {
		return err
	}
	outcome, err := a.previews.PreviewOutcome(ctx, key, a.previewSince())
	if err != nil || outcome.Copy || outcome.Failure != domain.PreviewRefusalNone {
		return err
	}
	position, err := domain.NewFilePosition(render.Owner, domain.FileKind(render.Kind), render.Name)
	if err != nil {
		return err
	}
	opened, err := a.persistent.Open(ctx, position.Owner, position)
	if errors.Is(err, ErrFileNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	defer opened.Content.Close()
	if opened.File.Attributes.ContentDigest != key.Digest {
		return nil
	}
	result, pdf, err := a.converter.Render(ctx, position.Name, opened.Content, a.preview.Limits.RenderedPDF)
	if err != nil {
		return err
	}
	if pdf != nil {
		defer pdf.Close()
	}
	if result.Outcome == domain.ConverterBusy {
		return ErrConverterBusy
	}
	settled := domain.SettleRendering(result, a.preview.Limits)
	switch {
	case settled.Copy && pdf != nil:
		return a.previews.StorePreview(ctx, key, pdf)
	case settled.Failure != domain.PreviewRefusalNone:
		return a.previews.RecordFailure(ctx, key, settled.Failure)
	}
	a.logger.WarnContext(ctx, "preview rendering failed for now; the next opening may try again", "digest", key.Digest, "outcome", int(result.Outcome))
	return nil
}

// openedForPreview is a file as a preview judges it.
type openedForPreview struct {
	position  domain.FilePosition
	stored    StoredFile
	structure domain.ContentStructure
	key       domain.PreviewKey
}

func (a *Application) openForPreview(ctx context.Context, owner string, kind domain.FileKind, name string) (openedForPreview, error) {
	_, position, err := a.existingFile(ctx, owner, kind, name)
	if err != nil {
		return openedForPreview{}, err
	}
	stored, structure, err := a.persistent.Inspect(ctx, position.Owner, position)
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
	// Only an admitted office file has an outcome to look up.
	if verdict.Mode == domain.PreviewRender {
		outcome, err := a.previews.PreviewOutcome(ctx, opened.key, a.previewSince())
		if err != nil {
			return FilePreview{}, err
		}
		subject.Outcome = outcome
		verdict = domain.AdmitForPreview(subject, a.preview.Limits)
	}
	preview := FilePreview{Verdict: verdict}
	switch {
	case verdict.Mode == domain.PreviewRender:
		rendering, err := a.rendering.Rendering(ctx, opened.key)
		if err != nil {
			return FilePreview{}, err
		}
		preview.Rendering = rendering
	case verdict.Readable():
		expiresAt := a.now().UTC().Add(a.linkTTL)
		grant := AccessGrant{
			Owner: opened.position.Owner, Position: opened.position, ContentDigest: opened.key.Digest,
			PreviewCopy: verdict.Mode == domain.PreviewShowCopy,
		}
		link := a.links.AccessLink(grant, expiresAt)
		preview.Link = &Link{URL: link.URL, Path: link.Path, ExpiresAt: expiresAt}
	}
	return preview, nil
}

// readPreviewCopy opens the Preview Copy a preview link names, under the
// file's name with a .pdf extension.
func (a *Application) readPreviewCopy(ctx context.Context, grant AccessGrant) (OpenedFile, error) {
	key, err := domain.NewPreviewKey(grant.ContentDigest)
	if err != nil {
		return OpenedFile{}, ErrInvalidLink
	}
	opened, err := a.previews.OpenPreview(ctx, key, a.previewSince())
	if err != nil {
		return OpenedFile{}, err
	}
	name := grant.Position.Name
	position := grant.Position
	position.Name = name[:len(name)-len(path.Ext(name))] + ".pdf"
	return OpenedFile{Content: opened.Content, File: StoredFile{
		Position: position, SizeBytes: opened.SizeBytes, ModifiedAt: opened.MadeAt,
		Attributes: FileAttributes{ContentType: "application/pdf", ContentDigest: key.Digest},
	}}, nil
}
