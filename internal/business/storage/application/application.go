// Package application implements Storage's use cases over its one kind of
// space, the Directory: opening and mounting located Directories, the files
// Storage writes into them, whole content replacement, Share and Access
// Links, Artifact publication, Workspace Views, previews and Work
// reclamation. Storage calls no other context; callers decide business
// admission before calling.
package application

import (
	"log/slog"
	"time"

	"github.com/go-jimu/components/ddd/message"
	"github.com/go-jimu/components/taskqueue"

	"github.com/talgent-work/workbench/internal/business/storage/application/query"
	"github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Application is Storage's use-case entry.
type Application struct {
	directories      domain.DirectoryRepository
	directoryQueries query.DirectoryQueryRepository
	previewCopies    domain.PreviewCopyRepository
	previewQueries   query.PreviewCopyQueryRepository
	backends         DirectoryBackends
	links            LinkSigningPort
	inspection       ContentInspectionPort
	converter        ConverterPort
	// publisher publishes committed file changes; nil publishes none.
	publisher message.Publisher
	tasks     TaskLanes
	preview   PreviewSettings
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

// Ports are the mechanisms Storage's use cases run on.
type Ports struct {
	DirectoryQueries query.DirectoryQueryRepository
	PreviewCopies    domain.PreviewCopyRepository
	PreviewQueries   query.PreviewCopyQueryRepository
	// Backends keep each Directory on the backend its Backend names.
	Backends   DirectoryBackends
	Links      LinkSigningPort
	Inspection ContentInspectionPort
	Converter  ConverterPort
	// Publisher publishes committed file changes.
	Publisher message.Publisher
	// Tasks queues renderings and content removals, each on its own lane.
	Tasks TaskLanes
}

// TaskLanes are the task queue lanes Storage enqueues on, each through its
// own client.
type TaskLanes struct {
	// Renderings queues Render preview.
	Renderings taskqueue.Enqueuer
	// Maintenance queues Remove reclaimed content.
	Maintenance taskqueue.Enqueuer
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

// SentContentLimit is the most content a Work without a Session Workspace
// sends to publish an Artifact.
func (a *Application) SentContentLimit() int { return a.sentContentLimit }

func NewApplication(directories domain.DirectoryRepository, ports Ports, lifetimes LinkLifetimes, preview PreviewSettings, workspace WorkspaceSettings, logger *slog.Logger) *Application {
	if lifetimes.Access <= 0 {
		lifetimes.Access = defaultLinkTTL
	}
	if lifetimes.Upload <= 0 {
		lifetimes.Upload = defaultUploadTTL
	}
	if workspace.ListEntries <= 0 {
		workspace.ListEntries = defaultWorkspaceListEntries
	}
	if workspace.SentContentBytes <= 0 {
		workspace.SentContentBytes = domain.MaxSentContentBytes
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Application{
		directories: directories, directoryQueries: ports.DirectoryQueries, previewCopies: ports.PreviewCopies, previewQueries: ports.PreviewQueries,
		backends: ports.Backends, links: ports.Links, inspection: ports.Inspection, converter: ports.Converter, publisher: ports.Publisher, tasks: ports.Tasks,
		preview: preview.withDefaults(), workspaceListLimit: workspace.ListEntries, sentContentLimit: workspace.SentContentBytes,
		linkTTL: lifetimes.Access, uploadTTL: lifetimes.Upload, now: time.Now, logger: logger.With("component", "storage.application"),
	}
}
