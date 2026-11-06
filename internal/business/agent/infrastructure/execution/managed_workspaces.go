package execution

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// WorkspaceViews reads a Work's workspace where its files are, as Environment
// starts it where it runs: a Work whose environment the Managed realization
// opened has its files in its directory on its machine, read through the
// relay while the machine is reachable; any other Work's workspace is
// Storage's. A Work's realization is fixed by its first start, so the
// allocation's recorded realization decides, never a guess.
type WorkspaceViews struct {
	cloud   application.WorkspaceViewPort
	managed *ManagedWorkspaces
}

var (
	_ application.WorkspaceViewPort     = (*WorkspaceViews)(nil)
	_ application.WorkspaceFileLinkPort = (*ManagedWorkspaces)(nil)
)

// NewWorkspaceViews reads Cloud Works through cloud and, when the deployment
// reaches Managed Computers, Managed Works through managed.
func NewWorkspaceViews(cloud application.WorkspaceViewPort, managed *ManagedWorkspaces) (*WorkspaceViews, error) {
	if cloud == nil {
		return nil, errors.New("agent: Storage's workspace views are required")
	}
	return &WorkspaceViews{cloud: cloud, managed: managed}, nil
}

func (v *WorkspaceViews) WorkspaceView(ctx context.Context, workspace application.ViewedWorkspace, dir string) (application.WorkspaceListing, error) {
	return readWhere(ctx, v, workspace, func(spec managedSpecification) (application.WorkspaceListing, error) {
		return v.managed.view(ctx, spec, workspace.WorkID, dir)
	}, func() (application.WorkspaceListing, error) { return v.cloud.WorkspaceView(ctx, workspace, dir) })
}

func (v *WorkspaceViews) PreviewWorkspaceFile(ctx context.Context, workspace application.ViewedWorkspace, file string) (application.WorkspaceFilePreview, error) {
	return readWhere(ctx, v, workspace, func(spec managedSpecification) (application.WorkspaceFilePreview, error) {
		return v.managed.preview(ctx, spec, workspace.WorkID, file)
	}, func() (application.WorkspaceFilePreview, error) {
		return v.cloud.PreviewWorkspaceFile(ctx, workspace, file)
	})
}

func (v *WorkspaceViews) WorkspaceFileLink(ctx context.Context, workspace application.ViewedWorkspace, file string) (application.WorkspaceFileLink, error) {
	return readWhere(ctx, v, workspace, func(spec managedSpecification) (application.WorkspaceFileLink, error) {
		return v.managed.downloadLink(ctx, spec, workspace.WorkID, file)
	}, func() (application.WorkspaceFileLink, error) { return v.cloud.WorkspaceFileLink(ctx, workspace, file) })
}

// readWhere reads the Work's workspace on its machine when the Managed
// realization opened its environment, and its Session Workspace in Storage
// otherwise. Any other Work, such as a project-scoped definition's or a Managed Work
// whose directory was never opened, has nothing to read yet.
func readWhere[T any](ctx context.Context, v *WorkspaceViews, workspace application.ViewedWorkspace, managed func(managedSpecification) (T, error), cloud func() (T, error)) (T, error) {
	var zero T
	if v.managed != nil {
		spec, opened, err := v.managed.opened(ctx, workspace.WorkID)
		if err != nil {
			return zero, err
		}
		if opened {
			return managed(spec)
		}
	}
	if workspace.Reference == "" {
		return zero, application.ErrWorkspaceNotCreated
	}
	return cloud()
}

// ManagedWorkspaces reads Managed Works' directories through the relay and
// serves their files behind Agent's own links.
type ManagedWorkspaces struct {
	db       *xorm.Engine
	machines *ManagedComputers
	links    *WorkspaceFileLinks
	previews PreviewJudge
}

// PreviewJudge is Storage's Preview Policy for a workspace file it does not
// hold: how the file is shown, judged from its name, size and leading bytes,
// and the most bytes its reader is served.
type PreviewJudge interface {
	JudgeFilePreview(ctx context.Context, name string, size int64, head []byte) (application.WorkspaceFilePreview, int64, error)
}

// previewHeadBytes is how much of a file Storage judges, its text probe.
const previewHeadBytes = 8 << 10

func NewManagedWorkspaces(db *xorm.Engine, machines *ManagedComputers, links *WorkspaceFileLinks, previews PreviewJudge) (*ManagedWorkspaces, error) {
	if db == nil || machines == nil || links == nil || previews == nil {
		return nil, errors.New("agent: allocations, Managed Computers, file links and Storage's preview judgement are required for Managed workspaces")
	}
	return &ManagedWorkspaces{db: db, machines: machines, links: links, previews: previews}, nil
}

// maxWorkspaceListEntries bounds one directory's listing, as Storage's
// workspace-list-entries bounds a Cloud Work's.
const maxWorkspaceListEntries = 1000

// inputsDirectory is where a Work's Attachments are, in either realization.
const inputsDirectory = "inputs"

// opened finds the environment the Managed realization last opened for the
// Work: the machine holds the token of the latest one, retired or not, for
// the Work's life. A Work never opened there is not Managed here.
func (w *ManagedWorkspaces) opened(ctx context.Context, workID string) (managedSpecification, bool, error) {
	var record allocationRecord
	found, err := w.db.Context(ctx).Where("session_id = ? AND realization = ? AND sandbox_id <> ''", workID, realizationManaged).
		Desc("created_at").Desc("id").Cols("id", "specification_json").Get(&record)
	if err != nil {
		return managedSpecification{}, false, oops.With("operation", "find_managed_workspace").Wrap(err)
	}
	if !found {
		return managedSpecification{}, false, nil
	}
	spec, err := decodeManagedSpecification(record)
	return spec, err == nil, err
}

// view lists one directory of the Work's directory. An entry under inputs/
// is the input source's Attachment of its name, read as that Attachment.
func (w *ManagedWorkspaces) view(ctx context.Context, spec managedSpecification, workID, dir string) (application.WorkspaceListing, error) {
	clean, err := workspaceDirectory(dir)
	if err != nil {
		return application.WorkspaceListing{}, err
	}
	entries, err := w.machines.listFiles(ctx, spec, workID, clean)
	if clean == "" && errors.Is(err, errWorkFileNotFound) {
		// The machine does not know the Work: it never opened its directory.
		return application.WorkspaceListing{}, application.ErrWorkspaceNotCreated
	}
	if err != nil {
		return application.WorkspaceListing{}, workspaceReadError(err)
	}
	listing := application.WorkspaceListing{Total: len(entries), Entries: make([]application.WorkspaceEntry, 0, min(len(entries), maxWorkspaceListEntries))}
	for _, entry := range entries[:min(len(entries), maxWorkspaceListEntries)] {
		kind := application.WorkspaceFile
		if entry.Kind == machineDirectory {
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

// preview has Storage judge one file as it judges a Cloud Work's, from its
// size and the start of its content, and links a readable one for inline
// reads of at most the bytes Storage allows.
func (w *ManagedWorkspaces) preview(ctx context.Context, spec managedSpecification, workID, file string) (application.WorkspaceFilePreview, error) {
	clean, entry, err := w.file(ctx, spec, workID, file)
	if err != nil {
		return application.WorkspaceFilePreview{}, err
	}
	content, err := w.machines.openFile(ctx, spec, workID, clean)
	if err != nil {
		return application.WorkspaceFilePreview{}, workspaceReadError(err)
	}
	head, err := io.ReadAll(io.LimitReader(content, previewHeadBytes))
	content.Close()
	if err != nil {
		return application.WorkspaceFilePreview{}, workspaceReadError(oops.With("operation", "read_work_file").Wrap(err))
	}
	preview, served, err := w.previews.JudgeFilePreview(ctx, entry.Name, entry.Size, head)
	if err != nil {
		return application.WorkspaceFilePreview{}, err
	}
	if served > 0 {
		url, expiresAt := w.links.issue(workspaceFileGrant{WorkID: workID, Path: clean, Limit: served})
		preview.Link = &application.WorkspaceFileLink{URL: url, ExpiresAt: expiresAt}
	}
	return preview, nil
}

// downloadLink links one existing file for download under its name.
func (w *ManagedWorkspaces) downloadLink(ctx context.Context, spec managedSpecification, workID, file string) (application.WorkspaceFileLink, error) {
	clean, _, err := w.file(ctx, spec, workID, file)
	if err != nil {
		return application.WorkspaceFileLink{}, err
	}
	url, expiresAt := w.links.issue(workspaceFileGrant{WorkID: workID, Path: clean, Download: true})
	return application.WorkspaceFileLink{URL: url, ExpiresAt: expiresAt}, nil
}

// file describes a file a reader opens outside inputs/, whose Attachments
// are read as themselves.
func (w *ManagedWorkspaces) file(ctx context.Context, spec managedSpecification, workID, file string) (string, machineFileEntry, error) {
	clean, err := workspaceFile(file)
	if err != nil {
		return "", machineFileEntry{}, err
	}
	entry, err := w.machines.statFile(ctx, spec, workID, clean)
	if err != nil {
		return "", machineFileEntry{}, workspaceReadError(err)
	}
	if entry.Kind != machineFile {
		return "", machineFileEntry{}, application.ErrInvalidWorkspacePath
	}
	return clean, entry, nil
}

// LinkedWork is the Work a link grants a read of.
func (w *ManagedWorkspaces) LinkedWork(token string) (string, error) {
	grant, err := w.links.verify(token)
	return grant.WorkID, err
}

// OpenLink opens the file a link grants. ErrWorkspaceUnavailable means the
// machine is not reachable now.
func (w *ManagedWorkspaces) OpenLink(ctx context.Context, token string) (application.OpenedWorkspaceFile, error) {
	grant, err := w.links.verify(token)
	if err != nil {
		return application.OpenedWorkspaceFile{}, err
	}
	spec, found, err := w.opened(ctx, grant.WorkID)
	if err != nil {
		return application.OpenedWorkspaceFile{}, err
	}
	if !found {
		return application.OpenedWorkspaceFile{}, application.ErrWorkspacePathNotFound
	}
	content, err := w.machines.openFile(ctx, spec, grant.WorkID, grant.Path)
	if err != nil {
		return application.OpenedWorkspaceFile{}, workspaceReadError(err)
	}
	return application.OpenedWorkspaceFile{Name: path.Base(grant.Path), Download: grant.Download, Limit: grant.Limit, Content: content}, nil
}

// workspaceReadError names what reading the machine found as a workspace
// outcome; a machine the relay does not reach is a Disconnection.
func workspaceReadError(err error) error {
	switch {
	case errors.Is(err, errMachineUnreachable):
		return application.ErrWorkspaceUnavailable
	case errors.Is(err, errWorkFileNotFound):
		return application.ErrWorkspacePathNotFound
	case errors.Is(err, errWorkFileNotRegular):
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
