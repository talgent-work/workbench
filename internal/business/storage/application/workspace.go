package application

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Layout is a Work Directory as its sandbox sees it, as Agent names it: the
// Work Directory and the Directories mounted inside it.
type Layout struct {
	Workspace Named
	Mounts    []Mount
}

// Mount is a Directory mounted at Path inside a Work Directory.
type Mount struct {
	Path      string
	Directory Named
}

func (l Layout) layout() (domain.WorkspaceLayout, error) {
	workspace, err := l.Workspace.ID()
	if err != nil {
		return domain.WorkspaceLayout{}, err
	}
	mounts := make([]domain.MountedDirectory, 0, len(l.Mounts))
	for _, mount := range l.Mounts {
		id, err := mount.Directory.ID()
		if err != nil {
			return domain.WorkspaceLayout{}, err
		}
		mounts = append(mounts, domain.MountedDirectory{Path: domain.WorkspacePath(mount.Path), Directory: id})
	}
	return domain.NewWorkspaceLayout(workspace, mounts)
}

// WorkspaceView lists one directory of a Work Directory as its sandbox sees
// it: each Directory mounted inside it shows its visible files where Agent
// placed it, read as they are when opened, and a file Storage wrote carries
// its File position. A directory lists its first entries by name.
func (a *Application) WorkspaceView(ctx context.Context, workspace Layout, directory string) (WorkspaceListing, error) {
	layout, err := workspace.layout()
	if err != nil {
		return WorkspaceListing{}, err
	}
	clean, err := domain.ParseWorkspaceDirectory(directory)
	if err != nil {
		return WorkspaceListing{}, err
	}
	place := layout.Locate(clean)
	_, backend, err := a.readable(ctx, place.Directory)
	if errors.Is(err, domain.ErrDirectoryNotOpen) && place.Mounted && place.Path == "." {
		// A mounted Directory never opened has no files yet.
		return WorkspaceListing{}, nil
	}
	if err != nil {
		return WorkspaceListing{}, err
	}
	return backend.ListEntries(ctx, place.Directory, place.Path, a.workspaceListLimit)
}

// PreviewWorkspaceFile tells how a Session Workspace file is shown, for a
// reader Agent admitted, with a link for one inline read. Files a Harness
// wrote have no Preview Copies, so an office file among them is not
// previewed; a file Storage wrote into a mounted Directory is previewed as
// that file.
func (a *Application) PreviewWorkspaceFile(ctx context.Context, workspace Layout, file string) (FilePreview, error) {
	place, backend, err := a.workspaceFile(ctx, workspace, file)
	if err != nil {
		return FilePreview{}, err
	}
	if position, ok := a.writtenPosition(ctx, backend, place); ok {
		opened, err := a.inspectForPreview(ctx, backend, position)
		if err != nil {
			return FilePreview{}, err
		}
		return a.filePreview(ctx, opened)
	}
	inspected, structure, err := backend.InspectEntry(ctx, place.Directory, place.Path)
	if err != nil {
		return FilePreview{}, err
	}
	verdict := domain.AdmitForPreview(domain.PreviewSubject{
		Source: domain.PreviewOfWorkspaceFile, Name: path.Base(place.Path), SizeBytes: inspected.SizeBytes, Structure: structure,
	}, a.preview.Limits)
	preview := FilePreview{Verdict: verdict}
	if verdict.Readable() {
		// The read is bound to what was judged: a file changed since is
		// refused, and the reader's next refresh judges it again.
		link := a.accessLink(AccessGrant{Directory: place.Directory, Path: place.Path, Workspace: true, WorkspaceVersion: &inspected})
		preview.Link = &link
	}
	return preview, nil
}

// writtenPosition is the File position of a file Storage wrote directly into
// a mounted Directory, read and previewed as that file.
func (a *Application) writtenPosition(ctx context.Context, backend DirectoryBackendPort, place domain.Place) (domain.FilePosition, bool) {
	if !place.Mounted || strings.Contains(place.Path, "/") {
		return domain.FilePosition{}, false
	}
	position, err := domain.NewFilePosition(place.Directory, place.Path)
	if err != nil {
		return domain.FilePosition{}, false
	}
	stored, err := backend.Stat(ctx, position)
	if err != nil || !stored.Attributes.Written() {
		return domain.FilePosition{}, false
	}
	return position, true
}

// JudgedPreview is how a file Storage does not hold is shown, and the most
// bytes its reader is served.
type JudgedPreview struct {
	Verdict   domain.PreviewVerdict
	ReadLimit int64
}

// JudgeFilePreview tells how a workspace file Storage does not hold is shown,
// judged from its name, size and leading bytes as a Session Workspace file
// is, under Storage's preview limits. Nothing is kept or linked.
func (a *Application) JudgeFilePreview(name string, sizeBytes int64, head []byte) (JudgedPreview, error) {
	if strings.TrimSpace(name) == "" || sizeBytes < 0 || len(head) > domain.TextProbeBytes {
		return JudgedPreview{}, ErrInvalidRequest
	}
	verdict := domain.AdmitForPreview(domain.PreviewSubject{
		Source: domain.PreviewOfWorkspaceFile, Name: path.Base(name), SizeBytes: sizeBytes,
		Structure: a.inspection.InspectHead(head),
	}, a.preview.Limits)
	return JudgedPreview{Verdict: verdict, ReadLimit: verdict.ReadLimit(a.preview.Limits)}, nil
}

// IssueWorkspaceFileLink issues a link to download one Session Workspace
// file, for a reader Agent admitted. A download is the original as it is
// when read.
func (a *Application) IssueWorkspaceFileLink(ctx context.Context, workspace Layout, file string) (Link, error) {
	place, backend, err := a.workspaceFile(ctx, workspace, file)
	if err != nil {
		return Link{}, err
	}
	opened, err := backend.OpenEntry(ctx, place.Directory, place.Path)
	if err != nil {
		return Link{}, err
	}
	opened.Content.Close()
	return a.accessLink(AccessGrant{Directory: place.Directory, Path: place.Path, Workspace: true, Download: true}), nil
}

// workspaceFile places a file a reader opens in a Workspace View in the
// Directory it is read in.
func (a *Application) workspaceFile(ctx context.Context, workspace Layout, file string) (domain.Place, DirectoryBackendPort, error) {
	layout, err := workspace.layout()
	if err != nil {
		return domain.Place{}, nil, err
	}
	clean, err := domain.ParseWorkspaceFile(file)
	if err != nil {
		return domain.Place{}, nil, err
	}
	place := layout.Locate(clean)
	if place.Path == "." {
		// A mount point is a directory, never a file.
		return domain.Place{}, nil, ErrInvalidWorkspacePath
	}
	_, backend, err := a.readable(ctx, place.Directory)
	if errors.Is(err, domain.ErrDirectoryNotOpen) && place.Mounted {
		return domain.Place{}, nil, ErrFileNotFound
	}
	return place, backend, err
}

// readWorkspaceFile opens the workspace file a link names, under its own
// name; its type follows the name.
func (a *Application) readWorkspaceFile(ctx context.Context, backend DirectoryBackendPort, grant AccessGrant) (OpenedFile, error) {
	opened, err := backend.OpenEntry(ctx, grant.Directory, grant.Path)
	if err != nil {
		return OpenedFile{}, err
	}
	if version := grant.WorkspaceVersion; version != nil && (opened.SizeBytes != version.SizeBytes || !opened.ModifiedAt.Equal(version.ModifiedAt)) {
		opened.Content.Close()
		return OpenedFile{}, ErrContentChanged
	}
	return OpenedFile{Content: opened.Content, File: StoredFile{
		Position: domain.FilePosition{Directory: grant.Directory, Name: path.Base(grant.Path)}, SizeBytes: opened.SizeBytes, ModifiedAt: opened.ModifiedAt,
	}}, nil
}
