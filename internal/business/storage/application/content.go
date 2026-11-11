package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// A Directory whose content its owner's context replaces as a whole for
// others to consult: a new content is staged aside and switched in at once,
// and earlier content is removed before the replacement completes; readers,
// including every sandbox that mounts it, see only the current content.

// ReplaceContent makes the files the Directory's whole content in one atomic
// switch, on its Space Token. Repeating it writes the same content again.
func (a *Application) ReplaceContent(ctx context.Context, named Named, token string, files []ContentFile) error {
	id, err := named.ID()
	if err != nil {
		return err
	}
	seen := make(map[domain.ContentPath]bool, len(files))
	for _, file := range files {
		if _, err := domain.ParseContentPath(string(file.Path)); err != nil || seen[file.Path] {
			return domain.ErrInvalidContentPath
		}
		seen[file.Path] = true
	}
	directory, err := a.directories.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := directory.AdmitContentReplacement(token); err != nil {
		return err
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return err
	}
	return a.mappedChange(ctx, backend, id, func(directory *domain.Directory) error {
		// Admitted again under the lock: a reclamation may have come between.
		if err := directory.AdmitContentReplacement(token); err != nil {
			return err
		}
		return a.carriedOut(ctx, id, backend.ReplaceContent(ctx, id, files, directory.Mappings))
	})
}

// ReadContent reads files of a Directory's current content by clean relative
// path, every file when none is named; before its first replacement, or
// before it is opened, it has none.
func (a *Application) ReadContent(ctx context.Context, named Named, paths []string) ([]ContentFile, error) {
	parsed := make([]domain.ContentPath, 0, len(paths))
	for _, raw := range paths {
		path, err := domain.ParseContentPath(raw)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, path)
	}
	directory, backend, err := a.readableNamed(ctx, named)
	if errors.Is(err, domain.ErrDirectoryNotOpen) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return backend.ReadContent(ctx, directory.ID, parsed)
}
