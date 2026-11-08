package infrastructure

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Reading a Directory as a sandbox mounting it sees it, for the Workspace
// View: a Directory replaced as a whole is read in its current content
// itself, showing only its visible entries, rather than through its links;
// any other is read in its folder.

// viewRoot opens where the Directory is read and tells whether hidden names
// at its root are left out. A replaced Directory before its first content,
// or one whose content was switched out meanwhile, has no files.
func (f *DirectoryFiles) viewRoot(ctx context.Context, id domain.DirectoryID) (int, bool, error) {
	if err := ctx.Err(); err != nil {
		return -1, false, err
	}
	operation := oops.With("operation", "storage.view", "directory", f.relative(id))
	folder, err := f.open(id)
	if errors.Is(err, unix.ENOENT) {
		return -1, false, application.ErrFileNotFound
	}
	if err != nil {
		return -1, false, operation.Wrap(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(folder, contentsFolder, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		return folder, false, nil
	}
	unix.Close(folder)
	content, err := f.current(id)
	if err != nil {
		return -1, false, operation.Wrap(err)
	}
	if content == "" {
		return -1, true, errNoCurrentContent
	}
	fd, err := openDirectory(f.layout.Root, path.Join(f.relative(id), contentsFolder, content), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return -1, true, application.ErrFileNotFound
	}
	if err != nil {
		return -1, true, operation.Wrap(err)
	}
	return fd, true, nil
}

// errNoCurrentContent means a Directory replaced as a whole has no content
// switched in yet.
var errNoCurrentContent = errors.Join(application.ErrFileNotFound, errors.New("storage: no content switched in yet"))

func (f *DirectoryFiles) ListEntries(ctx context.Context, id domain.DirectoryID, directory string, limit int) (application.WorkspaceListing, error) {
	root, replaced, err := f.viewRoot(ctx, id)
	if errors.Is(err, errNoCurrentContent) && directory == "." {
		return application.WorkspaceListing{}, nil
	}
	if err != nil {
		return application.WorkspaceListing{}, err
	}
	defer unix.Close(root)
	var hidden func(string) bool
	if replaced && directory == "." {
		hidden = func(name string) bool { return strings.HasPrefix(name, ".") }
	}
	listing, err := listBeneath(root, directory, limit, hidden)
	if err != nil || replaced || directory != "." {
		return listing, err
	}
	// A file Storage wrote directly into the Directory is read as that file.
	for i, entry := range listing.Entries {
		if entry.Kind != application.WorkspaceFile {
			continue
		}
		position, err := domain.NewFilePosition(id, entry.Name)
		if err != nil {
			continue
		}
		if stored, err := statAt(root, position); err == nil && stored.Attributes.Written() {
			listing.Entries[i].Position = &position
		}
	}
	return listing, nil
}

func (f *DirectoryFiles) OpenEntry(ctx context.Context, id domain.DirectoryID, file string) (application.OpenedWorkspaceFile, error) {
	root, replaced, err := f.viewRoot(ctx, id)
	if err != nil {
		return application.OpenedWorkspaceFile{}, err
	}
	defer unix.Close(root)
	if replaced && strings.HasPrefix(file, ".") {
		return application.OpenedWorkspaceFile{}, application.ErrInvalidWorkspacePath
	}
	return openRegularBeneath(root, file)
}

func (f *DirectoryFiles) InspectEntry(ctx context.Context, id domain.DirectoryID, file string) (application.WorkspaceFileVersion, domain.ContentStructure, error) {
	opened, err := f.OpenEntry(ctx, id, file)
	if err != nil {
		return application.WorkspaceFileVersion{}, domain.ContentStructure{}, err
	}
	return inspectOpened(opened)
}
