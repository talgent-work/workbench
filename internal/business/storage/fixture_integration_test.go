//go:build integration

package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/go-jimu/components/ddd/message"
	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	applicationtask "github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/business/storage/infrastructure"
	storageconnect "github.com/talgent-work/workbench/internal/business/storage/transport/connectrpc"
	storagehttp "github.com/talgent-work/workbench/internal/business/storage/transport/http"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
	"github.com/talgent-work/workbench/internal/pkg/storagepath"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// fixture is Storage over two real directories standing for JuiceFS and the
// NAS, with Directories in a real MySQL schema and links served over HTTP.
type fixture struct {
	t          *testing.T
	app        *application.Application
	server     *httptest.Server
	persistent string
	working    string
	// sandboxUID is the sandbox user every NAS Directory and mount point
	// belongs to.
	sandboxUID int
	// directories can be made to fail every Directory change, as an
	// interruption between a file operation and its record would.
	directories *interruptibleDirectories
	// nas can be made to fail allocations, as an interrupted allocation.
	nas *interruptibleBackend
	// rpc calls the Directory contract over Connect.
	rpc storageprivatev1connect.DirectoryServiceClient
	// mounts calls the Sandbox Mount contract over Connect as Agent, token "a".
	mounts storageprivatev1connect.SandboxMountServiceClient
	// converter stands for Gotenberg; queue for the task lanes.
	converter *fakeConverter
	queue     *fakeQueue
	published *recordedMessages
}

// fixtureMaxUploadBytes bounds one upload in these tests.
const fixtureMaxUploadBytes = 16 << 10

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, func(*Config) {})
}

// newFixtureWith is a fixture whose configuration configure adjusts.
func newFixtureWith(t *testing.T, configure func(*Config)) *fixture {
	t.Helper()
	engine, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	f := &fixture{t: t, persistent: t.TempDir(), working: t.TempDir(), sandboxUID: os.Getuid()}
	if f.sandboxUID == 0 {
		f.sandboxUID = nativeSandboxUID
	}
	mux := http.NewServeMux()
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	f.directories = &interruptibleDirectories{DirectoryRepository: infrastructure.NewDirectoryRepository(engine)}
	f.converter, f.queue, f.published = newFakeConverter(t), newFakeQueue(), &recordedMessages{}
	config := Config{
		PersistentRoot: f.persistent, WorkingRoot: f.working, SandboxUID: f.sandboxUID,
		PublicBaseURL: f.server.URL, LinkSecret: "link-secret",
		AgentToken: "a", DirectoryTokens: []string{"i"}, ContentCallers: []ContentCallerConfig{{Token: "k", MappingPrefix: "view"}},
		Mounts:  MountConfig{NASClaim: "nas-root", JuiceFSClaim: "juicefs-inputs", JuiceFSRoot: "talgent"},
		Preview: PreviewConfig{GotenbergAddress: f.converter.server.URL},
	}
	configure(&config)
	require.NoError(t, config.Validate())
	ports := mechanisms(config, infrastructure.NewDirectoryQueries(engine), application.TaskLanes{Renderings: f.queue, Maintenance: f.queue}, f.published, slog.Default())
	f.nas = &interruptibleBackend{DirectoryBackendPort: ports.Backends[domain.BackendNAS]}
	ports.Backends[domain.BackendNAS] = f.nas
	f.app = assembleWith(config, f.directories, ports, slog.Default())
	storagehttp.Register(mux, f.app, fixtureMaxUploadBytes)
	content, err := config.contentCallers()
	require.NoError(t, err)
	rpc := connectserver.New(slog.Default())
	storageprivatev1connect.RegisterDirectoryServiceHandler(rpc, storageconnect.NewDirectoryHandler(f.app, []string{config.AgentToken, "i"}, content))
	storageprivatev1connect.RegisterSandboxMountServiceHandler(rpc, storageconnect.NewSandboxMountHandler(f.app, config.AgentToken))
	connectserver.Mount(mux, rpc)
	client := connect.NewClient(connecthttp.NewTransport(f.server.Client(), f.server.URL))
	f.rpc = storageprivatev1connect.NewDirectoryServiceClient(client)
	f.mounts = storageprivatev1connect.NewSandboxMountServiceClient(client)
	return f
}

// viewPrefix is the mapping prefix of the content caller "k".
var viewPrefix = func() domain.MappingPrefix {
	prefix, err := domain.NewMappingPrefix("view")
	if err != nil {
		panic(err)
	}
	return prefix
}()

// mapAt maps a located Directory at a mapped path beneath "view".
func (f *fixture) mapAt(located application.Located, path string) error {
	return f.app.MapDirectory(f.t.Context(), located, path, viewPrefix)
}

// viewOf is where a mapped path lies on JuiceFS.
func (f *fixture) viewOf(path string) string {
	return filepath.Join(f.persistent, juiceFSMappedView, filepath.FromSlash(path))
}

// named is a Directory named by its owner's facts, condition then value.
func named(name string, facts ...string) application.Named {
	directory := application.Named{Name: name}
	for i := 0; i+1 < len(facts); i += 2 {
		directory.Owner = append(directory.Owner, domain.OwnerFact{Condition: facts[i], Value: facts[i+1]})
	}
	return directory
}

// fileSpace locates a permanent JuiceFS Directory.
func fileSpace(directory application.Named) application.Located {
	return application.Located{Directory: directory, Backend: domain.BackendJuiceFS, Lifetime: domain.LifetimePermanent}
}

// workDirectory locates a NAS Directory with its Work's lifetime.
func workDirectory(workID, name string) application.Located {
	return application.Located{Directory: named(name, "work", workID), Backend: domain.BackendNAS, Lifetime: domain.LifetimeWork}
}

func (f *fixture) open(located application.Located) string {
	f.t.Helper()
	token, err := f.app.OpenDirectory(f.t.Context(), located)
	require.NoError(f.t, err)
	return token
}

func at(directory application.Named, name string) application.Position {
	return application.Position{Directory: directory, Name: name}
}

// upload writes an Attachment through its upload link, as a browser would.
func (f *fixture) upload(directory application.Named, token, name, content string, replace bool) *http.Response {
	return f.uploadAs(directory, token, name, "text/markdown", content, replace)
}

// uploadAs uploads with the declared contentType, empty when none is known.
func (f *fixture) uploadAs(directory application.Named, token, name, contentType, content string, replace bool) *http.Response {
	f.t.Helper()
	link, err := f.app.IssueUpload(f.t.Context(), application.IssueUpload{Position: at(directory, name), SpaceToken: token, Replace: replace, UploaderUserID: "user-1", ContentType: contentType})
	require.NoError(f.t, err)
	request, _ := http.NewRequest(http.MethodPut, link.URL, strings.NewReader(content))
	response, err := http.DefaultClient.Do(request)
	require.NoError(f.t, err)
	f.t.Cleanup(func() { response.Body.Close() })
	return response
}

func (f *fixture) get(rawURL string) (int, string, http.Header) {
	f.t.Helper()
	response, err := http.Get(rawURL)
	require.NoError(f.t, err)
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body), response.Header
}

func (f *fixture) files(directory application.Named) map[string]application.DirectoryFile {
	f.t.Helper()
	files, err := f.app.ListFiles(f.t.Context(), directory)
	require.NoError(f.t, err)
	byName := map[string]application.DirectoryFile{}
	for _, file := range files {
		byName[file.Position.Name] = file
	}
	return byName
}

// folder is where a Directory lies on its file system in these tests.
func (f *fixture) folder(located application.Located) string {
	facts := make([]storagepath.OwnerFact, 0, len(located.Directory.Owner))
	for _, fact := range located.Directory.Owner {
		facts = append(facts, storagepath.OwnerFact{Condition: fact.Condition, Value: fact.Value})
	}
	if located.Backend == domain.BackendNAS {
		return filepath.Join(f.working, nasDirectories, storagepath.Directory(facts, located.Directory.Name))
	}
	return filepath.Join(f.persistent, juiceFSDirectories, storagepath.Directory(facts, located.Directory.Name))
}

// workspace mounts a Work's Session Workspace with Directories mounted in it
// at the given paths, and returns its layout.
func (f *fixture) workspace(workID string, mounts map[string]application.Located) application.Layout {
	f.t.Helper()
	root := workDirectory(workID, "workspace")
	layout := application.Layout{Workspace: root.Directory}
	request := application.MountRequest{Directory: root}
	requests := []application.MountRequest{{}}
	for path, located := range mounts {
		request.MountPoints = append(request.MountPoints, path)
		layout.Mounts = append(layout.Mounts, application.Mount{Path: path, Directory: located.Directory})
		requests = append(requests, application.MountRequest{Directory: located, ReadOnly: true})
	}
	requests[0] = request
	_, err := f.app.AcquireMounts(f.t.Context(), workID, requests)
	require.NoError(f.t, err)
	return layout
}

// workspaceFile writes a file into a Work's Session Workspace as its sandbox
// would.
func (f *fixture) workspaceFile(workID, relative, content string) {
	f.t.Helper()
	target := filepath.Join(f.folder(workDirectory(workID, "workspace")), relative)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(f.t, os.WriteFile(target, []byte(content), 0o644))
}

// interruptibleDirectories is the real repository whose saves can be made
// to fail.
type interruptibleDirectories struct {
	domain.DirectoryRepository
	failing atomic.Bool
}

var errInterrupted = errors.New("interrupted before the record")

func (d *interruptibleDirectories) Save(ctx context.Context, directory *domain.Directory) error {
	if d.failing.Load() {
		return errInterrupted
	}
	return d.DirectoryRepository.Save(ctx, directory)
}

// interruptibleBackend is the real NAS whose allocations can be made to fail.
type interruptibleBackend struct {
	application.DirectoryBackendPort
	failing atomic.Bool
	// meanwhile, when set, runs between an allocation's read and its space's
	// creation, as a concurrent change would.
	meanwhile func()
}

func (b *interruptibleBackend) Allocate(ctx context.Context, id domain.DirectoryID) error {
	if b.failing.Load() {
		return errInterrupted
	}
	if b.meanwhile != nil {
		b.meanwhile()
	}
	return b.DirectoryBackendPort.Allocate(ctx, id)
}

// fakeQueue keeps keyed tasks as the provider does: a task waiting or
// running for a key absorbs further enqueues of it.
type fakeQueue struct {
	mu    sync.Mutex
	tasks map[string]taskqueue.Task
	order []string
}

func newFakeQueue() *fakeQueue { return &fakeQueue{tasks: map[string]taskqueue.Task{}} }

func (q *fakeQueue) Enqueue(_ context.Context, task taskqueue.Task, _ ...taskqueue.EnqueueOption) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, waiting := q.tasks[task.ID()]; !waiting {
		q.tasks[task.ID()] = task
		q.order = append(q.order, task.ID())
	}
	return nil
}

// run works every queued task of the type once, as its lane's worker would;
// a task asked to retry stays for a later run.
func (q *fakeQueue) run(t *testing.T, kind taskqueue.TaskType, work func(taskqueue.Task) error, retry func(error) bool) {
	t.Helper()
	q.mu.Lock()
	var tasks []taskqueue.Task
	for _, id := range q.order {
		if task, ok := q.tasks[id]; ok && task.Type() == kind {
			tasks = append(tasks, task)
		}
	}
	q.mu.Unlock()
	for _, task := range tasks {
		err := work(task)
		if retry(err) {
			continue
		}
		require.NoError(t, err)
		q.mu.Lock()
		delete(q.tasks, task.ID())
		q.mu.Unlock()
	}
}

func (q *fakeQueue) size(kind taskqueue.TaskType) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := 0
	for _, task := range q.tasks {
		if task.Type() == kind {
			count++
		}
	}
	return count
}

// renderings works every queued rendering once.
func (f *fixture) renderings() {
	f.t.Helper()
	f.queue.run(f.t, applicationtask.RenderPreviewType, func(task taskqueue.Task) error {
		render, err := applicationtask.ParseRenderPreview(task.Payload())
		require.NoError(f.t, err)
		return f.app.RenderPreview(f.t.Context(), render)
	}, func(err error) bool { return errors.Is(err, application.ErrConverterBusy) })
}

// removals works every queued content removal once.
func (f *fixture) removals() {
	f.t.Helper()
	f.queue.run(f.t, applicationtask.RemoveReclaimedContentType, func(task taskqueue.Task) error {
		removal, err := applicationtask.ParseRemoveReclaimedContent(task.Payload())
		require.NoError(f.t, err)
		return f.app.RemoveReclaimedContent(f.t.Context(), removal)
	}, func(err error) bool { return errors.Is(err, application.ErrPublicationInProgress) })
}

// recordedMessages keeps every published message.
type recordedMessages struct {
	mu       sync.Mutex
	messages []message.Message
}

func (r *recordedMessages) Publish(_ context.Context, msg message.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
	return nil
}

func (r *recordedMessages) all() []message.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]message.Message(nil), r.messages...)
}

func fixtureFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("infrastructure/testdata/preview", name))
	require.NoError(t, err)
	return string(content)
}
