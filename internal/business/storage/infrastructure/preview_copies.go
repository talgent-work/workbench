package infrastructure

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/application/query"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// PreviewCopies keeps Preview Copies in the JuiceFS preview area, outside
// every Directory, so no mount of a Directory shows them. Each content has
// <digest>.pdf, its rendering, or <digest>.failed, the reason the converter
// could not render it, both made when their outcome was settled; and
// <digest>.rendering while a rendering requested then has no outcome. A
// settled outcome outranks a rendering marker a lost race left beside it.
type PreviewCopies struct {
	root string
	// area is the preview area and pending its staging, relative to root.
	area, pending string
	now           func() time.Time
}

var (
	_ domain.PreviewCopyRepository     = (*PreviewCopies)(nil)
	_ query.PreviewCopyQueryRepository = (*PreviewCopies)(nil)
)

// NewPreviewCopies keeps Preview Copies in area under root, staging their
// renderings in pending.
func NewPreviewCopies(root, area, pending string) *PreviewCopies {
	return &PreviewCopies{root: root, area: area, pending: pending, now: time.Now}
}

const (
	renderedSuffix  = ".pdf"
	failedSuffix    = ".failed"
	renderingSuffix = ".rendering"
)

// failureNames are the stored names of recorded failures, kept stable apart
// from the Domain enumeration's order.
var failureNames = map[domain.PreviewRefusal]string{
	domain.PreviewRefusalCorrupt:           "corrupt",
	domain.PreviewRefusalEncrypted:         "encrypted",
	domain.PreviewRefusalConverterRefused:  "converter_refused",
	domain.PreviewRefusalRenderingTooLarge: "rendering_too_large",
}

func (p *PreviewCopies) Get(ctx context.Context, key domain.PreviewKey) (*domain.PreviewCopy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := openDirectory(p.root, p.area, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, domain.ErrPreviewCopyNotFound
	}
	if err != nil {
		return nil, oops.With("operation", "storage.preview_copy.get").Wrap(err)
	}
	defer unix.Close(dir)
	if made, err := madeAt(dir, key.Digest+renderedSuffix); err != nil || !made.IsZero() {
		return &domain.PreviewCopy{Key: key, State: domain.PreviewRendered, MadeAt: made}, err
	}
	if made, err := madeAt(dir, key.Digest+failedSuffix); err != nil || !made.IsZero() {
		if err != nil {
			return nil, err
		}
		reason, err := readFailure(dir, key.Digest+failedSuffix)
		return &domain.PreviewCopy{Key: key, State: domain.PreviewFailed, Failure: reason, MadeAt: made}, err
	}
	requested, err := madeAt(dir, key.Digest+renderingSuffix)
	if err != nil {
		return nil, err
	}
	if requested.IsZero() {
		return nil, domain.ErrPreviewCopyNotFound
	}
	return &domain.PreviewCopy{Key: key, State: domain.PreviewRendering, RequestedAt: requested}, nil
}

// Add keeps the rendering marker; only one opening creates it.
func (p *PreviewCopies) Add(ctx context.Context, requested *domain.PreviewCopy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := openDirectory(p.root, p.area, true, directoryMode)
	if err != nil {
		return oops.With("operation", "storage.preview_copy.add").Wrap(err)
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, requested.Key.Digest+renderingSuffix, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, fileMode)
	if errors.Is(err, unix.EEXIST) {
		return domain.ErrConcurrentModification
	}
	if err != nil {
		return oops.With("operation", "storage.preview_copy.add").Wrap(err)
	}
	return unix.Close(fd)
}

// Save keeps a settled outcome, then drops the rendering marker. One content
// always renders the same, so a concurrent rendering writes the same bytes.
func (p *PreviewCopies) Save(ctx context.Context, settled *domain.PreviewCopy, rendering io.Reader) error {
	var name string
	switch settled.State {
	case domain.PreviewRendered:
		if rendering == nil {
			return oops.Errorf("storage: a rendered Preview Copy needs its rendering")
		}
		name = settled.Key.Digest + renderedSuffix
	case domain.PreviewFailed:
		reason, known := failureNames[settled.Failure]
		if !known {
			return oops.Errorf("storage: %d is not a rendering failure", settled.Failure)
		}
		name, rendering = settled.Key.Digest+failedSuffix, strings.NewReader(reason)
	default:
		return oops.Errorf("storage: only a settled Preview Copy is saved")
	}
	staged, err := stage(ctx, p.root, p.pending, rendering, application.FileAttributes{}, p.now())
	if err != nil {
		return err
	}
	if err := p.commit(staged, name); err != nil {
		discard(p.root, p.pending, staged)
		return err
	}
	return p.unlink(settled.Key.Digest + renderingSuffix)
}

func (p *PreviewCopies) commit(staged, name string) error {
	pending, err := openDirectory(p.root, p.pending, false, 0)
	if err != nil {
		return oops.With("operation", "storage.preview_copy.save").Wrap(err)
	}
	defer unix.Close(pending)
	previews, err := openDirectory(p.root, p.area, true, directoryMode)
	if err != nil {
		return oops.With("operation", "storage.preview_copy.save").Wrap(err)
	}
	defer unix.Close(previews)
	return oops.With("operation", "storage.preview_copy.save").Wrap(unix.Renameat(pending, staged, previews, name))
}

func (p *PreviewCopies) Remove(ctx context.Context, key domain.PreviewKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var failures []error
	for _, suffix := range []string{renderedSuffix, failedSuffix, renderingSuffix} {
		failures = append(failures, p.unlink(key.Digest+suffix))
	}
	return errors.Join(failures...)
}

func (p *PreviewCopies) unlink(name string) error {
	dir, err := openDirectory(p.root, p.area, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return oops.With("operation", "storage.preview_copy.remove").Wrap(err)
	}
	defer unix.Close(dir)
	if err := unix.Unlinkat(dir, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return oops.With("operation", "storage.preview_copy.remove").Wrap(err)
	}
	return nil
}

func (p *PreviewCopies) OpenRendering(ctx context.Context, key domain.PreviewKey) (io.ReadSeekCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	dir, err := openDirectory(p.root, p.area, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, 0, application.ErrFileNotFound
	}
	if err != nil {
		return nil, 0, oops.With("operation", "storage.preview_copy.open").Wrap(err)
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, key.Digest+renderedSuffix, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, 0, application.ErrFileNotFound
	}
	if err != nil {
		return nil, 0, oops.With("operation", "storage.preview_copy.open").Wrap(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, 0, oops.With("operation", "storage.preview_copy.open").Wrap(err)
	}
	return os.NewFile(uintptr(fd), key.Digest+renderedSuffix), stat.Size, nil
}

// MadeBefore reads only the preview area, so its cost follows the number of
// Preview Copies.
func (p *PreviewCopies) MadeBefore(ctx context.Context, before time.Time) ([]domain.PreviewKey, error) {
	dir, err := openDirectory(p.root, p.area, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, oops.With("operation", "storage.preview_copies.made_before").Wrap(err)
	}
	defer unix.Close(dir)
	names, err := directoryNames(dir)
	if err != nil {
		return nil, oops.With("operation", "storage.preview_copies.made_before").Wrap(err)
	}
	seen := map[string]bool{}
	var keys []domain.PreviewKey
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, err := domain.NewPreviewKey(strings.TrimSuffix(name, path.Ext(name)))
		if err != nil || seen[key.Digest] {
			continue
		}
		made, err := madeAt(dir, name)
		if err != nil {
			return nil, err
		}
		if !made.IsZero() && made.Before(before) {
			seen[key.Digest] = true
			keys = append(keys, key)
		}
	}
	return keys, nil
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
