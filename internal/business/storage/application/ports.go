package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

var (
	// ErrFileNotFound means no file is at the position.
	ErrFileNotFound = errors.New("storage: file not found")
	// ErrNameTaken means a Create or Rename found its target position taken.
	ErrNameTaken = errors.New("storage: file name is taken")
	// ErrInvalidSource means a publication source is not one regular file of
	// the Session Workspace outside every Directory mounted in it.
	ErrInvalidSource = domain.ErrInvalidPublicationSource
	// ErrSourceNotFound means the publication source does not exist.
	ErrSourceNotFound = errors.New("storage: publication source not found")
	// ErrInvalidLink means a link is malformed, forged or expired.
	ErrInvalidLink = errors.New("storage: invalid or expired link")
	// ErrInvalidWorkspacePath means a workspace path leaves the workspace,
	// crosses a symbolic link, or names no file a reader may open.
	ErrInvalidWorkspacePath = domain.ErrInvalidWorkspacePath
	// ErrContentChanged means a preview link's file was replaced after its
	// preview was decided.
	ErrContentChanged = errors.New("storage: the file changed since its preview was decided")
	// ErrInvalidRequest means a required identity is missing.
	ErrInvalidRequest = errors.New("storage: invalid request")
	// ErrPublicationInProgress means a publication still copies from a
	// reclaimed Directory, or a change admitted before its reclamation still
	// holds it, so its content stays for a later attempt.
	ErrPublicationInProgress = errors.New("storage: a publication or a change still holds the Directory")
	// ErrMappedViewBehind means a change was carried out in the Directory but
	// did not reach every mapped path, which shows fewer files until the
	// Mapped View is repaired.
	ErrMappedViewBehind = errors.New("storage: the Mapped View is behind its Directory until repaired")
)

// StagedContent names content staged completely on a Directory's backend,
// not yet visible; Commit makes it a file, and Discard removes it.
type StagedContent string

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

// Written reports whether Storage wrote the file: every file it writes
// records its origin, an uploader or a producing Work.
func (a FileAttributes) Written() bool { return a.UploaderUserID != "" || a.WorkID != "" }

// StoredFile is a file at its position as its backend holds it.
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

// ContentFile is one file of a Directory's whole content.
type ContentFile struct {
	Path    domain.ContentPath
	Content []byte
}

// WorkspaceEntryKind is what a workspace entry is.
type WorkspaceEntryKind string

const (
	WorkspaceDirectory WorkspaceEntryKind = "directory"
	WorkspaceFile      WorkspaceEntryKind = "file"
	// WorkspaceLink is a symbolic link: listed, never followed or opened.
	WorkspaceLink WorkspaceEntryKind = "link"
)

// WorkspaceEntry is one entry of a directory as a sandbox sees it. A file
// Storage wrote into a Directory carries its File position, so it is read
// and previewed as that file.
type WorkspaceEntry struct {
	Name       string
	Kind       WorkspaceEntryKind
	SizeBytes  int64
	ModifiedAt time.Time
	Position   *domain.FilePosition
}

// WorkspaceListing is one directory's first entries by name and how many it
// has in all.
type WorkspaceListing struct {
	Entries []WorkspaceEntry
	Total   int
}

// WorkspaceFileVersion is a workspace file's size and time; a Harness writes
// without records, so a change of either is a new content.
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

// MountLocation is where a Directory lies for a sandbox: a shared claim and
// the Directory's path in it.
type MountLocation struct {
	ClaimName string
	SubPath   string
}

// DirectoryBackendPort keeps Directories on one file system, the
// realization a Directory's Backend selects. It never follows a symbolic
// link, and new content it writes becomes visible by one atomic commit.
type DirectoryBackendPort interface {
	// Allocate creates the Directory's space; an existing space is kept.
	Allocate(ctx context.Context, directory domain.DirectoryID) error
	// PrepareMountPoints creates each mount point and every parent inside
	// the Directory, all owned by the sandbox user.
	PrepareMountPoints(ctx context.Context, directory domain.DirectoryID, points []domain.MountPoint) error
	// Locate is the Mount Grant location of the Directory.
	Locate(directory domain.DirectoryID) MountLocation
	// List lists the files Storage wrote directly into the Directory.
	List(ctx context.Context, directory domain.DirectoryID) ([]StoredFile, error)
	Stat(ctx context.Context, position domain.FilePosition) (StoredFile, error)
	// Stage writes the content completely, with its origin, where no reader
	// sees it.
	Stage(ctx context.Context, content io.Reader, attributes FileAttributes) (StagedContent, error)
	// Discard removes staged content that was not committed.
	Discard(staged StagedContent)
	// Commit makes staged content visible at the position in one atomic
	// step. A Create refuses a taken position with ErrNameTaken; a Replace
	// refuses an absent one with ErrFileNotFound and keeps the previous
	// content on failure.
	//
	// Every file command and content replacement takes the mapped paths of
	// its Directory, whose lock the caller holds, and follows them there:
	// a link that goes is removed before the Directory changes, and one that
	// comes is made after, so an interruption leaves the Mapped View with
	// fewer files, never more. A change carried out whose new link was not
	// made returns its result with ErrMappedViewBehind.
	Commit(ctx context.Context, staged StagedContent, position domain.FilePosition, replace bool, mappings []domain.MappedPath) (StoredFile, error)
	Rename(ctx context.Context, from, to domain.FilePosition, mappings []domain.MappedPath) (StoredFile, error)
	// Remove deletes the file and reports whether this call removed one; an
	// absent file is already removed.
	Remove(ctx context.Context, position domain.FilePosition, mappings []domain.MappedPath) (bool, error)
	Open(ctx context.Context, position domain.FilePosition) (OpenedFile, error)
	// Inspect finds the content structure Preview Policy judges, with the
	// facts of the same content.
	Inspect(ctx context.Context, position domain.FilePosition) (StoredFile, domain.ContentStructure, error)
	// ReplaceContent stages the files as the Directory's whole new content,
	// switches it in in one atomic step and removes every earlier content
	// before returning. The caller holds the Directory's lock.
	ReplaceContent(ctx context.Context, directory domain.DirectoryID, files []ContentFile, mappings []domain.MappedPath) error
	// ReadContent reads the given files of the current content, every file
	// when none is given; a path the content lacks is left out.
	ReadContent(ctx context.Context, directory domain.DirectoryID, paths []domain.ContentPath) ([]ContentFile, error)
	// ListEntries lists the first limit entries of a directory inside the
	// Directory, "." for its root, as a sandbox mounting it sees them: a
	// Directory replaced as a whole is read through its current content.
	ListEntries(ctx context.Context, directory domain.DirectoryID, path string, limit int) (WorkspaceListing, error)
	// OpenEntry opens one regular file inside the Directory as a sandbox
	// sees it; what is read is the content of that inode when opened.
	OpenEntry(ctx context.Context, directory domain.DirectoryID, path string) (OpenedWorkspaceFile, error)
	// InspectEntry finds the content structure Preview Policy judges in one
	// regular file inside the Directory, with the file's size and time.
	InspectEntry(ctx context.Context, directory domain.DirectoryID, path string) (WorkspaceFileVersion, domain.ContentStructure, error)
	// CopyPublicationSource opens one regular file inside the Directory
	// under its shared lock, held while read runs.
	CopyPublicationSource(ctx context.Context, directory domain.DirectoryID, source domain.WorkspacePath, read func(io.Reader) error) error
	// RemoveReclaimed removes the Directory's content once no publication
	// copies from it, or returns ErrPublicationInProgress.
	RemoveReclaimed(ctx context.Context, directory domain.DirectoryID) error
	// Lock takes the Directory's lock exclusively, across every process on
	// the file system, until the returned release runs.
	Lock(ctx context.Context, directory domain.DirectoryID) (func(), error)
	// LockMappedView takes the Mapped View's one lock exclusively, across
	// every process on the file system; a holder of a Directory's lock may
	// take it, never the reverse.
	LockMappedView(ctx context.Context) (func(), error)
	// MapFiles aligns a mapped path of the Mapped View with the Directory's
	// files: each appears there as the same file, and nothing else stays.
	// The caller holds the Directory's lock.
	MapFiles(ctx context.Context, directory domain.DirectoryID, path domain.MappedPath) error
	// UnmapFiles removes a mapped path's files and the folders it emptied.
	// The caller holds the lock of the Directory mapped there.
	UnmapFiles(ctx context.Context, path domain.MappedPath) error
}

// DirectoryBackends are the realizations by the Backend they keep.
type DirectoryBackends map[domain.Backend]DirectoryBackendPort

// errNoBackend means a Directory names a backend this Storage keeps none on.
var errNoBackend = errors.New("storage: no realization of the Directory's backend")

func (b DirectoryBackends) of(directory *domain.Directory) (DirectoryBackendPort, error) {
	backend, ok := b[directory.Backend]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errNoBackend, directory.Backend)
	}
	return backend, nil
}

// ContentInspectionPort finds the content structure Preview Policy judges in
// the leading bytes of a file Storage does not hold.
type ContentInspectionPort interface {
	InspectHead(head []byte) domain.ContentStructure
}

// ConverterPort renders office content to PDF.
type ConverterPort interface {
	// Render converts content named name. A rendering within pdfLimit comes
	// back with its PDF, which the caller closes; one over it comes back as
	// rendered with its size and no PDF.
	Render(ctx context.Context, name string, content io.Reader, pdfLimit int64) (domain.ConverterResult, io.ReadCloser, error)
}

// UploadGrant is what an upload link authorizes: one Attachment write.
type UploadGrant struct {
	Position       domain.FilePosition
	Replace        bool
	UploaderUserID string
	ContentType    string
}

// AccessGrant is what an Access Link authorizes: one read of one file of a
// Directory, by its position, or, with Workspace set, by its path as a
// sandbox sees it.
type AccessGrant struct {
	Directory domain.DirectoryID
	// Path is the file inside Directory: a file name for a position, or a
	// relative path for a workspace file.
	Path      string
	Workspace bool
	// WorkspaceVersion, when set, binds a preview read of a workspace file
	// to its size and time when its preview was decided.
	WorkspaceVersion *WorkspaceFileVersion
	Download         bool
	// ContentDigest, when set, binds the read to the content a preview was
	// admitted for: a file replaced since then is not served through it.
	ContentDigest string
	// PreviewCopy reads the content's Preview Copy instead of the file, and
	// only inline: a download is always the original.
	PreviewCopy bool
}

// AccessLink is an Access Link at the public address and as a path, which a
// Work resolves against the entry's origin.
type AccessLink struct {
	URL, Path string
}

// LinkSigningPort signs and verifies upload links and Access Links, and
// names the URL of a Share Link.
type LinkSigningPort interface {
	UploadURL(grant UploadGrant, expiresAt time.Time) string
	VerifyUpload(token string, now time.Time) (UploadGrant, error)
	AccessLink(grant AccessGrant, expiresAt time.Time) AccessLink
	VerifyAccess(token string, now time.Time) (AccessGrant, error)
	ShareURL(token string) string
}
