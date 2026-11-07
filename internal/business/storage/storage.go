// Package storage composes the Storage Bounded Context: File Spaces and
// their files and Reference Spaces on JuiceFS, Session Workspaces and HOMEs on the NAS, sandbox
// mounts, links and workspace reclamation.
package storage

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2/connecthttp"
	integrationmessage "github.com/go-jimu/components/ddd/message"
	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"
	"xorm.io/xorm"

	"github.com/talgent-work/contracts/gen/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/application/messagepublisher"
	applicationtask "github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/business/storage/infrastructure"
	storageconnect "github.com/talgent-work/workbench/internal/business/storage/transport/connectrpc"
	storagehttp "github.com/talgent-work/workbench/internal/business/storage/transport/http"
	"github.com/talgent-work/workbench/internal/business/storage/transport/taskprocessor"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
)

// defaultMaxUploadBytes bounds one upload when none is configured.
const defaultMaxUploadBytes = 1 << 30

// Database is the Workbench core's MySQL engine, holding File Spaces.
const Database = "workbench"

// Config is Storage's trusted deployment configuration.
type Config struct {
	// PersistentRoot is the controlplane's mount of the JuiceFS root.
	PersistentRoot string `json:"persistent-root" yaml:"persistent-root" toml:"persistent-root"`
	// WorkingRoot is the controlplane's mount of the NAS export root.
	WorkingRoot string `json:"working-root" yaml:"working-root" toml:"working-root"`
	// SandboxUID owns every new workspace and HOME. Sandboxes never run as
	// root, so zero selects 1000, the native sandbox image's
	// user; a negative value keeps the controlplane's own user.
	SandboxUID int `json:"sandbox-uid" yaml:"sandbox-uid" toml:"sandbox-uid"`
	// PublicBaseURL prefixes every link, such as /api behind the gateway.
	PublicBaseURL string `json:"public-base-url" yaml:"public-base-url" toml:"public-base-url"`
	// LinkSecret signs upload links and Access Links.
	LinkSecret string `json:"link-secret" yaml:"link-secret" toml:"link-secret"`
	// LinkTTL bounds Access Links, such as 15m.
	LinkTTL string `json:"link-ttl" yaml:"link-ttl" toml:"link-ttl"`
	// UploadLinkTTL bounds upload links, such as 2m; an upload must start
	// within it.
	UploadLinkTTL string `json:"upload-link-ttl" yaml:"upload-link-ttl" toml:"upload-link-ttl"`
	// MaxUploadBytes bounds one upload; zero means 1 GiB.
	MaxUploadBytes int64 `json:"max-upload-bytes" yaml:"max-upload-bytes" toml:"max-upload-bytes"`
	// Control tokens of the callers of Storage's private contracts. A Work
	// Agent's Artifact commands carry its verified Work identity instead.
	// AgentToken admits Agent, the core's caller, to the File Space and
	// Sandbox Mount contracts.
	AgentToken string `json:"agent-token" yaml:"agent-token" toml:"agent-token"`
	// FileSpaceTokens admit further callers that open and change File Spaces
	// for the owners they name; none is required.
	FileSpaceTokens []string `json:"file-space-tokens" yaml:"file-space-tokens" toml:"file-space-tokens"`
	// ReferenceSpaceTokens admit callers to the Reference Space Contract and
	// the Owner File Query; none is required.
	ReferenceSpaceTokens []string `json:"reference-space-tokens" yaml:"reference-space-tokens" toml:"reference-space-tokens"`
	// Mounts names the shared volume claims Mount Grants point into.
	Mounts MountConfig `json:"mounts" yaml:"mounts" toml:"mounts"`
	// ReclaimInterval is how often accepted reclamations are attempted.
	ReclaimInterval string `json:"reclaim-interval" yaml:"reclaim-interval" toml:"reclaim-interval"`
	// Preview is Storage's preview configuration.
	Preview PreviewConfig `json:"preview" yaml:"preview" toml:"preview"`
	// WorkspaceListEntries bounds the entries one workspace directory lists;
	// zero means 1000, a conservative starting point.
	WorkspaceListEntries int `json:"workspace-list-entries" yaml:"workspace-list-entries" toml:"workspace-list-entries"`
	// sentContentBytes bounds the content a Work without a Session Workspace
	// sends; zero means domain.MaxSentContentBytes. It is unexported because
	// only tests lower it, to exercise the bound without sending 64 MiB.
	sentContentBytes int
}

// MountConfig names the claims sandboxes mount.
type MountConfig struct {
	// WorkingClaim claims the NAS export root, mounted read-write.
	WorkingClaim string `json:"working-claim" yaml:"working-claim" toml:"working-claim"`
	// InputsClaim claims the JuiceFS volume again for read-only mounts.
	InputsClaim string `json:"inputs-claim" yaml:"inputs-claim" toml:"inputs-claim"`
	// InputsRoot is where PersistentRoot lies within the inputs claim.
	InputsRoot string `json:"inputs-root" yaml:"inputs-root" toml:"inputs-root"`
}

// Validate rejects a configuration Storage cannot serve with.
func (c Config) Validate() error {
	if strings.TrimSpace(c.PersistentRoot) == "" || strings.TrimSpace(c.WorkingRoot) == "" {
		return errors.New("storage: persistent and working roots are required")
	}
	if strings.TrimSpace(c.LinkSecret) == "" {
		return errors.New("storage: link secret is required")
	}
	if strings.TrimSpace(c.AgentToken) == "" {
		return errors.New("storage: Agent's control token is required")
	}
	if strings.TrimSpace(c.Mounts.WorkingClaim) == "" || strings.TrimSpace(c.Mounts.InputsClaim) == "" {
		return errors.New("storage: working and inputs claims are required")
	}
	if _, err := parseDuration(c.LinkTTL, time.Minute); err != nil {
		return err
	}
	if _, err := parseDuration(c.UploadLinkTTL, time.Minute); err != nil {
		return err
	}
	if c.WorkspaceListEntries < 0 {
		return errors.New("storage: workspace list entries must not be negative")
	}
	if c.MaxUploadBytes < 0 {
		return errors.New("storage: max upload bytes must not be negative")
	}
	if err := c.Preview.validate(); err != nil {
		return err
	}
	_, err := parseDuration(c.ReclaimInterval, time.Second)
	return err
}

func parseDuration(value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, errors.New("storage: durations must be positive, such as 15m")
	}
	return duration, nil
}

var Module = fx.Module(
	"domain.storage",
	pkgmysql.Bind(Database),
	pkgtaskqueue.Bind(PreviewQueue),
	fx.Provide(newApplication),
	fx.Provide(fx.Annotate(newRenderPreviewProcessor, fx.As(new(componenttaskqueue.Processor)), fx.ResultTags(pkgtaskqueue.ProcessorTag(PreviewQueue)))),
	fx.Invoke(registerHandlers),
	fx.Invoke(runMaintenance),
)

// nativeSandboxUID is the user of the native sandbox image.
const nativeSandboxUID = 1000

// PreviewQueue is the taskqueue module Storage renders Preview Copies on;
// its worker concurrency bounds conversions per controlplane replica.
const PreviewQueue = applicationtask.Queue

// Rendering is how Storage queues renderings and reads their state.
type Rendering struct {
	Enqueuer componenttaskqueue.Enqueuer
	Tasks    infrastructure.TaskStates
}

func newApplication(config Config, engine *xorm.Engine, enqueuer componenttaskqueue.Enqueuer, tasks *pkgtaskqueue.KeyedTasks, bus integrationmessage.Publisher, logger *slog.Logger) (*application.Application, error) {
	return assemble(config, infrastructure.NewFileSpaceRepository(engine), Rendering{Enqueuer: enqueuer, Tasks: tasks}, messagepublisher.NewFileChangePublisher(bus, logger), logger)
}

func newRenderPreviewProcessor(app *application.Application, logger *slog.Logger) *taskprocessor.RenderPreviewProcessor {
	return taskprocessor.NewRenderPreviewProcessor(app, logger)
}

// assemble builds Storage's application over its storage mechanisms and the
// publisher of its file changes; unset
// link lifetimes and preview settings take the application's defaults.
func assemble(config Config, spaces domain.FileSpaceRepository, rendering Rendering, fileChanges application.FileChangesPort, logger *slog.Logger) (*application.Application, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	access, _ := parseDuration(config.LinkTTL, 0)
	upload, _ := parseDuration(config.UploadLinkTTL, 0)
	uid := config.SandboxUID
	if uid == 0 {
		uid = nativeSandboxUID
	}
	persistent := infrastructure.NewPersistentFiles(config.PersistentRoot)
	ports := application.Ports{
		Persistent: persistent,
		Previews:   persistent,
		Inspector:  infrastructure.ContentInspector{},
		Working:    infrastructure.NewWorkingFiles(config.WorkingRoot, uid),
		Links: infrastructure.NewLinks(config.PublicBaseURL, config.LinkSecret, infrastructure.LinkPaths{
			Upload: storagehttp.UploadPath, Content: storagehttp.ContentPath, SharePrefix: storagehttp.SharePrefix,
		}),
		References:     infrastructure.NewReferenceSpaces(config.PersistentRoot),
		Mounts:         infrastructure.MountLayout{WorkingClaim: config.Mounts.WorkingClaim, InputsClaim: config.Mounts.InputsClaim, InputsRoot: config.Mounts.InputsRoot},
		Converter:      infrastructure.NewGotenberg(config.Preview.GotenbergAddress, config.Preview.clientTimeout(), logger),
		RenderingTasks: infrastructure.RenderingTasks{Tasks: rendering.Tasks, Queue: applicationtask.Queue},
		Enqueuer:       rendering.Enqueuer,
		FileChanges:    fileChanges,
	}
	return application.NewApplication(spaces, ports, application.LinkLifetimes{Access: access, Upload: upload}, config.Preview.settings(), application.WorkspaceSettings{ListEntries: config.WorkspaceListEntries, SentContentBytes: config.sentContentBytes}, logger), nil
}

// artifactMessageOverhead is the room an Artifact command takes beside its
// content.
const artifactMessageOverhead = 64 << 10

// AgentArtifactProcedures are the Work Agent's Artifact commands, each taking
// its Space Token beside its Work credential.
var AgentArtifactProcedures = []string{
	storageprivatev1connect.ArtifactServiceCreateArtifactProcedure,
	storageprivatev1connect.ArtifactServiceReplaceArtifactProcedure,
	storageprivatev1connect.ArtifactServiceDeleteArtifactProcedure,
}

func registerHandlers(app *application.Application, config Config, mux *http.ServeMux, logger *slog.Logger) {
	server := connectserver.New(logger)
	storageprivatev1connect.RegisterFileSpaceServiceHandler(server, storageconnect.NewFileSpaceHandler(app, append([]string{config.AgentToken}, config.FileSpaceTokens...), config.ReferenceSpaceTokens))
	storageprivatev1connect.RegisterSandboxMountServiceHandler(server, storageconnect.NewSandboxMountHandler(app, config.AgentToken))
	storageprivatev1connect.RegisterReferenceSpaceServiceHandler(server, storageconnect.NewReferenceSpaceHandler(app, config.ReferenceSpaceTokens...))
	connectserver.Mount(mux, server)
	mountArtifactService(mux, app, logger)
	limit := config.MaxUploadBytes
	if limit == 0 {
		limit = defaultMaxUploadBytes
	}
	storagehttp.Register(mux, app, limit)
}

// mountArtifactService serves the Work Agent's Artifact commands; a
// publication carries at most the content a Work sends, with room for the
// rest of its message.
func mountArtifactService(mux connecthttp.ServeMux, app *application.Application, logger *slog.Logger) {
	server := connectserver.New(logger)
	storageprivatev1connect.RegisterArtifactServiceHandler(server, storageconnect.NewArtifactHandler(app))
	connectserver.Mount(mux, server, connecthttp.WithReadMaxBytes(app.SentContentLimit()+artifactMessageOverhead))
}

// previewSweepInterval is how often lapsed Preview Copies and recorded
// failures are removed.
const previewSweepInterval = time.Hour

// runMaintenance attempts accepted workspace reclamations until each
// completes, and removes lapsed Preview Copies and recorded failures.
// Replicas share the reclamations through the workspace locks; sweeps are
// idempotent.
func runMaintenance(lc fx.Lifecycle, config Config, app *application.Application, logger *slog.Logger) {
	interval, _ := parseDuration(config.ReclaimInterval, 30*time.Second)
	runPeriodically(lc, interval, func(ctx context.Context) {
		if err := app.ReclaimRequestedWorkspaces(ctx); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "workspace reclamation remains pending", "error", err)
		}
	})
	runPeriodically(lc, previewSweepInterval, func(ctx context.Context) {
		if err := app.SweepPreviewCopies(ctx); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "lapsed preview copies remain", "error", err)
		}
	})
}

// runPeriodically runs work at start and on every interval until stop.
func runPeriodically(lc fx.Lifecycle, interval time.Duration, work func(context.Context)) {
	var cancel context.CancelFunc
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			go func() {
				defer close(done)
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					work(ctx)
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
