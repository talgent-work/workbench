package application

import (
	"context"
	"errors"
	"io"
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
	// ErrWorkspaceNotCreated means the Work never had its workspace prepared.
	ErrWorkspaceNotCreated = errors.New("agent: the workspace has not been created")
	// ErrWorkspaceReclaimed means Storage reclaimed the workspace.
	ErrWorkspaceReclaimed = errors.New("agent: the workspace was reclaimed")
	// ErrWorkspaceClosed means the Work is Closed: its workspace is neither
	// browsed nor read, whether or not reclamation has finished.
	ErrWorkspaceClosed = errors.New("agent: the Work is closed, its workspace is unavailable")
	// ErrWorkspaceUnavailable means the Work's files stay on its Managed
	// Computer, which is not reachable now: a Disconnection.
	ErrWorkspaceUnavailable = errors.New("agent: the Computer is not reachable")
	// ErrInvalidWorkspaceFileLink means a link to a Managed Work's file is
	// unsigned, altered or expired.
	ErrInvalidWorkspaceFileLink = errors.New("agent: invalid or expired workspace file link")
)

// WorkspaceFileLinkPort reads the files Agent's own links grant, which are
// Managed Works' files on their machines.
type WorkspaceFileLinkPort interface {
	// LinkedWork is the Work a link grants a read of;
	// ErrInvalidWorkspaceFileLink for an unsigned, altered or expired one.
	LinkedWork(token string) (string, error)
	// OpenLink opens the file the link grants; ErrWorkspaceUnavailable while
	// its machine is not reachable.
	OpenLink(ctx context.Context, token string) (OpenedWorkspaceFile, error)
}

// WorkspaceFileLinks serves the links to Managed Works' files. A link admits
// its reader by itself, until the Work is Closed.
type WorkspaceFileLinks struct {
	works domain.AgentWorkRepository
	files WorkspaceFileLinkPort
}

func NewWorkspaceFileLinks(works domain.AgentWorkRepository, files WorkspaceFileLinkPort) (*WorkspaceFileLinks, error) {
	if works == nil || files == nil {
		return nil, errors.New("agent: Works and Managed workspace files are required for workspace file links")
	}
	return &WorkspaceFileLinks{works: works, files: files}, nil
}

// Open opens the file a link grants while its Work is not Closed: a Closed
// Work's workspace is not read, by a link issued earlier either.
func (l *WorkspaceFileLinks) Open(ctx context.Context, token string) (OpenedWorkspaceFile, error) {
	workID, err := l.files.LinkedWork(token)
	if err != nil {
		return OpenedWorkspaceFile{}, err
	}
	work, err := l.works.Get(ctx, workID)
	if errors.Is(err, domain.ErrWorkNotFound) {
		return OpenedWorkspaceFile{}, ErrInvalidWorkspaceFileLink
	}
	if err != nil {
		return OpenedWorkspaceFile{}, err
	}
	if work.ClosureConfirmed() {
		return OpenedWorkspaceFile{}, ErrWorkspaceClosed
	}
	return l.files.OpenLink(ctx, token)
}

// OpenedWorkspaceFile is one Managed Work's file a link reads, streamed from
// its machine; the reader closes Content.
type OpenedWorkspaceFile struct {
	Name     string
	Download bool
	// Limit bounds the bytes served, zero for the whole file.
	Limit   int64
	Content io.ReadCloser
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

// ViewedWorkspace names a Work's workspace: on a Cloud its Session Workspace
// and what its sandbox mounts inside it, its input source and its read-only
// mounts; on a Managed Computer the Work's directory on the machine.
type ViewedWorkspace struct {
	WorkID         string
	Reference      string
	InputSource    domain.WorkInputSource
	ReadOnlyMounts []domain.ReadOnlyMount
}

// viewedWorkspace is the workspace a Work's execution environment sees.
func viewedWorkspace(work *domain.AgentWork) ViewedWorkspace {
	return ViewedWorkspace{WorkID: work.ID, Reference: work.WorkspaceReference, InputSource: work.InputSource, ReadOnlyMounts: work.ReadOnlyMounts}
}

// WorkspaceViewPort reads a Work's workspace where its files are: Storage's
// Workspace View Query and Workspace File Access Link for a Cloud Work, the
// Computer itself for a Managed Work, unavailable during a Disconnection
// (ErrWorkspaceUnavailable). The Work's realization decides: a Work with
// neither a directory opened on its machine nor a Session Workspace, such as
// a project-scoped definition's Work, has nothing to read
// (ErrWorkspaceNotCreated).
type WorkspaceViewPort interface {
	WorkspaceView(ctx context.Context, workspace ViewedWorkspace, path string) (WorkspaceListing, error)
	PreviewWorkspaceFile(ctx context.Context, workspace ViewedWorkspace, path string) (WorkspaceFilePreview, error)
	WorkspaceFileLink(ctx context.Context, workspace ViewedWorkspace, path string) (WorkspaceFileLink, error)
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
	work, err := a.readableWorkspace(ctx, input)
	if err != nil {
		return WorkspaceListing{}, err
	}
	return a.WorkspaceViews.WorkspaceView(ctx, viewedWorkspace(work), input.Path)
}

// PreviewWorkspaceFile tells such a reader how one workspace file is shown,
// with a link for one inline read.
func (a *Application) PreviewWorkspaceFile(ctx context.Context, input ReadWorkspace) (WorkspaceFilePreview, error) {
	work, err := a.readableWorkspace(ctx, input)
	if err != nil {
		return WorkspaceFilePreview{}, err
	}
	return a.WorkspaceViews.PreviewWorkspaceFile(ctx, viewedWorkspace(work), input.Path)
}

// DownloadWorkspaceFile issues such a reader a link to download one
// workspace file under its own name.
func (a *Application) DownloadWorkspaceFile(ctx context.Context, input ReadWorkspace) (WorkspaceFileLink, error) {
	work, err := a.readableWorkspace(ctx, input)
	if err != nil {
		return WorkspaceFileLink{}, err
	}
	return a.WorkspaceViews.WorkspaceFileLink(ctx, viewedWorkspace(work), input.Path)
}

// readableWorkspace admits the reader as for the Work's message stream, then
// refuses a Closed Work: its workspace is reclaimed or about to be, and its
// readers are pointed to its Artifacts.
func (a *Application) readableWorkspace(ctx context.Context, input ReadWorkspace) (*domain.AgentWork, error) {
	if strings.TrimSpace(input.ActorUserID) == "" {
		return nil, domain.ErrInvalidWork
	}
	work, _, err := a.admittedReaderWork(ctx, AdmitWorkReader{ReaderID: input.ActorUserID, WorkID: input.WorkID, Admission: input.Admission})
	if err != nil {
		return nil, err
	}
	if work.ClosureConfirmed() {
		return nil, ErrWorkspaceClosed
	}
	// Compositions that build only part of the Application, as tests do,
	// leave the port unset; refuse instead of panicking.
	if a.WorkspaceViews == nil {
		return nil, errors.New("agent: workspace views are not configured")
	}
	return work, nil
}
