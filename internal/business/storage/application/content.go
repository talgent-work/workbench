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
// switch, on its Space Token, only while the content it expects is current,
// and returns the new content's revision. Repeating it without an
// expectation writes the same content again.
func (a *Application) ReplaceContent(ctx context.Context, named Named, token string, expected domain.ContentExpectation, files []ContentFile) (domain.ContentRevision, error) {
	id, err := named.ID()
	if err != nil {
		return "", err
	}
	seen := make(map[domain.ContentPath]bool, len(files))
	for _, file := range files {
		if _, err := domain.ParseContentPath(string(file.Path)); err != nil || seen[file.Path] {
			return "", domain.ErrInvalidContentPath
		}
		seen[file.Path] = true
	}
	directory, err := a.directories.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if err := directory.AdmitContentReplacement(token); err != nil {
		return "", err
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return "", err
	}
	var revision domain.ContentRevision
	err = a.mappedChange(ctx, backend, id, func(directory *domain.Directory) error {
		// Admitted again under the lock: a reclamation may have come between.
		if err := directory.AdmitContentReplacement(token); err != nil {
			return err
		}
		// The lock keeps the current content until the switch.
		current, err := backend.CurrentContent(ctx, id)
		if err != nil {
			return err
		}
		if err := expected.Admit(current); err != nil {
			return err
		}
		switched, err := backend.ReplaceContent(ctx, id, files, directory.Mappings)
		revision = switched
		return a.carriedOut(ctx, id, err)
	})
	if err != nil {
		return "", err
	}
	return revision, nil
}

// ReadContent reads files of a Directory's current content by clean relative
// path, every file when none is named, with the content's revision; before
// its first replacement, or before it is opened, it has none.
func (a *Application) ReadContent(ctx context.Context, named Named, paths []string) (Content, error) {
	parsed := make([]domain.ContentPath, 0, len(paths))
	for _, raw := range paths {
		path, err := domain.ParseContentPath(raw)
		if err != nil {
			return Content{}, err
		}
		parsed = append(parsed, path)
	}
	directory, backend, err := a.readableNamed(ctx, named)
	if errors.Is(err, domain.ErrDirectoryNotOpen) {
		return Content{}, nil
	}
	if err != nil {
		return Content{}, err
	}
	return backend.ReadContent(ctx, directory.ID, parsed)
}
