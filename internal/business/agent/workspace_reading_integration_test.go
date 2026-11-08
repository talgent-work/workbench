//go:build integration

package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/business/computer"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// workspaceMachine stands in for the relay to Managed Computers: it serves
// the Work files of the Works its machine opened, refuses a Work it never
// opened as the machine does, and answers as unreachable while away.
type workspaceMachine struct {
	mu    sync.Mutex
	away  bool
	files map[string]map[string]string
}

func (m *workspaceMachine) set(change func(*workspaceMachine)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(m)
}

func (m *workspaceMachine) serve(mux *http.ServeMux) {
	read := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.away {
			http.Error(w, "computer not connected", http.StatusServiceUnavailable)
			return "", false
		}
		files, opened := m.files[r.PathValue("work")]
		if !opened {
			http.NotFound(w, r)
			return "", false
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			return "", true
		}
		content, found := files[path]
		if !found {
			http.NotFound(w, r)
			return "", false
		}
		return content, true
	}
	mux.HandleFunc("GET /relay/{computer}/works/{work}/files", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := read(w, r); !ok {
			return
		}
		m.mu.Lock()
		entries := []map[string]any{}
		for name, content := range m.files[r.PathValue("work")] {
			entries = append(entries, map[string]any{"name": name, "kind": "file", "size": len(content), "modified_at": time.Now().UTC()})
		}
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
	})
	mux.HandleFunc("GET /relay/{computer}/works/{work}/files/stat", func(w http.ResponseWriter, r *http.Request) {
		if content, ok := read(w, r); ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"name": r.URL.Query().Get("path"), "kind": "file", "size": len(content), "modified_at": time.Now().UTC()})
		}
	})
	mux.HandleFunc("GET /relay/{computer}/works/{work}/files/content", func(w http.ResponseWriter, r *http.Request) {
		if content, ok := read(w, r); ok {
			_, _ = io.WriteString(w, content)
		}
	})
}

// sessionWorkspaces stands in for Storage: it reads each Session Workspace
// it holds and judges every file shown as text.
type sessionWorkspaces struct {
	execution.StorageWorkspaces
	mu    sync.Mutex
	reads []string
}

func (s *sessionWorkspaces) WorkspaceView(_ context.Context, location domain.WorkspaceLocation, _ string) (application.WorkspaceListing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, location.SessionWorkspace)
	return application.WorkspaceListing{Total: 1, Entries: []application.WorkspaceEntry{{Name: "report.md", Kind: application.WorkspaceFile, SizeBytes: 8}}}, nil
}

func (s *sessionWorkspaces) PreviewWorkspaceFile(_ context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceFilePreview, error) {
	return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewDirect, Display: application.WorkspacePreviewText, Link: &application.WorkspaceFileLink{URL: "https://storage.test/" + location.SessionWorkspace + "/" + path, ExpiresAt: time.Now().Add(time.Minute)}}, nil
}

func (s *sessionWorkspaces) WorkspaceFileLink(_ context.Context, location domain.WorkspaceLocation, path string) (application.WorkspaceFileLink, error) {
	return application.WorkspaceFileLink{URL: "https://storage.test/" + location.SessionWorkspace + "/" + path + "?download", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (s *sessionWorkspaces) JudgeFilePreview(_ context.Context, _ string, size int64, _ []byte) (application.WorkspaceFilePreview, int64, error) {
	return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewDirect, Display: application.WorkspacePreviewText}, size, nil
}

type workspaceReading struct {
	server   *httptest.Server
	client   agentprivatev1connect.AgentServiceClient
	reading  agentprivatev1connect.WorkspaceReadingServiceClient
	machine  *workspaceMachine
	storage  *sessionWorkspaces
	agentID  string
	observer *xorm.Engine
}

func startWorkspaceReading(t *testing.T, ctx context.Context) *workspaceReading {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UnixMilli()
	_, err = db.Exec(`INSERT INTO computer (id, kind, owner_id, name, capacity, occupied, admission_policy, waiting_line, reachable, unreachable_since, talgent_computer_version, claude_code_version, credential_digest, credential_expires_at, state, version, created_at, updated_at, deleted_at)
 VALUES (?, 'managed', 'employee-a', 'Laptop', '{"desks":4}', '{}', 'reject', '[]', 1, 0, '1.0.0', '', ?, ?, 'active', 1, ?, ?, 0)`,
		managedLaptop, "0000000000000000000000000000000000000000000000000000000000000000", now+time.Hour.Milliseconds(), now, now)
	require.NoError(t, err)
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	kube := fake.NewClientset(quota)
	machine := &workspaceMachine{files: map[string]map[string]string{}}
	relayMux := http.NewServeMux()
	machine.serve(relayMux)
	relay := httptest.NewServer(relayMux)
	t.Cleanup(relay.Close)
	storage := &sessionWorkspaces{}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	computers := agent.ComputerConfig{Address: server.URL, Token: "computer", RelayAddress: relay.URL,
		MCPAddress: "https://workbench.test/mcp", ContentAddress: "https://workbench.test/content", TelemetryAddress: "https://workbench.test/otel",
		WorkspaceFileAddress: server.URL + "/v1/agent/workspace-files"}
	app := fx.New(computer.Module, agent.Module, moduleCatalog(), sessionStateTransport(t), fx.NopLogger, storageStub(), testRedis(t),
		fx.Provide(func() command.ExecutionEnvironmentPort { return realizedEnvironments{} }),
		fx.Decorate(func(execution.StorageWorkspaces) execution.StorageWorkspaces { return storage }),
		fx.Decorate(func(execution.PreviewJudge) execution.PreviewJudge { return storage }),
		fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, computers, testCloud("4", "4Gi", "64")),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(func() kubernetes.Interface { return kube }),
		fx.Provide(func() command.RunningHarnessPort { return &sessionCommandRecorder{} }),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(ctx))
	t.Cleanup(func() { require.NoError(t, app.Stop(context.Background())) })
	transport := connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))
	r := &workspaceReading{server: server, machine: machine, storage: storage, observer: db,
		client:  agentprivatev1connect.NewAgentServiceClient(connect.NewClient(transport)),
		reading: agentprivatev1connect.NewWorkspaceReadingServiceClient(connect.NewClient(transport))}
	created, err := r.client.CreateAgentDefinition(sessionControlRequest(ctx), &agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"employee-a"}, AgentName: "Fixture Agent",
		UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "employee-a"}, Purpose: "work",
		HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "employee-a", Namespace: "personal"},
	})
	require.NoError(t, err)
	r.agentID = created.AgentId
	return r
}

// create starts a Work on a Computer, with a Session Workspace when named.
func (r *workspaceReading) create(t *testing.T, ctx context.Context, computerID, workspace string) string {
	t.Helper()
	response, err := r.client.CreateWork(sessionControlRequest(ctx), &agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: "employee-a", AgentId: r.agentID, WorkspaceReference: workspace, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: uuid.NewString()}, ProjectId: "project-a", ComputerId: computerID})
	require.NoError(t, err)
	return response.WorkId
}

func (r *workspaceReading) browse(t *testing.T, ctx context.Context, workID string) *agentprivatev1.BrowseWorkspaceResponse {
	t.Helper()
	response, err := r.reading.BrowseWorkspace(sessionControlRequest(ctx), &agentprivatev1.BrowseWorkspaceRequest{WorkId: workID, ReaderId: "employee-a"})
	require.NoError(t, err)
	return response
}

func (r *workspaceReading) preview(t *testing.T, ctx context.Context, workID, path string) *agentprivatev1.PreviewWorkspaceFileResponse {
	t.Helper()
	response, err := r.reading.PreviewWorkspaceFile(sessionControlRequest(ctx), &agentprivatev1.PreviewWorkspaceFileRequest{WorkId: workID, ReaderId: "employee-a", Path: path})
	require.NoError(t, err)
	return response
}

func (r *workspaceReading) download(t *testing.T, ctx context.Context, workID, path string) *agentprivatev1.DownloadWorkspaceFileResponse {
	t.Helper()
	response, err := r.reading.DownloadWorkspaceFile(sessionControlRequest(ctx), &agentprivatev1.DownloadWorkspaceFileRequest{WorkId: workID, ReaderId: "employee-a", Path: path})
	require.NoError(t, err)
	return response
}

// open follows a file link as a reader's browser does.
func (r *workspaceReading) open(t *testing.T, link string) (int, string, string) {
	t.Helper()
	response, err := r.server.Client().Get(link)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, response.Header.Get("Content-Disposition"), string(body)
}

// A Cloud Work's workspace is its Session Workspace in Storage, browsed and
// previewed there with Storage's own links; a Cloud Work without one has
// nothing to read yet.
func TestACloudWorksWorkspaceIsReadFromItsSessionWorkspace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r := startWorkspaceReading(t, ctx)
	work := r.create(t, ctx, "", "case-workspace")

	listing := r.browse(t, ctx, work)
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_AVAILABLE, listing.State)
	require.Equal(t, "report.md", listing.Entries[0].Name)
	require.Equal(t, []string{"case-workspace"}, r.storage.reads)
	require.Equal(t, "https://storage.test/case-workspace/report.md", r.preview(t, ctx, work, "report.md").AccessUrl)
	require.Equal(t, "https://storage.test/case-workspace/report.md?download", r.download(t, ctx, work, "report.md").AccessUrl)

	bare := r.create(t, ctx, "", "")
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_NOT_CREATED, r.browse(t, ctx, bare).State)
	require.Len(t, r.storage.reads, 1, "Storage is not asked for a workspace that does not exist")
}

// A Managed Work's files are read on its machine through its Computer, and
// Agent's own links to them open there for a reader without a session. A
// directory the machine never opened is not created yet; while the machine is
// away the files are unavailable, and so are the links.
func TestAManagedWorksFilesAreReadThroughItsComputer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r := startWorkspaceReading(t, ctx)
	work := r.create(t, ctx, managedLaptop, "named-by-caller")
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_NOT_CREATED, r.browse(t, ctx, work).State, "the machine never opened its directory")

	r.machine.set(func(m *workspaceMachine) { m.files[work] = map[string]string{"notes.md": "# notes\n"} })
	listing := r.browse(t, ctx, work)
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_AVAILABLE, listing.State)
	require.Len(t, listing.Entries, 1)
	require.Equal(t, "notes.md", listing.Entries[0].Name)
	require.Empty(t, r.storage.reads, "a Managed Work has no Session Workspace")

	preview := r.preview(t, ctx, work, "notes.md")
	require.Equal(t, agentprivatev1.WorkspacePreviewMode_WORKSPACE_PREVIEW_MODE_DIRECT, preview.Mode)
	status, disposition, body := r.open(t, preview.AccessUrl)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, disposition, "inline")
	require.Equal(t, "# notes\n", body)
	download := r.download(t, ctx, work, "notes.md")
	status, disposition, body = r.open(t, download.AccessUrl)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, disposition, "attachment")
	require.Equal(t, "# notes\n", body)
	status, _, _ = r.open(t, download.AccessUrl+"x")
	require.Equal(t, http.StatusUnauthorized, status, "an altered link grants nothing")

	r.machine.set(func(m *workspaceMachine) { m.away = true })
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_UNAVAILABLE, r.browse(t, ctx, work).State)
	status, _, _ = r.open(t, download.AccessUrl)
	require.Equal(t, http.StatusServiceUnavailable, status)
}
