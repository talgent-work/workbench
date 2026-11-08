package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var (
	// ErrWorkspacePathNotFound means the workspace has no such directory or file.
	ErrWorkspacePathNotFound = errors.New("agent: workspace path not found")
	// ErrInvalidWorkspacePath means a path leaves the workspace, crosses a
	// symbolic link, or names nothing a reader may open.
	ErrInvalidWorkspacePath = errors.New("agent: invalid workspace path")
	// ErrWorkspaceReclaimed means Storage reclaimed the workspace.
	ErrWorkspaceReclaimed = errors.New("agent: the workspace was reclaimed")
	// ErrWorkspaceClosed means the Work is Closed: its workspace is neither
	// browsed nor read, whether or not reclamation has finished.
	ErrWorkspaceClosed = errors.New("agent: the Work is closed, its workspace is unavailable")
	// ErrInvalidWorkspaceFileLink means a link to a Managed Work's file is
	// unsigned, altered or expired.
	ErrInvalidWorkspaceFileLink = errors.New("agent: invalid or expired workspace file link")
)

// WorkspacePort reads a Work's workspace at the WorkspaceLocation its
// AgentWork names, for either realization: callers never tell a Session
// Workspace from a directory on a Managed Computer. A location whose files
// cannot be reached now answers domain.ErrWorkspaceUnavailable, and one with
// nothing to read yet domain.ErrWorkspaceNotCreated.
type WorkspacePort interface {
	// WorkspaceView, PreviewWorkspaceFile and WorkspaceFileLink serve the
	// Workspace View Query and Workspace File Access Link.
	WorkspaceView(ctx context.Context, location domain.WorkspaceLocation, path string) (WorkspaceListing, error)
	PreviewWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, path string) (WorkspaceFilePreview, error)
	WorkspaceFileLink(ctx context.Context, location domain.WorkspaceLocation, path string) (WorkspaceFileLink, error)
	// OpenWorkspaceFile opens the file one of Agent's own links grants, its
	// grant already verified; ErrInvalidWorkspaceFileLink where Agent issues
	// no such link.
	OpenWorkspaceFile(ctx context.Context, location domain.WorkspaceLocation, grant WorkspaceFileGrant) (OpenedWorkspaceFile, error)
	// RequestWorkspaceReclamation sends Storage's Workspace Reclamation
	// Request, an asynchronous request Storage accepts idempotently and
	// completes on its own schedule.
	RequestWorkspaceReclamation(ctx context.Context, workID, workspaceReference string) error
}

// WorkspaceEntryKind is what a workspace entry is.
type WorkspaceEntryKind string

const (
	WorkspaceDirectory WorkspaceEntryKind = "directory"
	WorkspaceFile      WorkspaceEntryKind = "file"
	WorkspaceLink      WorkspaceEntryKind = "link"
)

// WorkspaceEntry is one entry of a workspace directory as the sandbox sees
// it. An Attachment under inputs/ names itself, so the reader reads and
// previews it through the caller that supplied it.
type WorkspaceEntry struct {
	Name           string
	Kind           WorkspaceEntryKind
	SizeBytes      int64
	ModifiedAt     time.Time
	AttachmentName string
}

// WorkspaceListing is one directory's first entries by name and how many it
// has in all.
type WorkspaceListing struct {
	Entries []WorkspaceEntry
	Total   int
}

// WorkspacePreviewMode is how Storage shows a workspace file.
type WorkspacePreviewMode string

const (
	WorkspacePreviewRefused   WorkspacePreviewMode = "refused"
	WorkspacePreviewDirect    WorkspacePreviewMode = "direct"
	WorkspacePreviewTruncated WorkspacePreviewMode = "truncated"
)

// WorkspacePreviewDisplay is what the shown bytes are.
type WorkspacePreviewDisplay string

const (
	WorkspacePreviewText  WorkspacePreviewDisplay = "text"
	WorkspacePreviewPDF   WorkspacePreviewDisplay = "pdf"
	WorkspacePreviewImage WorkspacePreviewDisplay = "image"
)

// WorkspacePreviewRefusal is why a workspace file is not shown.
type WorkspacePreviewRefusal string

const (
	WorkspacePreviewUnsupported       WorkspacePreviewRefusal = "unsupported"
	WorkspacePreviewStructureMismatch WorkspacePreviewRefusal = "structure_mismatch"
	WorkspacePreviewEncrypted         WorkspacePreviewRefusal = "encrypted"
	WorkspacePreviewTooLarge          WorkspacePreviewRefusal = "too_large"
)

// WorkspaceFilePreview is how Storage shows one workspace file.
type WorkspaceFilePreview struct {
	Mode       WorkspacePreviewMode
	Display    WorkspacePreviewDisplay
	Refusal    WorkspacePreviewRefusal
	LimitBytes int64
	Link       *WorkspaceFileLink
}

// WorkspaceFileLink is a short-lived link to read one workspace file.
type WorkspaceFileLink struct {
	URL       string
	ExpiresAt time.Time
}

// ReadWorkspace names a Work, a path in its workspace, the reader and the
// reader's admission a trusted caller supplied.
type ReadWorkspace struct {
	ActorUserID, WorkID, Path string
	Admission                 domain.ReaderAdmission
}

// BrowseWorkspace serves Storage's Workspace View to a reader Work access
// admits, until the Work is Closed.
func (a *Application) BrowseWorkspace(ctx context.Context, input ReadWorkspace) (WorkspaceListing, error) {
	location, err := a.readableWorkspace(ctx, input)
	if err != nil {
		return WorkspaceListing{}, err
	}
	return a.Workspaces.WorkspaceView(ctx, location, input.Path)
}

// PreviewWorkspaceFile tells such a reader how one workspace file is shown,
// with a link for one inline read.
func (a *Application) PreviewWorkspaceFile(ctx context.Context, input ReadWorkspace) (WorkspaceFilePreview, error) {
	location, err := a.readableWorkspace(ctx, input)
	if err != nil {
		return WorkspaceFilePreview{}, err
	}
	return a.Workspaces.PreviewWorkspaceFile(ctx, location, input.Path)
}

// DownloadWorkspaceFile issues such a reader a link to download one
// workspace file under its own name.
func (a *Application) DownloadWorkspaceFile(ctx context.Context, input ReadWorkspace) (WorkspaceFileLink, error) {
	location, err := a.readableWorkspace(ctx, input)
	if err != nil {
		return WorkspaceFileLink{}, err
	}
	return a.Workspaces.WorkspaceFileLink(ctx, location, input.Path)
}

// readableWorkspace admits the reader as for the Work's message stream,
// refuses a Closed Work, whose workspace is reclaimed or about to be and
// whose readers are pointed to its Artifacts, and names where the Work's
// files are read.
func (a *Application) readableWorkspace(ctx context.Context, input ReadWorkspace) (domain.WorkspaceLocation, error) {
	if strings.TrimSpace(input.ActorUserID) == "" {
		return domain.WorkspaceLocation{}, domain.ErrInvalidWork
	}
	work, _, err := a.admittedReaderWork(ctx, AdmitWorkReader{ReaderID: input.ActorUserID, WorkID: input.WorkID, Admission: input.Admission})
	if err != nil {
		return domain.WorkspaceLocation{}, err
	}
	if work.ClosureConfirmed() {
		return domain.WorkspaceLocation{}, ErrWorkspaceClosed
	}
	// Compositions that build only part of the Application, as tests do,
	// leave the port unset; refuse instead of panicking.
	if a.Workspaces == nil {
		return domain.WorkspaceLocation{}, errors.New("agent: workspace views are not configured")
	}
	return work.LocateWorkspace()
}
