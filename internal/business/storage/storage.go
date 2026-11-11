// Package storage composes the Storage Bounded Context: Directories on
// JuiceFS and the NAS with the files in them, sandbox mounts, links,
// previews and Work reclamation.
package storage

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"connectrpc.com/connect/v2/connecthttp"
	integrationmessage "github.com/go-jimu/components/ddd/message"
	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"go.uber.org/fx"
	"xorm.io/xorm"

	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/contracts/gen/go/storage/public/v1/storagepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/application/query"
	applicationtask "github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/business/storage/infrastructure"
	storageconnect "github.com/talgent-work/workbench/internal/business/storage/transport/connectrpc"
	storagehttp "github.com/talgent-work/workbench/internal/business/storage/transport/http"
	"github.com/talgent-work/workbench/internal/business/storage/transport/taskprocessor"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
)

// defaultMaxUploadBytes bounds one upload when none is configured.
const defaultMaxUploadBytes = 1 << 30

// Database is the Workbench core's MySQL engine, holding Directories.
const Database = "workbench"

// Config is Storage's trusted deployment configuration.
type Config struct {
	// PersistentRoot is the controlplane's mount of the JuiceFS root.
	PersistentRoot string `json:"persistent-root" yaml:"persistent-root" toml:"persistent-root"`
	// WorkingRoot is the controlplane's mount of the NAS export root.
	WorkingRoot string `json:"working-root" yaml:"working-root" toml:"working-root"`
	// SandboxUID owns every NAS Directory and every mount point Storage
	// prepares. Sandboxes never run as root, so zero selects 1000, the native
	// sandbox image's user; a negative value keeps the controlplane's own
	// user.
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
	// Agent's Artifact commands carry its agent Principal instead.
	// AgentToken admits Agent, the core's caller, to the Directory and
	// Sandbox Mount contracts.
	AgentToken string `json:"agent-token" yaml:"agent-token" toml:"agent-token"`
	// DirectoryTokens admit further callers to the whole Directory contract,
	// for the Directories they locate; none is required.
	DirectoryTokens []string `json:"directory-tokens" yaml:"directory-tokens" toml:"directory-tokens"`
	// ContentCallers admit callers that replace and consult whole content:
	// opening, content replacement and reading, the Directory file query,
	// and mapping beneath their own mapping prefix; none is required.
	ContentCallers []ContentCallerConfig `json:"content-callers" yaml:"content-callers" toml:"content-callers"`
	// Mounts names the shared volume claims Mount Grants point into.
	Mounts MountConfig `json:"mounts" yaml:"mounts" toml:"mounts"`
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

// ContentCallerConfig is one caller of the whole-content part: its control
// token, and the mapped path beneath which it maps, empty when it maps
// nowhere. Prefixes never overlap, so no caller places files in, or
// occupies paths of, another caller's tree.
type ContentCallerConfig struct {
	Token         string `json:"token" yaml:"token" toml:"token"`
	MappingPrefix string `json:"mapping-prefix" yaml:"mapping-prefix" toml:"mapping-prefix"`
}

// contentCallers are the configured callers with their admitted prefixes.
func (c Config) contentCallers() ([]storageconnect.ContentCaller, error) {
	callers := make([]storageconnect.ContentCaller, 0, len(c.ContentCallers))
	var prefixes []domain.MappedPath
	for _, caller := range c.ContentCallers {
		prefix, err := domain.NewMappingPrefix(caller.MappingPrefix)
		if err != nil {
			return nil, fmt.Errorf("storage: mapping prefix %q is not a mapped path", caller.MappingPrefix)
		}
		if caller.MappingPrefix != "" {
			mapped := domain.MappedPath(caller.MappingPrefix)
			for _, other := range prefixes {
				if mapped.Overlaps(other) {
					return nil, fmt.Errorf("storage: mapping prefixes %q and %q overlap", other, mapped)
				}
			}
			prefixes = append(prefixes, mapped)
		}
		callers = append(callers, storageconnect.ContentCaller{Token: caller.Token, Prefix: prefix})
	}
	return callers, nil
}

// mappingPrefixes are the configured mapping prefixes, each the mapped path
// a standing reader's volume shows.
func (c Config) mappingPrefixes() []string {
	var prefixes []string
	for _, caller := range c.ContentCallers {
		if caller.MappingPrefix != "" {
			prefixes = append(prefixes, caller.MappingPrefix)
		}
	}
	return prefixes
}

// MountConfig names the claims sandboxes mount Directories from, one per
// backend; each Directory is granted as its own subPath.
type MountConfig struct {
	// NASClaim claims the NAS export root.
	NASClaim string `json:"nas-claim" yaml:"nas-claim" toml:"nas-claim"`
	// JuiceFSClaim claims the JuiceFS volume; OpenSandbox requires every
	// mount of one claim to share its read-only mode.
	JuiceFSClaim string `json:"juicefs-claim" yaml:"juicefs-claim" toml:"juicefs-claim"`
	// JuiceFSRoot is where PersistentRoot lies within the JuiceFS claim.
	JuiceFSRoot string `json:"juicefs-root" yaml:"juicefs-root" toml:"juicefs-root"`
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
	if strings.TrimSpace(c.Mounts.NASClaim) == "" || strings.TrimSpace(c.Mounts.JuiceFSClaim) == "" {
		return errors.New("storage: NAS and JuiceFS claims are required")
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
	if _, err := c.contentCallers(); err != nil {
		return err
	}
	return c.Preview.validate()
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
	// Each lane's tasks go through that lane's own client.
	fx.Provide(fx.Annotate(newApplication, fx.ParamTags(``, ``, pkgtaskqueue.EnqueuerTag(PreviewQueue), pkgtaskqueue.EnqueuerTag(MaintenanceQueue)))),
	fx.Provide(
		fx.Annotate(newRenderPreviewProcessor, fx.As(new(componenttaskqueue.Processor)), fx.ResultTags(pkgtaskqueue.ProcessorTag(PreviewQueue))),
		fx.Annotate(newMaintenanceProcessors, fx.ResultTags(pkgtaskqueue.ProcessorsTag(MaintenanceQueue))),
		fx.Annotate(applicationtask.NewLapsePreviewCopies, fx.ResultTags(pkgtaskqueue.PeriodicTaskTag(MaintenanceQueue))),
		fx.Annotate(applicationtask.NewRepairMappedView, fx.ResultTags(pkgtaskqueue.PeriodicTaskTag(MaintenanceQueue))),
	),
	fx.Invoke(registerHandlers, prepareMappedView),
)

// nativeSandboxUID is the user of the native sandbox image.
const nativeSandboxUID = 1000

// PreviewQueue is the taskqueue module Storage renders Preview Copies on;
// its worker concurrency bounds conversions per controlplane replica.
const PreviewQueue = applicationtask.Queue

// MaintenanceQueue is the taskqueue module Storage removes reclaimed content
// and lapsed Preview Copies and repairs the Mapped View on.
const MaintenanceQueue = applicationtask.MaintenanceQueue

func newApplication(config Config, engine *xorm.Engine, renderings, maintenance componenttaskqueue.Enqueuer, bus integrationmessage.Publisher, logger *slog.Logger) (*application.Application, error) {
	tasks := application.TaskLanes{Renderings: renderings, Maintenance: maintenance}
	return assemble(config, infrastructure.NewDirectoryRepository(engine), infrastructure.NewDirectoryQueries(engine), tasks, bus, logger)
}

// prepareMappedView creates the standing paths' folders at start, which the
// standing readers' volumes need before anything is mapped.
func prepareMappedView(lifecycle fx.Lifecycle, app *application.Application) {
	lifecycle.Append(fx.Hook{OnStart: app.PrepareMappedView})
}

func newRenderPreviewProcessor(app *application.Application, logger *slog.Logger) *taskprocessor.RenderPreviewProcessor {
	return taskprocessor.NewRenderPreviewProcessor(app, logger)
}

func newMaintenanceProcessors(app *application.Application, logger *slog.Logger) []componenttaskqueue.Processor {
	return []componenttaskqueue.Processor{
		taskprocessor.NewRemoveReclaimedContentProcessor(app, logger),
		taskprocessor.NewLapsePreviewCopiesProcessor(app),
		taskprocessor.NewRepairMappedViewProcessor(app),
	}
}

// Layouts of Storage's file systems: the published Directories of each,
// beside Storage's own areas. JuiceFS publishes its Directories as the
// originals mount; the preview area lies beside them.
const (
	juiceFSDirectories = "assets/directories"
	juiceFSPrivate     = "assets/storage"
	juiceFSPreviews    = "assets/previews"
	// juiceFSMappedView holds the Mapped View; a standing reader's volume
	// shows one mapped path beneath it.
	juiceFSMappedView = "assets/mapped-view"
	nasDirectories    = "directories"
	nasPrivate        = "storage"
)

// assemble builds Storage's application over its mechanisms; unset link
// lifetimes and preview settings take the application's defaults.
func assemble(config Config, directories domain.DirectoryRepository, queries query.DirectoryQueryRepository, tasks application.TaskLanes, bus integrationmessage.Publisher, logger *slog.Logger) (*application.Application, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return assembleWith(config, directories, mechanisms(config, queries, tasks, bus, logger), logger), nil
}

// assembleWith builds the application of a validated configuration over the
// given mechanisms.
func assembleWith(config Config, directories domain.DirectoryRepository, ports application.Ports, logger *slog.Logger) *application.Application {
	access, _ := parseDuration(config.LinkTTL, 0)
	upload, _ := parseDuration(config.UploadLinkTTL, 0)
	return application.NewApplication(directories, ports, application.LinkLifetimes{Access: access, Upload: upload}, config.Preview.settings(),
		application.WorkspaceSettings{ListEntries: config.WorkspaceListEntries, SentContentBytes: config.sentContentBytes}, logger)
}

// mechanisms are Storage's mechanisms over its configured file systems,
// link signing, converter, publisher and task queue.
func mechanisms(config Config, queries query.DirectoryQueryRepository, tasks application.TaskLanes, bus integrationmessage.Publisher, logger *slog.Logger) application.Ports {
	uid := config.SandboxUID
	if uid == 0 {
		uid = nativeSandboxUID
	}
	previews := infrastructure.NewPreviewCopies(config.PersistentRoot, juiceFSPreviews, path.Join(juiceFSPrivate, "pending"))
	return application.Ports{
		DirectoryQueries: queries,
		PreviewCopies:    previews,
		PreviewQueries:   previews,
		Backends: application.DirectoryBackends{
			domain.BackendJuiceFS: infrastructure.NewDirectoryFiles(infrastructure.DirectoryLayout{
				Root: config.PersistentRoot, Published: juiceFSDirectories, Private: juiceFSPrivate, MappedView: juiceFSMappedView, StandingPaths: config.mappingPrefixes(),
				Claim: config.Mounts.JuiceFSClaim, ClaimRoot: config.Mounts.JuiceFSRoot, SandboxUID: uid,
			}),
			domain.BackendNAS: infrastructure.NewDirectoryFiles(infrastructure.DirectoryLayout{
				Root: config.WorkingRoot, Published: nasDirectories, Private: nasPrivate,
				Claim: config.Mounts.NASClaim, SandboxUID: uid, SandboxWrites: true,
			}),
		},
		Links: infrastructure.NewLinks(config.PublicBaseURL, config.LinkSecret, infrastructure.LinkPaths{
			Upload: storagehttp.UploadPath, Content: storagehttp.ContentPath, SharePrefix: storagehttp.SharePrefix,
		}),
		Inspection: infrastructure.ContentInspector{},
		Converter:  infrastructure.NewGotenberg(config.Preview.GotenbergAddress, config.Preview.clientTimeout(), logger),
		Publisher:  bus,
		Tasks:      tasks,
	}
}

// artifactMessageOverhead is the room an Artifact command takes beside its
// content.
const artifactMessageOverhead = 64 << 10

func registerHandlers(app *application.Application, config Config, mux *http.ServeMux, logger *slog.Logger) {
	server := connectserver.New(logger)
	// Validated when the application was assembled.
	content, _ := config.contentCallers()
	storageprivatev1connect.RegisterDirectoryServiceHandler(server, storageconnect.NewDirectoryHandler(app, append([]string{config.AgentToken}, config.DirectoryTokens...), content))
	storageprivatev1connect.RegisterSandboxMountServiceHandler(server, storageconnect.NewSandboxMountHandler(app, config.AgentToken))
	connectserver.Mount(mux, server)
	mountArtifactService(mux, app, logger)
	limit := config.MaxUploadBytes
	if limit == 0 {
		limit = defaultMaxUploadBytes
	}
	storagehttp.Register(mux, app, limit)
}

// mountArtifactService serves the Work Agent's Artifact commands to the
// agent Principals the contract admits; a publication carries at most the
// content a Work sends, with room for the rest of its message.
func mountArtifactService(mux connecthttp.ServeMux, app *application.Application, logger *slog.Logger) {
	server := connectserver.New(logger, authn.NewPrincipalInterceptor())
	storagepublicv1connect.RegisterArtifactServiceHandler(server, storageconnect.NewArtifactHandler(app))
	connectserver.Mount(mux, server, connecthttp.WithReadMaxBytes(app.SentContentLimit()+artifactMessageOverhead))
}
