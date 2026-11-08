package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// StoragePort reaches Storage's contract, which keeps every space a Work
// uses: the Mount Grants of a Cloud start, the Workspace Reclamation Request
// of a Closed Work, the File Spaces Agent names by a Personal Work's
// identity, the Owner File Query and the resolution of input sources to their
// File Space owners. Agent decides every access itself before calling
// Storage; Storage decides where each space lives.
type StoragePort interface {
	// AcquireMounts obtains the Mount Grants of a Cloud start; Storage
	// decides whether each space is created or reused.
	AcquireMounts(ctx context.Context, request domain.SandboxMountRequest) (domain.SandboxMounts, error)
	// RequestWorkspaceReclamation sends the Workspace Reclamation Request, an
	// asynchronous request Storage accepts idempotently and completes on its
	// own schedule.
	RequestWorkspaceReclamation(ctx context.Context, workID, workspaceReference string) error
	OpenFileSpace(ctx context.Context, owner string) (domain.WorkInputSource, error)
	// ResolveOwners names the owner of each input source's File Space, in
	// order; an unknown one resolves to "".
	ResolveOwners(ctx context.Context, inputSources []string) ([]string, error)
	ListFiles(ctx context.Context, owner string) ([]SpaceFile, error)
	ListOwnerFiles(ctx context.Context, owners []string) ([]SpaceFile, error)
	IssueAttachmentUpload(ctx context.Context, space domain.WorkInputSource, owner, name string, replace bool, contentType, uploader string) (FileLink, error)
	RenameAttachment(ctx context.Context, space domain.WorkInputSource, owner, name, newName, actor string) error
	DeleteFile(ctx context.Context, space domain.WorkInputSource, owner string, kind FileKind, name, actor string) error
	IssueAccessLink(ctx context.Context, owner string, kind FileKind, name string, inline bool) (FileLink, error)
	CreateShareLink(ctx context.Context, owner string, kind FileKind, name, actor string) (ShareLink, error)
	RevokeShareLinks(ctx context.Context, owner string, kind FileKind, name, actor string) error
	// FilePreview tells how a file is shown; render first asks for its
	// Preview Copy.
	FilePreview(ctx context.Context, owner string, kind FileKind, name string, render bool) (FilePreview, error)
}

var (
	// ErrFileNotFound means the Work has no such file the person may read.
	ErrFileNotFound = errors.New("agent: no such Work file")
	// ErrFileNameTaken means another file already has the name.
	ErrFileNameTaken = errors.New("agent: a Work file already has the name")
	// ErrInvalidFileName means the name is not one file name.
	ErrInvalidFileName = errors.New("agent: invalid Work file name")
)

// FileKind is where a file sits in its File Space.
type FileKind string

const (
	FileKindAttachment FileKind = "attachment"
	FileKindArtifact   FileKind = "artifact"
)

// SpaceFile is one file of a File Space, as Storage keeps it.
type SpaceFile struct {
	Owner          string
	Kind           FileKind
	Name           string
	SizeBytes      int64
	ContentType    string
	UpdatedAt      time.Time
	UploaderUserID string
	// WorkID is the Work that published an Artifact.
	WorkID     string
	ShareLinks []ShareLink
}

// ShareLink is one active Share Link of a file.
type ShareLink struct {
	Token, URL, CreatedByUserID string
	CreatedAt                   time.Time
}

// FileLink is a short-lived link to read or write one file.
type FileLink struct {
	URL, Path string
	ExpiresAt time.Time
}

// PreviewMode is how a file is shown.
type PreviewMode string

const (
	PreviewRefused   PreviewMode = "refused"
	PreviewCopy      PreviewMode = "preview_copy"
	PreviewRender    PreviewMode = "render"
	PreviewDirect    PreviewMode = "direct"
	PreviewTruncated PreviewMode = "truncated"
)

// PreviewDisplay is what the shown bytes are.
type PreviewDisplay string

const (
	PreviewText  PreviewDisplay = "text"
	PreviewPDF   PreviewDisplay = "pdf"
	PreviewImage PreviewDisplay = "image"
)

// PreviewRefusal is why a file is not shown.
type PreviewRefusal string

const (
	PreviewUnsupported       PreviewRefusal = "unsupported"
	PreviewStructureMismatch PreviewRefusal = "structure_mismatch"
	PreviewEncrypted         PreviewRefusal = "encrypted"
	PreviewTooLarge          PreviewRefusal = "too_large"
	PreviewCorrupt           PreviewRefusal = "corrupt"
	PreviewConverterRefused  PreviewRefusal = "converter_refused"
	PreviewRenderingTooLarge PreviewRefusal = "rendering_too_large"
)

// FilePreview is how a file is shown; Link reads the shown bytes once.
type FilePreview struct {
	Mode       PreviewMode
	Display    PreviewDisplay
	Refusal    PreviewRefusal
	LimitBytes int64
	Link       *FileLink
	Rendering  bool
}
