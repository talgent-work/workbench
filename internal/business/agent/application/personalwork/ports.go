// Package personalwork is a person's own use of their Agent: the Personal
// Works they start for themselves, their files and sharing, and the
// person's Personal Library. Agent decides every access itself before calling
// Storage, which keeps the files.
package personalwork

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var (
	ErrInvalidRequest = errors.New("agent: invalid personal work request")
	// ErrReaderUnknown means the User Directory has no such person.
	ErrReaderUnknown = errors.New("agent: the reader is not in the User Directory")
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

// FileSpaces is Storage's File Space Contract and Owner File Query as Agent
// uses them. Agent names a Personal Work's File Space by the Work's identity;
// other Works' spaces it knows only by their input sources.
type FileSpaces interface {
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

// DirectoryUser is one person in the User Directory.
type DirectoryUser struct {
	UserID, Username, DisplayName, AvatarURL string
}

// UserDirectory is Account's User Directory: everyone in the deployment's
// one enterprise, the scope a Work without a Project is shared within.
type UserDirectory interface {
	SearchUsers(ctx context.Context, query string, limit int) ([]DirectoryUser, error)
	GetUsers(ctx context.Context, userIDs []string) ([]DirectoryUser, error)
}

// PersonalWorkSummary is one Personal Work as its owner's list shows it.
type PersonalWorkSummary struct {
	WorkID, Title string
	// State is the Work State as Works are shown.
	State   string
	Waiting bool
	// StateReason is why it stopped, when it is Stopped.
	StateReason string
	// DisconnectedSince is the origin of its current Disconnection; zero
	// while observed.
	DisconnectedSince    time.Time
	CreatedAt, UpdatedAt time.Time
}

// LibraryWork is one Work whose Artifacts a person's Personal Library holds:
// one of their Personal Works, or one they hold a Work Share in, shared by
// its owner.
type LibraryWork struct {
	WorkID, Title string
	InputSource   string
	// Personal tells the Work is a Personal Work, whose File Space it owns.
	Personal bool
	// SharedBy is the owner who shared it; empty for the person's own Work.
	SharedBy string
}

// Works reads the Works a person's own pages show.
type Works interface {
	// ListPersonalWorks lists a definition's Works without a Subject or a
	// Project, most recently updated first.
	ListPersonalWorks(ctx context.Context, agentID string, limit int) ([]PersonalWorkSummary, error)
	// ListLibraryWorks lists the person's Personal Works under their
	// definition and the Works with an input source they hold a Work Share
	// in, closed ones included, at most limit.
	ListLibraryWorks(ctx context.Context, person, agentID string, limit int) ([]LibraryWork, error)
}
