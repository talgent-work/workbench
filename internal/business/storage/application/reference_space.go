package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Reference Spaces: one owner's material for others to consult read-only,
// written only by the context that names the owner. A new content is staged
// aside and switched in at once, and earlier content is removed before the
// replacement completes; readers, including every sandbox that mounts the
// space, see only the current content.

// ReferenceFile is one file of a Reference Space's content.
type ReferenceFile struct {
	Path    domain.ReferencePath
	Content []byte
}

// ReferenceSpacesPort keeps Reference Spaces on persistent storage. Every
// method opens the owner's space when it does not exist yet.
type ReferenceSpacesPort interface {
	EnsureReferenceSpace(ctx context.Context, owner string) error
	// ReadCurrent reads the given files of the current content, every file
	// when none is given; a path the content lacks is left out.
	ReadCurrent(ctx context.Context, owner string, paths []domain.ReferencePath) ([]ReferenceFile, error)
	// Replace stages the files as the whole new content, switches it in in
	// one atomic step and removes every earlier content before returning.
	Replace(ctx context.Context, owner string, files []ReferenceFile) error
	// ListCurrent lists a directory of the current content as a Workspace
	// View shows it, "." for its root, which leaves out hidden names; the
	// root is empty before the first replacement.
	ListCurrent(ctx context.Context, owner, directory string, limit int) (WorkspaceListing, error)
	// OpenCurrent opens one regular file of the current content.
	OpenCurrent(ctx context.Context, owner, file string) (OpenedWorkspaceFile, error)
	// InspectCurrent reads the facts Preview Policy judges from one regular
	// file of the current content.
	InspectCurrent(ctx context.Context, owner, file string) (WorkspaceFileVersion, domain.ContentStructure, error)
}

// ReadReferenceFiles reads files of the owner's current content.
func (a *Application) ReadReferenceFiles(ctx context.Context, owner string, paths []string) ([]ReferenceFile, error) {
	owner, err := domain.ParseOwner(owner)
	if err != nil {
		return nil, err
	}
	parsed := make([]domain.ReferencePath, 0, len(paths))
	for _, raw := range paths {
		path, err := domain.ParseReferencePath(raw)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, path)
	}
	return a.references.ReadCurrent(ctx, owner, parsed)
}

// ReplaceReferenceContent makes the files the owner's whole content at once.
func (a *Application) ReplaceReferenceContent(ctx context.Context, owner string, files []ReferenceFile) error {
	owner, err := domain.ParseOwner(owner)
	if err != nil {
		return err
	}
	seen := make(map[domain.ReferencePath]bool, len(files))
	for _, file := range files {
		if _, err := domain.ParseReferencePath(string(file.Path)); err != nil || seen[file.Path] {
			return domain.ErrInvalidReferencePath
		}
		seen[file.Path] = true
	}
	return a.references.Replace(ctx, owner, files)
}
