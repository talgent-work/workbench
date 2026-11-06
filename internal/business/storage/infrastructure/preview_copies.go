package infrastructure

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// previewsDirectory holds Preview Copies and recorded failures outside the
// spaces directory, so the originals mount never shows them. Each content
// has at most <digest>.pdf, its copy, or <digest>.failed, the reason the
// converter could not render it; its change time is when it was made.
const previewsDirectory = "assets/previews"

const (
	copySuffix    = ".pdf"
	failureSuffix = ".failed"
)

var _ application.PreviewCopiesPort = (*PersistentFiles)(nil)

// PreviewOutcome is the content's copy or recorded failure made since since.
func (p *PersistentFiles) PreviewOutcome(ctx context.Context, key domain.PreviewKey, since time.Time) (domain.PreviewOutcome, error) {
	if err := ctx.Err(); err != nil {
		return domain.PreviewOutcome{}, err
	}
	dir, err := openDirectory(p.root, previewsDirectory, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return domain.PreviewOutcome{}, nil
	}
	if err != nil {
		return domain.PreviewOutcome{}, oops.With("operation", "storage.preview_outcome").Wrap(err)
	}
	defer unix.Close(dir)
	if made, err := madeAt(dir, key.Digest+copySuffix); err != nil {
		return domain.PreviewOutcome{}, err
	} else if !made.IsZero() && !made.Before(since) {
		return domain.PreviewOutcome{Copy: true}, nil
	}
	made, err := madeAt(dir, key.Digest+failureSuffix)
	if err != nil || made.IsZero() || made.Before(since) {
		return domain.PreviewOutcome{}, err
	}
	reason, err := readFailure(dir, key.Digest+failureSuffix)
	if err != nil {
		return domain.PreviewOutcome{}, err
	}
	return domain.PreviewOutcome{Failure: reason}, nil
}

// StorePreview keeps a rendering of the content. One content always renders
// the same, so an existing copy is kept; a lapsed one is replaced.
func (p *PersistentFiles) StorePreview(ctx context.Context, key domain.PreviewKey, rendering io.Reader) error {
	return p.keepPreview(ctx, key.Digest+copySuffix, rendering)
}

// RecordFailure keeps why the converter could not render the content.
func (p *PersistentFiles) RecordFailure(ctx context.Context, key domain.PreviewKey, reason domain.PreviewRefusal) error {
	name, known := failureNames[reason]
	if !known {
		return oops.Errorf("storage: %d is not a rendering failure", reason)
	}
	return p.keepPreview(ctx, key.Digest+failureSuffix, strings.NewReader(name))
}

// failureNames are the stored names of recorded failures, kept stable apart
// from the Domain enumeration's order.
var failureNames = map[domain.PreviewRefusal]string{
	domain.PreviewRefusalCorrupt:           "corrupt",
	domain.PreviewRefusalEncrypted:         "encrypted",
	domain.PreviewRefusalConverterRefused:  "converter_refused",
	domain.PreviewRefusalRenderingTooLarge: "rendering_too_large",
}

func (p *PersistentFiles) keepPreview(ctx context.Context, name string, content io.Reader) error {
	staged, err := p.stage(ctx, content, application.FileAttributes{})
	if err != nil {
		return err
	}
	pending, err := openDirectory(p.root, pendingDirectory, false, 0)
	if err != nil {
		p.discard(staged)
		return oops.With("operation", "storage.store_preview").Wrap(err)
	}
	defer unix.Close(pending)
	previews, err := openDirectory(p.root, previewsDirectory, true, directoryMode)
	if err != nil {
		p.discard(staged)
		return oops.With("operation", "storage.store_preview").Wrap(err)
	}
	defer unix.Close(previews)
	// A rename replaces a lapsed copy the sweep has not reached yet; a
	// concurrent rendering of the same content writes the same bytes.
	if err := unix.Renameat(pending, staged.Name, previews, name); err != nil {
		p.discard(staged)
		return oops.With("operation", "storage.store_preview").Wrap(err)
	}
	return nil
}

// OpenPreview opens the content's copy made since since.
func (p *PersistentFiles) OpenPreview(ctx context.Context, key domain.PreviewKey, since time.Time) (application.OpenedPreview, error) {
	if err := ctx.Err(); err != nil {
		return application.OpenedPreview{}, err
	}
	dir, err := openDirectory(p.root, previewsDirectory, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return application.OpenedPreview{}, application.ErrPreviewNotFound
	}
	if err != nil {
		return application.OpenedPreview{}, oops.With("operation", "storage.open_preview").Wrap(err)
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, key.Digest+copySuffix, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return application.OpenedPreview{}, application.ErrPreviewNotFound
	}
	if err != nil {
		return application.OpenedPreview{}, oops.With("operation", "storage.open_preview").Wrap(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return application.OpenedPreview{}, oops.With("operation", "storage.open_preview").Wrap(err)
	}
	made := time.Unix(stat.Mtim.Unix())
	if made.Before(since) {
		unix.Close(fd)
		return application.OpenedPreview{}, application.ErrPreviewNotFound
	}
	return application.OpenedPreview{Content: os.NewFile(uintptr(fd), key.Digest+copySuffix), SizeBytes: stat.Size, MadeAt: made}, nil
}

// SweepPreviews removes copies and failures made before before. It reads
// only the previews directory, so its cost follows the number of previews.
func (p *PersistentFiles) SweepPreviews(ctx context.Context, before time.Time) error {
	dir, err := openDirectory(p.root, previewsDirectory, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return oops.With("operation", "storage.sweep_previews").Wrap(err)
	}
	defer unix.Close(dir)
	names, err := directoryNames(dir)
	if err != nil {
		return oops.With("operation", "storage.sweep_previews").Wrap(err)
	}
	var failures []error
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		made, err := madeAt(dir, name)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if made.IsZero() || !made.Before(before) {
			continue
		}
		if err := unix.Unlinkat(dir, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			failures = append(failures, err)
		}
	}
	return oops.With("operation", "storage.sweep_previews").Wrap(errors.Join(failures...))
}

// madeAt is when the entry was made, or zero when it does not exist.
func madeAt(dir int, name string) (time.Time, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(dir, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, oops.With("operation", "storage.preview_stat").Wrap(err)
	}
	return time.Unix(stat.Mtim.Unix()), nil
}

func readFailure(dir int, name string) (domain.PreviewRefusal, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return domain.PreviewRefusalNone, oops.With("operation", "storage.preview_failure").Wrap(err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 64))
	if err != nil {
		return domain.PreviewRefusalNone, oops.With("operation", "storage.preview_failure").Wrap(err)
	}
	for reason, stored := range failureNames {
		if stored == strings.TrimSpace(string(content)) {
			return reason, nil
		}
	}
	// An unreadable record still says the content failed.
	return domain.PreviewRefusalConverterRefused, nil
}
