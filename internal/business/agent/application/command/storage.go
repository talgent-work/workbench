package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// StoragePort reaches Storage's contract, which keeps every Directory a Work
// uses: the Mount Grants of a Cloud start, one per located Directory, the
// Work Reclamation Request of a Closed Work, the opening of a located
// Directory for its Space Token, and the File Space commands and queries of
// Standalone Work files and the Personal Library, each naming a Directory by
// its owner and name. Agent decides every access itself before calling
// Storage; Storage decides where each Directory lives.
type StoragePort interface {
	// AcquireMounts obtains the Mount Grants of a Cloud start, one per
	// requested Directory in request order, with each Directory's mount
	// points prepared; Storage opens each Directory as needed.
	AcquireMounts(ctx context.Context, workID string, mounts []domain.DirectoryMount) (domain.SandboxMounts, error)
	// RequestWorkReclamation sends the Work Reclamation Request, naming only
	// the Work; Storage accepts it idempotently and completes it on its own
	// schedule.
	RequestWorkReclamation(ctx context.Context, workID string) error
	// OpenDirectory opens a located Directory and returns its Space Token,
	// the same on every opening.
	OpenDirectory(ctx context.Context, directory domain.LocatedDirectory) (string, error)
	// ListFiles lists the files Storage wrote into one Directory, with their
	// Share Links.
	ListFiles(ctx context.Context, directory domain.Directory) ([]SpaceFile, error)
	// ListDirectoryFiles lists the files of the given Directories, without
	// Share Links.
	ListDirectoryFiles(ctx context.Context, directories []domain.Directory) ([]SpaceFile, error)
	IssueUpload(ctx context.Context, position FilePosition, spaceToken string, replace bool, contentType string, uploader FileOwner) (FileLink, error)
	RenameFile(ctx context.Context, position FilePosition, spaceToken, newName string, actor FileOwner) error
	DeleteFile(ctx context.Context, position FilePosition, spaceToken string, actor FileOwner) error
	IssueAccessLink(ctx context.Context, position FilePosition, inline bool) (FileLink, error)
	CreateShareLink(ctx context.Context, position FilePosition, actor string) (ShareLink, error)
	RevokeShareLinks(ctx context.Context, position FilePosition, actor string) error
	// FilePreview tells how a file is shown; render first asks for its
	// Preview Copy.
	FilePreview(ctx context.Context, position FilePosition, render bool) (FilePreview, error)
}

// FilePosition is a file's identity: its Directory and its name.
type FilePosition struct {
	Directory domain.Directory
	Name      string
}

var (
	// ErrFileNotFound means the Work has no such file the person may read.
	ErrFileNotFound = errors.New("agent: no such Work file")
	// ErrFileNameTaken means another file already has the name.
	ErrFileNameTaken = errors.New("agent: a Work file already has the name")
	// ErrInvalidFileName means the name is not one file name.
	ErrInvalidFileName = errors.New("agent: invalid Work file name")
)

// FileKind is what a file of a File Space is: an Attachment a person
// uploaded, or an Artifact a Work published.
type FileKind string

const (
	FileKindAttachment FileKind = "attachment"
	FileKindArtifact   FileKind = "artifact"
)

// SpaceFile is one file of a File Space, as Storage keeps it.
type SpaceFile struct {
	Directory   domain.Directory
	Kind        FileKind
	Name        string
	SizeBytes   int64
	ContentType string
	UpdatedAt   time.Time
	// Uploader is the owner that uploaded an Attachment.
	Uploader FileOwner
	// WorkID is the Work that published an Artifact.
	WorkID     string
	ShareLinks []ShareLink
}

// FileOwner is the owner of a Standalone Work changing its files, a person or
// an Application; exactly one is named.
type FileOwner struct{ UserID, ApplicationID string }

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
