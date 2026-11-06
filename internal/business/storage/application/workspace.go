package application

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// MountRequest names a Work's spaces by business identity.
type MountRequest struct {
	HomeOwner          string
	WorkID             string
	WorkspaceReference string
	InputSource        string
	SpaceToken         string
	// References are the Reference Spaces the Work's sandbox mounts
	// read-only, each at the name its caller gave.
	References []domain.ReferenceMount
}

// MountedReference is the grant of one Reference Space mount.
type MountedReference struct {
	Name  string
	Grant MountGrant
}

// Mounts are the Mount Grants of one Work.
type Mounts struct {
	Home MountGrant
	// Workspace is absent for a Work without a Session Workspace, such as
	// the Work of a project-scoped Agent Definition.
	Workspace *MountGrant
	// Inputs is absent when the Work has no input source.
	Inputs *MountGrant
	// References follow the requested mounts' order.
	References []MountedReference
}

// AcquireMounts returns the HOME, the Session Workspace, the input source and
// the Reference Spaces of a Work, creating each on its first request: a HOME
// and a workspace read-write, the input source's Attachments and each
// Reference Space's current content read-only. A Work named without a
// workspace reference has no Session Workspace.
func (a *Application) AcquireMounts(ctx context.Context, request MountRequest) (Mounts, error) {
	if strings.TrimSpace(request.WorkID) == "" {
		return Mounts{}, ErrInvalidRequest
	}
	owner, err := domain.ParseHomeOwner(request.HomeOwner)
	if err != nil {
		return Mounts{}, err
	}
	references, err := domain.NewReferenceMounts(request.References)
	if err != nil {
		return Mounts{}, err
	}
	var mounts Mounts
	if request.InputSource != "" {
		space, err := a.inputSpace(ctx, request.InputSource, request.SpaceToken)
		if err != nil {
			return Mounts{}, err
		}
		if err := a.persistent.EnsureSpace(ctx, space); err != nil {
			return Mounts{}, err
		}
		inputs := a.mounts.Attachments(space)
		mounts.Inputs = &inputs
	}
	names := make([]string, 0, len(references))
	for _, reference := range references {
		if err := a.references.EnsureReferenceSpace(ctx, reference.Owner); err != nil {
			return Mounts{}, err
		}
		mounts.References = append(mounts.References, MountedReference{Name: reference.Name, Grant: a.mounts.Reference(reference.Owner)})
		names = append(names, reference.Name)
	}
	home, err := a.working.PrepareHome(ctx, owner.Key())
	if err != nil {
		return Mounts{}, err
	}
	mounts.Home = a.mounts.Working(home)
	if strings.TrimSpace(request.WorkspaceReference) == "" {
		return mounts, nil
	}
	workspace, err := a.working.PrepareWorkspace(ctx, request.WorkspaceReference, names)
	if err != nil {
		return Mounts{}, err
	}
	grant := a.mounts.Working(workspace)
	mounts.Workspace = &grant
	return mounts, nil
}

// inputSpace resolves an input source this Storage issued to its File
// Space's owner, admitted on the Space Token supplied with it.
func (a *Application) inputSpace(ctx context.Context, source, token string) (string, error) {
	owner, err := domain.OwnerOfInputSource(source)
	if err != nil {
		return "", err
	}
	space, err := a.spaces.Get(ctx, owner)
	if err != nil {
		return "", err
	}
	if err := space.AdmitSpaceToken(token); err != nil {
		return "", err
	}
	return space.Owner, nil
}

// ViewedWorkspace names a Work's Session Workspace and the spaces its
// sandbox mounts inside it: the input source's Attachments at inputs/ and
// each Reference Space at the name its caller gave.
type ViewedWorkspace struct {
	Reference   string
	InputSource string
	References  []domain.ReferenceMount
}

// WorkspaceView lists one directory of a Work's workspace as its sandbox
// sees it: inputs/ shows the input source's current Attachments, each by its
// File position, and each mount its Reference Space's current content. A
// directory lists its first entries by name.
func (a *Application) WorkspaceView(ctx context.Context, workspace ViewedWorkspace, directory string) (WorkspaceListing, error) {
	clean, err := domain.ParseWorkspaceDirectory(directory)
	if err != nil {
		return WorkspaceListing{}, err
	}
	references, err := domain.NewReferenceMounts(workspace.References)
	if err != nil {
		return WorkspaceListing{}, err
	}
	mount, entry, mounted, err := clean.InMount(references)
	switch {
	case err != nil:
		return WorkspaceListing{}, err
	case mounted:
		return a.references.ListCurrent(ctx, mount.Owner, entry, a.workspaceListLimit)
	case clean.UnderInputs():
		// Attachments are single files; inputs/ has no subdirectory.
		return WorkspaceListing{}, ErrInvalidWorkspacePath
	case clean != domain.InputsDirectory:
		return a.working.List(ctx, workspace.Reference, clean, a.workspaceListLimit)
	}
	if workspace.InputSource == "" {
		return WorkspaceListing{}, nil
	}
	owner, err := domain.OwnerOfInputSource(workspace.InputSource)
	if err != nil {
		return WorkspaceListing{}, err
	}
	files, err := a.ListFiles(ctx, owner)
	if err != nil {
		return WorkspaceListing{}, err
	}
	var listing WorkspaceListing
	slices.SortFunc(files, func(a, b SpaceFile) int { return strings.Compare(a.Position.Name, b.Position.Name) })
	for _, file := range files {
		if file.Position.Kind != domain.FileKindAttachment {
			continue
		}
		listing.Total++
		if len(listing.Entries) < a.workspaceListLimit {
			position := file.Position
			listing.Entries = append(listing.Entries, WorkspaceEntry{Name: position.Name, Kind: WorkspaceFile, SizeBytes: file.SizeBytes, ModifiedAt: file.ModifiedAt, Attachment: &position})
		}
	}
	return listing, nil
}

// PreviewWorkspaceFile tells how a Session Workspace file is shown, for a
// reader Agent admitted, with a link for one inline read. Workspace files
// have no Preview Copies, so an office file is not previewed.
func (a *Application) PreviewWorkspaceFile(ctx context.Context, workspace ViewedWorkspace, file string) (FilePreview, error) {
	grant, err := workspaceGrant(workspace, file)
	if err != nil {
		return FilePreview{}, err
	}
	var inspected WorkspaceFileVersion
	var structure domain.ContentStructure
	if grant.ReferenceOwner != "" {
		inspected, structure, err = a.references.InspectCurrent(ctx, grant.ReferenceOwner, grant.ReferenceEntry)
	} else {
		inspected, structure, err = a.working.InspectFile(ctx, workspace.Reference, grant.WorkspacePath)
	}
	if err != nil {
		return FilePreview{}, err
	}
	verdict := domain.AdmitForPreview(domain.PreviewSubject{
		Source: domain.PreviewOfWorkspaceFile, Name: path.Base(string(grant.WorkspacePath)), SizeBytes: inspected.SizeBytes, Structure: structure,
	}, a.preview.Limits)
	preview := FilePreview{Verdict: verdict}
	if verdict.Readable() {
		// The read is bound to what was judged: a file changed since is
		// refused, and the reader's next refresh judges it again.
		grant.WorkspaceVersion = &inspected
		link := a.workspaceLink(grant)
		preview.Link = &link
	}
	return preview, nil
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
		Structure: a.inspector.InspectHead(head),
	}, a.preview.Limits)
	return JudgedPreview{Verdict: verdict, ReadLimit: verdict.ReadLimit(a.preview.Limits)}, nil
}

// IssueWorkspaceFileLink issues a link to download one Session Workspace
// file, for a reader Agent admitted.
func (a *Application) IssueWorkspaceFileLink(ctx context.Context, workspace ViewedWorkspace, file string) (Link, error) {
	grant, err := workspaceGrant(workspace, file)
	if err != nil {
		return Link{}, err
	}
	opened, err := a.openWorkspaceFile(ctx, grant)
	if err != nil {
		return Link{}, err
	}
	opened.Content.Close()
	// A download is the original as it is when read.
	grant.Download = true
	return a.workspaceLink(grant), nil
}

// workspaceGrant reads a file a reader opens in a Workspace View: a file
// under a Reference Space mount is that space's current content, any other
// the workspace's.
func workspaceGrant(workspace ViewedWorkspace, file string) (AccessGrant, error) {
	clean, err := domain.ParseWorkspaceFile(file)
	if err != nil {
		return AccessGrant{}, err
	}
	references, err := domain.NewReferenceMounts(workspace.References)
	if err != nil {
		return AccessGrant{}, err
	}
	grant := AccessGrant{Workspace: workspace.Reference, WorkspacePath: clean}
	mount, entry, mounted, err := clean.InMount(references)
	switch {
	case err != nil:
		return AccessGrant{}, err
	case !mounted:
		return grant, nil
	case entry == ".":
		return AccessGrant{}, ErrInvalidWorkspacePath
	}
	grant.ReferenceOwner, grant.ReferenceEntry = mount.Owner, entry
	return grant, nil
}

// openWorkspaceFile opens the file a grant names: a Reference Space's
// current content for one under its mount, otherwise the workspace's own.
func (a *Application) openWorkspaceFile(ctx context.Context, grant AccessGrant) (OpenedWorkspaceFile, error) {
	if grant.ReferenceOwner == "" {
		return a.working.OpenFile(ctx, grant.Workspace, grant.WorkspacePath)
	}
	return a.references.OpenCurrent(ctx, grant.ReferenceOwner, grant.ReferenceEntry)
}

func (a *Application) workspaceLink(grant AccessGrant) Link {
	expiresAt := a.now().UTC().Add(a.linkTTL)
	link := a.links.AccessLink(grant, expiresAt)
	return Link{URL: link.URL, Path: link.Path, ExpiresAt: expiresAt}
}

// readWorkspaceFile opens the workspace file a link names, under its own
// name; its type follows the name.
func (a *Application) readWorkspaceFile(ctx context.Context, grant AccessGrant) (OpenedFile, error) {
	opened, err := a.openWorkspaceFile(ctx, grant)
	if err != nil {
		return OpenedFile{}, err
	}
	if version := grant.WorkspaceVersion; version != nil && (opened.SizeBytes != version.SizeBytes || !opened.ModifiedAt.Equal(version.ModifiedAt)) {
		opened.Content.Close()
		return OpenedFile{}, ErrContentChanged
	}
	return OpenedFile{Content: opened.Content, File: StoredFile{
		Position: domain.FilePosition{Name: path.Base(string(grant.WorkspacePath))}, SizeBytes: opened.SizeBytes, ModifiedAt: opened.ModifiedAt,
	}}, nil
}

// RequestWorkspaceReclamation durably accepts the reclamation of a closed
// Work's workspace. Storage completes it on its own schedule, once every
// publication copying from the workspace has finished.
func (a *Application) RequestWorkspaceReclamation(ctx context.Context, workID, reference string) error {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(reference) == "" {
		return ErrInvalidRequest
	}
	return a.working.RequestReclamation(ctx, reference)
}

// ReclaimRequestedWorkspaces attempts every accepted reclamation; one still
// copying or failing is attempted again on the next run, until removed.
func (a *Application) ReclaimRequestedWorkspaces(ctx context.Context) error {
	return a.working.ReclaimRequested(ctx)
}
