package workspacereading

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
)

// managedFiles reads a Managed Work's directory on its machine.
// domain.ErrWorkspaceUnavailable means the machine is not reachable now.
type managedFiles interface {
	List(ctx context.Context, location domain.WorkspaceLocation, dir string) ([]execution.WorkFile, error)
	Stat(ctx context.Context, location domain.WorkspaceLocation, path string) (execution.WorkFile, error)
	Open(ctx context.Context, location domain.WorkspaceLocation, path string) (io.ReadCloser, error)
}

// managedWorkspaces reads Managed Works' directories and serves their files
// behind Agent's own links; Storage judges their previews as it judges a
// Cloud Work's.
type managedWorkspaces struct {
	files    managedFiles
	links    *application.WorkspaceFileSigner
	previews StorageViews
}

// previewHeadBytes is how much of a file Storage judges, its text probe.
const previewHeadBytes = 8 << 10

// maxWorkspaceListEntries bounds one directory's listing, as Storage's
// workspace-list-entries bounds a Cloud Work's.
const maxWorkspaceListEntries = 1000

// inputsDirectory is where a Work's Attachments are, in either realization.
const inputsDirectory = "inputs"

// WorkspaceView lists one directory of the Work's directory. An entry under
// inputs/ is the input source's Attachment of its name, read as that
// Attachment.
func (w *managedWorkspaces) WorkspaceView(ctx context.Context, location domain.WorkspaceLocation, dir string) (application.WorkspaceListing, error) {
	clean, err := workspaceDirectory(dir)
	if err != nil {
		return application.WorkspaceListing{}, err
	}
	entries, err := w.files.List(ctx, location, clean)
	if clean == "" && errors.Is(err, execution.ErrWorkFileNotFound) {
		// The machine does not know the Work: it never opened its directory.
		return application.WorkspaceListing{}, domain.ErrWorkspaceNotCreated
	}
	if err != nil {
		return application.WorkspaceListing{}, readError(err)
	}
	listing := application.WorkspaceListing{Total: len(entries), Entries: make([]application.WorkspaceEntry, 0, min(len(entries), maxWorkspaceListEntries))}
	for _, entry := range entries[:min(len(entries), maxWorkspaceListEntries)] {
		kind := application.WorkspaceFile
		if entry.Directory {
			kind = application.WorkspaceDirectory
		}
		mapped := application.WorkspaceEntry{Name: entry.Name, Kind: kind, SizeBytes: entry.Size, ModifiedAt: entry.ModifiedAt}
		if clean == inputsDirectory && kind == application.WorkspaceFile {
			mapped.AttachmentName = entry.Name
		}
		listing.Entries = append(listing.Entries, mapped)
	}
	return listing, nil
}

// PreviewWorkspaceFile has Storage judge one file as it judges a Cloud
// Work's, from its size and the start of its content, and links a readable
// one for inline reads of at most the bytes Storage allows.
func (w *managedWorkspaces) PreviewWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, file string) (application.WorkspaceFilePreview, error) {
	clean, entry, err := w.file(ctx, location, file)
	if err != nil {
		return application.WorkspaceFilePreview{}, err
	}
	content, err := w.files.Open(ctx, location, clean)
	if err != nil {
		return application.WorkspaceFilePreview{}, readError(err)
	}
	head, err := io.ReadAll(io.LimitReader(content, previewHeadBytes))
	content.Close()
	if err != nil {
		return application.WorkspaceFilePreview{}, readError(oops.With("operation", "read_work_file").Wrap(err))
	}
	preview, served, err := w.previews.JudgeFilePreview(ctx, entry.Name, entry.Size, head)
	if err != nil {
		return application.WorkspaceFilePreview{}, err
	}
	if served > 0 {
		link := w.links.Issue(application.WorkspaceFileGrant{WorkID: location.WorkID, Path: clean, Limit: served})
		preview.Link = &link
	}
	return preview, nil
}

// WorkspaceFileLink links one existing file for download under its name.
func (w *managedWorkspaces) WorkspaceFileLink(ctx context.Context, location domain.WorkspaceLocation, file string) (application.WorkspaceFileLink, error) {
	clean, _, err := w.file(ctx, location, file)
	if err != nil {
		return application.WorkspaceFileLink{}, err
	}
	return w.links.Issue(application.WorkspaceFileGrant{WorkID: location.WorkID, Path: clean, Download: true}), nil
}

// file describes a file a reader opens outside inputs/, whose Attachments
// are read as themselves.
func (w *managedWorkspaces) file(ctx context.Context, location domain.WorkspaceLocation, file string) (string, execution.WorkFile, error) {
	clean, err := workspaceFile(file)
	if err != nil {
		return "", execution.WorkFile{}, err
	}
	entry, err := w.files.Stat(ctx, location, clean)
	if err != nil {
		return "", execution.WorkFile{}, readError(err)
	}
	if entry.Directory {
		return "", execution.WorkFile{}, application.ErrInvalidWorkspacePath
	}
	return clean, entry, nil
}

// OpenWorkspaceFile opens the file a verified link grants.
func (w *managedWorkspaces) OpenWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, grant application.WorkspaceFileGrant) (application.OpenedWorkspaceFile, error) {
	content, err := w.files.Open(ctx, location, grant.Path)
	if err != nil {
		return application.OpenedWorkspaceFile{}, readError(err)
	}
	return application.OpenedWorkspaceFile{Name: path.Base(grant.Path), Download: grant.Download, Limit: grant.Limit, Content: content}, nil
}

// readError names what reading the machine found as a workspace outcome.
func readError(err error) error {
	switch {
	case errors.Is(err, execution.ErrWorkFileNotFound):
		return application.ErrWorkspacePathNotFound
	case errors.Is(err, execution.ErrWorkFileNotRegular):
		return application.ErrInvalidWorkspacePath
	}
	return err
}

// workspaceDirectory is a directory relative to the Work root, "" for the
// root, as Storage accepts a Cloud Work's.
func workspaceDirectory(raw string) (string, error) {
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", nil
	}
	return relativeWorkspacePath(raw)
}

// workspaceFile is a file relative to the Work root outside inputs/.
func workspaceFile(raw string) (string, error) {
	clean, err := relativeWorkspacePath(strings.TrimPrefix(raw, "/"))
	if err != nil || clean == "." || clean == inputsDirectory || strings.HasPrefix(clean, inputsDirectory+"/") {
		return "", application.ErrInvalidWorkspacePath
	}
	return clean, nil
}

func relativeWorkspacePath(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\\\x00") {
		return "", application.ErrInvalidWorkspacePath
	}
	clean := path.Clean(raw)
	if clean != raw || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", application.ErrInvalidWorkspacePath
	}
	return clean, nil
}
