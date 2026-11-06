package application

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

var (
	// ErrFileNotFound means no file is at the position.
	ErrFileNotFound = errors.New("storage: file not found")
	// ErrNameTaken means a Create or Rename found its target position taken.
	ErrNameTaken = errors.New("storage: file name is taken")
	// ErrInvalidSource means a publication source is not one file of the
	// workspace outside inputs/: a directory, a symbolic link, inputs/, the
	// root or a path leaving the workspace.
	ErrInvalidSource = domain.ErrInvalidPublicationSource
	// ErrSourceNotFound means the publication source does not exist.
	ErrSourceNotFound = errors.New("storage: publication source not found")
	// ErrInvalidLink means a link is malformed, forged or expired.
	ErrInvalidLink = errors.New("storage: invalid or expired link")
	// ErrInvalidWorkspacePath means a workspace path leaves the workspace,
	// crosses a symbolic link, or names no file a reader may open.
	ErrInvalidWorkspacePath = domain.ErrInvalidWorkspacePath
	// ErrWorkspaceNotCreated means the Work never had its workspace prepared.
	ErrWorkspaceNotCreated = errors.New("storage: the workspace has not been created")
	// ErrWorkspaceReclaimed means the workspace was reclaimed, or is being.
	ErrWorkspaceReclaimed = errors.New("storage: the workspace was reclaimed")
	// ErrContentChanged means a preview link's file was replaced after its
	// preview was decided.
	ErrContentChanged = errors.New("storage: the file changed since its preview was decided")
	// ErrInvalidRequest means a required identity is missing.
	ErrInvalidRequest = errors.New("storage: invalid request")
)

// FileAttributes are the facts kept with a file besides its name, content
// and times: who uploaded an Attachment, which Work published an Artifact,
// and the declared media type. Any other attribute a file carries is never
// read.
type FileAttributes struct {
	UploaderUserID string
	WorkID         string
	ContentType    string
	// ContentDigest is the SHA-256 of the content, recorded when it is
	// written; Preview Copies are keyed by it.
	ContentDigest string
}

// StoredFile is a file at its position as persistent storage holds it.
type StoredFile struct {
	Position   domain.FilePosition
	SizeBytes  int64
	ModifiedAt time.Time
	Attributes FileAttributes
}

// OpenedFile is a readable file with its facts.
type OpenedFile struct {
	Content io.ReadSeekCloser
	File    StoredFile
}

// PersistentFilesPort is persistent storage of File Spaces (JuiceFS), each
// laid out by its owner. New content is staged outside every space's
// directory and becomes visible by one atomic commit.
type PersistentFilesPort interface {
	EnsureSpace(ctx context.Context, owner string) error
	List(ctx context.Context, owner string) ([]StoredFile, error)
	Stat(ctx context.Context, owner string, position domain.FilePosition) (StoredFile, error)
	// Write stages the content completely, then makes it visible at the
	// position in one atomic step, so no reader sees a partial file. A Create
	// refuses a taken position with ErrNameTaken; a Replace refuses an absent
	// one with ErrFileNotFound and keeps the previous content on failure.
	Write(ctx context.Context, owner string, position domain.FilePosition, content io.Reader, attributes FileAttributes, replace bool) (StoredFile, error)
	Rename(ctx context.Context, owner string, from domain.FilePosition, to domain.FilePosition) (StoredFile, error)
	// Remove deletes the file and reports whether this call removed one; an
	// absent file is already removed.
	Remove(ctx context.Context, owner string, position domain.FilePosition) (bool, error)
	Open(ctx context.Context, owner string, position domain.FilePosition) (OpenedFile, error)
	// Inspect finds the content structure Preview Policy judges, with the
	// facts of the same content.
	Inspect(ctx context.Context, owner string, position domain.FilePosition) (StoredFile, domain.ContentStructure, error)
}

// ContentInspectorPort finds the content structure Preview Policy judges in
// the leading bytes of a file Storage does not hold.
type ContentInspectorPort interface {
	InspectHead(head []byte) domain.ContentStructure
}

// WorkspaceEntryKind is what a workspace entry is.
type WorkspaceEntryKind string

const (
	WorkspaceDirectory WorkspaceEntryKind = "directory"
	WorkspaceFile      WorkspaceEntryKind = "file"
	// WorkspaceLink is a symbolic link: listed, never followed or opened.
	WorkspaceLink WorkspaceEntryKind = "link"
)

// WorkspaceEntry is one entry of a workspace directory. An Attachment under
// inputs/ carries its File position, so it is read as that Attachment.
type WorkspaceEntry struct {
	Name       string
	Kind       WorkspaceEntryKind
	SizeBytes  int64
	ModifiedAt time.Time
	Attachment *domain.FilePosition
}

// WorkspaceListing is one directory's first entries by name and how many it
// has in all.
type WorkspaceListing struct {
	Entries []WorkspaceEntry
	Total   int
}

// Capped says the directory has more entries than listed.
func (l WorkspaceListing) Capped() bool { return l.Total > len(l.Entries) }

// WorkspaceFileVersion is a workspace file's size and time; the sandbox
// writes without records, so a change of either is a new content.
type WorkspaceFileVersion struct {
	SizeBytes  int64
	ModifiedAt time.Time
}

// OpenedWorkspaceFile is one regular workspace file opened for reading.
type OpenedWorkspaceFile struct {
	Content    io.ReadSeekCloser
	SizeBytes  int64
	ModifiedAt time.Time
}

// WorkingFilesPort is working storage of Session Workspaces and HOMEs (NAS).
type WorkingFilesPort interface {
	// PrepareWorkspace returns the existing workspace or creates it with
	// inputs/ as the mount point of its input source's Attachments and a
	// mount point for each named Reference Space mount.
	PrepareWorkspace(ctx context.Context, reference string, mounts []string) (string, error)
	// PrepareHome returns the existing HOME of the owner key or creates it.
	PrepareHome(ctx context.Context, ownerKey string) (string, error)
	// ReadPublicationSource opens one regular workspace file under the
	// workspace's shared lock, held while read runs, without following
	// symbolic links.
	ReadPublicationSource(ctx context.Context, reference string, source domain.WorkspacePath, read func(io.Reader) error) error
	// List lists the first limit entries of a directory by name, with the
	// directory's entry count; symbolic links are listed, not followed.
	List(ctx context.Context, reference string, directory domain.WorkspacePath, limit int) (WorkspaceListing, error)
	// OpenFile opens one regular file without following symbolic links; what
	// is read is the content of that inode when opened.
	OpenFile(ctx context.Context, reference string, file domain.WorkspacePath) (OpenedWorkspaceFile, error)
	// InspectFile finds the content structure Preview Policy judges in one
	// regular file, with the file's size and time.
	InspectFile(ctx context.Context, reference string, file domain.WorkspacePath) (WorkspaceFileVersion, domain.ContentStructure, error)
	// RequestReclamation durably records a reclamation request.
	RequestReclamation(ctx context.Context, reference string) error
	// ReclaimRequested removes every requested workspace whose publications
	// have finished, leaving the rest for a later attempt.
	ReclaimRequested(ctx context.Context) error
}

// UploadGrant is what an upload link authorizes: one Attachment write.
type UploadGrant struct {
	Owner          string
	Name           string
	Replace        bool
	UploaderUserID string
	ContentType    string
}

// AccessGrant is what an Access Link authorizes: one read of one file of a
// File Space, or, with Workspace set, of a Session Workspace.
type AccessGrant struct {
	// Workspace names a Session Workspace; WorkspacePath the file in it.
	Workspace     string
	WorkspacePath domain.WorkspacePath
	// ReferenceOwner, set for a file under a Reference Space mount, reads
	// ReferenceEntry from that space's current content, as the Work's
	// sandbox sees it.
	ReferenceOwner, ReferenceEntry string
	// WorkspaceVersion, when set, binds a preview read to the file's size
	// and time when its preview was decided.
	WorkspaceVersion *WorkspaceFileVersion
	Owner            string
	Position         domain.FilePosition
	Download         bool
	// ContentDigest, when set, binds the read to the content a preview was
	// admitted for: a file replaced since then is not served through it.
	ContentDigest string
	// PreviewCopy reads the content's Preview Copy instead of the file, and
	// only inline: a download is always the original.
	PreviewCopy bool
}

// AccessLink is an Access Link at the public address and as a path, which a
// Work's sandbox resolves against the native API.
type AccessLink struct {
	URL, Path string
}

// LinkPort signs and verifies upload links and Access Links, and names the
// URL of a Share Link.
type LinkPort interface {
	UploadURL(grant UploadGrant, expiresAt time.Time) string
	VerifyUpload(token string, now time.Time) (UploadGrant, error)
	AccessLink(grant AccessGrant, expiresAt time.Time) AccessLink
	VerifyAccess(token string, now time.Time) (AccessGrant, error)
	ShareURL(token string) string
}

// MountGrant is the opaque location and access mode of a space for a sandbox.
type MountGrant struct {
	ClaimName string
	SubPath   string
	ReadOnly  bool
}

// MountLayoutPort maps prepared spaces to grants of the shared volume claims.
type MountLayoutPort interface {
	// Working grants a prepared workspace or HOME directory read-write.
	Working(directory string) MountGrant
	Attachments(owner string) MountGrant
	// Reference grants a Reference Space read-only.
	Reference(owner string) MountGrant
}
