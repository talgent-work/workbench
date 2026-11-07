// Package application implements Storage's use cases: File Spaces and their
// files, Reference Spaces, Share and Access Links, Artifact publication, sandbox mounts and
// workspace reclamation. Storage calls no other context; callers decide
// business admission before calling.
package application

import (
	"log/slog"
	"time"

	"github.com/go-jimu/components/taskqueue"

	"github.com/talgent-work/workbench/internal/business/storage/application/task"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Application is Storage's use-case entry.
type Application struct {
	spaces     domain.FileSpaceRepository
	persistent PersistentFilesPort
	working    WorkingFilesPort
	links      LinkPort
	mounts     MountLayoutPort
	references ReferenceSpacesPort
	previews   PreviewCopiesPort
	inspector  ContentInspectorPort
	converter  ConverterPort
	rendering  RenderingTasksPort
	enqueuer   taskqueue.Enqueuer
	// fileChanges publishes committed file changes; nil publishes none.
	fileChanges FileChangesPort
	preview     PreviewSettings
	// workspaceListLimit bounds the entries one workspace directory lists.
	workspaceListLimit int
	// sentContentLimit bounds the content a Work without a Session
	// Workspace sends.
	sentContentLimit int
	linkTTL          time.Duration
	uploadTTL        time.Duration
	now              func() time.Time
	logger           *slog.Logger
}

// Ports are the storage mechanisms Storage's use cases run on.
type Ports struct {
	Persistent PersistentFilesPort
	Working    WorkingFilesPort
	Links      LinkPort
	Mounts     MountLayoutPort
	// References keeps every Reference Space.
	References ReferenceSpacesPort
	Previews   PreviewCopiesPort
	// Inspector reads the structure of leading bytes Storage is handed.
	Inspector ContentInspectorPort
	// Converter renders Preview Copies.
	Converter ConverterPort
	// RenderingTasks tells queued and running renderings.
	RenderingTasks RenderingTasksPort
	// Enqueuer queues renderings on Storage's preview lane.
	Enqueuer taskqueue.Enqueuer
	// FileChanges publishes committed file changes.
	FileChanges FileChangesPort
}

// PreviewSettings are Storage's preview configuration: every limit, how long
// Preview Copies and recorded failures hold, and how long a rendering may take.
type PreviewSettings struct {
	Limits    domain.PreviewLimits
	Retention time.Duration
	Rendering task.RenderingPolicy
}

const (
	defaultPreviewRetention = 30 * 24 * time.Hour
	// A conversion has 60 s and its client waits 65 s, so the converter's own
	// timeout is what answers; an attempt runs 70 s so the client's timeout,
	// not the attempt's, ends it; the task ends 90 s after its request.
	defaultRenderingAttempt  = 70 * time.Second
	defaultRenderingDeadline = 90 * time.Second
)

// withDefaults fills every unset setting with its default.
func (s PreviewSettings) withDefaults() PreviewSettings {
	s.Limits = s.Limits.WithDefaults()
	if s.Retention <= 0 {
		s.Retention = defaultPreviewRetention
	}
	if s.Rendering.Attempt <= 0 {
		s.Rendering.Attempt = defaultRenderingAttempt
	}
	if s.Rendering.Deadline <= 0 {
		s.Rendering.Deadline = defaultRenderingDeadline
	}
	return s
}

// LinkLifetimes bound the links Storage issues.
type LinkLifetimes struct {
	// Access bounds an Access Link.
	Access time.Duration
	// Upload bounds an upload link. It is short because the person's right
	// to write was decided when it was issued: the shorter it lives, the
	// smaller the window in which an upload lands after the owner's context
	// stopped admitting writes.
	Upload time.Duration
}

const (
	defaultLinkTTL   = 15 * time.Minute
	defaultUploadTTL = 2 * time.Minute
)

// WorkspaceSettings bound how Session Workspaces are read, and what a Work
// without one sends instead.
type WorkspaceSettings struct {
	// ListEntries bounds the entries one directory lists (1000).
	ListEntries int
	// SentContentBytes bounds the content a Work without a Session Workspace
	// sends (domain.MaxSentContentBytes). Only tests lower it: content at the
	// real bound costs seconds to send and store.
	SentContentBytes int
}

// defaultWorkspaceListEntries is a conservative starting point.
const defaultWorkspaceListEntries = 1000

func (s WorkspaceSettings) listLimit() int {
	if s.ListEntries <= 0 {
		return defaultWorkspaceListEntries
	}
	return s.ListEntries
}

func (s WorkspaceSettings) sentContentLimit() int {
	if s.SentContentBytes <= 0 {
		return domain.MaxSentContentBytes
	}
	return s.SentContentBytes
}

// SentContentLimit is the most content a Work without a Session Workspace
// sends to publish an Artifact.
func (a *Application) SentContentLimit() int { return a.sentContentLimit }

func NewApplication(spaces domain.FileSpaceRepository, ports Ports, lifetimes LinkLifetimes, preview PreviewSettings, workspace WorkspaceSettings, logger *slog.Logger) *Application {
	if lifetimes.Access <= 0 {
		lifetimes.Access = defaultLinkTTL
	}
	if lifetimes.Upload <= 0 {
		lifetimes.Upload = defaultUploadTTL
	}
	if logger == nil {
		logger = slog.Default()
	}
	preview = preview.withDefaults()
	return &Application{
		spaces: spaces, persistent: ports.Persistent, working: ports.Working, links: ports.Links, mounts: ports.Mounts, references: ports.References, previews: ports.Previews, inspector: ports.Inspector,
		converter: ports.Converter, rendering: ports.RenderingTasks, enqueuer: ports.Enqueuer, fileChanges: ports.FileChanges, preview: preview, workspaceListLimit: workspace.listLimit(),
		sentContentLimit: workspace.sentContentLimit(),

		linkTTL: lifetimes.Access, uploadTTL: lifetimes.Upload, now: time.Now, logger: logger.With("component", "storage.application"),
	}
}
