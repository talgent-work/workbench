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

// Reading a Reference Space's current content for the Workspace View, as a
// sandbox sees it at its mount: its visible entries, read in the content
// itself rather than through the space's links.

func (r *ReferenceSpaces) ListCurrent(ctx context.Context, owner, directory string, limit int) (application.WorkspaceListing, error) {
	content, err := r.openCurrent(ctx, owner)
	if errors.Is(err, errNoCurrentContent) && directory == "." {
		return application.WorkspaceListing{}, nil
	}
	if err != nil {
		return application.WorkspaceListing{}, err
	}
	defer unix.Close(content)
	var hidden func(string) bool
	if directory == "." {
		hidden = func(name string) bool { return strings.HasPrefix(name, ".") }
	}
	return listBeneath(content, directory, limit, hidden)
}

func (r *ReferenceSpaces) OpenCurrent(ctx context.Context, owner, file string) (application.OpenedWorkspaceFile, error) {
	content, err := r.openCurrent(ctx, owner)
	if err != nil {
		return application.OpenedWorkspaceFile{}, err
	}
	defer unix.Close(content)
	return openRegularBeneath(content, file)
}

func (r *ReferenceSpaces) InspectCurrent(ctx context.Context, owner, file string) (application.WorkspaceFileVersion, domain.ContentStructure, error) {
	opened, err := r.OpenCurrent(ctx, owner, file)
	if err != nil {
		return application.WorkspaceFileVersion{}, domain.ContentStructure{}, err
	}
	return inspectOpened(opened)
}

// errNoCurrentContent means the space has no content switched in yet.
var errNoCurrentContent = errors.New("storage: reference space has no current content")

// openCurrent opens the current content's directory. A content switched
// out between reading ".current" and opening it reads as no file.
func (r *ReferenceSpaces) openCurrent(ctx context.Context, owner string) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	content, err := r.current(owner)
	if err != nil {
		return -1, oops.With("operation", "reference.view").With("owner", owner).Wrap(err)
	}
	if content == "" {
		return -1, errors.Join(application.ErrFileNotFound, errNoCurrentContent)
	}
	fd, err := openDirectory(r.root, path.Join(referenceSpace(owner), referenceContents, content), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return -1, application.ErrFileNotFound
	}
	if err != nil {
		return -1, oops.With("operation", "reference.view").With("owner", owner).Wrap(err)
	}
	return fd, nil
}
