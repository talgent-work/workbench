//go:build integration

package agent_test

import (
	"context"
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
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/computer"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// lifecycleModule is the production Agent and Computer modules, HTTP mapping
// and persistence over one test database. Each begun AgentExecution is
// realized as execution integration would (moduleRuns); native facts enter the
// feedback API. A case seeds the Work it starts from in the database
// (work_seed_fixture_integration_test.go) and performs one transition.
type lifecycleModule struct {
	t   *testing.T
	ctx context.Context
	dsn string
	// db reads and seeds the database beside the module.
	db       *xorm.Engine
	port     *sessionCommandRecorder
	runs     *moduleRuns
	capacity *fake.Clientset
	client   agentprivatev1connect.AgentServiceClient
	feedback agentprivatev1connect.ExecutionFeedbackServiceClient
	recovery *command.RecoverWorkHandler
	app      *application.Application
	stop     func()
	// public reaches the module's public APIs as the entry forwards them.
	public *http.Client
	url    string
	// options compose beside the module, such as a case's own decorations.
	options []fx.Option
	// catalog replaces the module's Marketplace when a case sets it.
	catalog command.MarketplacePort
}

// availableQuota is the cluster quota of the test Cloud with room for ten runs.
func availableQuota() *corev1.ResourceQuota {
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	return &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
}

// newLifecycleModule opens a test database; the module starts on start, after
// the case seeded what it starts from.
func newLifecycleModule(t *testing.T) *lifecycleModule {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &lifecycleModule{t: t, ctx: ctx, dsn: dsn, db: db, port: &sessionCommandRecorder{}, runs: &moduleRuns{}, capacity: fake.NewClientset(availableQuota())}
}

// start composes and starts the module; a started module stops on cleanup.
func (m *lifecycleModule) start() *lifecycleModule {
	t := m.t
	t.Helper()
	db, err := xorm.NewEngine("mysql", m.dsn)
	require.NoError(t, err)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	catalog := moduleCatalog()
	if m.catalog != nil {
		catalog = fx.Replace(fx.Annotate(m.catalog, fx.As(new(command.MarketplacePort))))
	}
	app := fx.New(computer.Module, agent.Module, catalog, sessionStateTransport(t), fx.NopLogger, storageStub(), testRedis(t),
		fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, defaultBuiltIns, agent.ComputerConfig{Address: server.URL, Token: "computer"}, testCloud("10", "10Gi", "64")),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(func() kubernetes.Interface { return m.capacity }),
		fx.Provide(func() command.DispatcherPort { return m.port }),
		m.runs.option(),
		fx.Invoke(func(recovery *command.RecoverWorkHandler, app *application.Application) {
			m.recovery, m.app = recovery, app
		}),
		fx.Options(m.options...),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(m.ctx))
	var once sync.Once
	m.stop = func() {
		once.Do(func() {
			server.Close()
			require.NoError(t, app.Stop(context.Background()))
			require.NoError(t, db.Close())
		})
	}
	t.Cleanup(m.stop)
	m.public, m.url = server.Client(), server.URL
	transport := connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))
	m.client = agentprivatev1connect.NewAgentServiceClient(connect.NewClient(transport))
	m.feedback = agentprivatev1connect.NewExecutionFeedbackServiceClient(connect.NewClient(transport))
	return m
}

// restart stops the module and composes it again over the same database.
func (m *lifecycleModule) restart() {
	m.t.Helper()
	m.stop()
	m.start()
}

// control is the context of a call under the control token.
func (m *lifecycleModule) control() context.Context { return sessionControlRequest(m.ctx) }

// get reads the Work as its owner sees it.
func (m *lifecycleModule) get(workID string) *agentprivatev1.GetWorkResponse {
	m.t.Helper()
	response, err := m.client.GetWork(m.control(), &agentprivatev1.GetWorkRequest{SubjectId: workOwner, WorkId: workID})
	require.NoError(m.t, err)
	return response
}

// state is the Work's State as its owner reads it.
func (m *lifecycleModule) state(workID string) agentprivatev1.WorkState {
	m.t.Helper()
	return m.get(workID).State
}

// report delivers one Dispatcher feedback under the feedback token.
func (m *lifecycleModule) report(request *agentprivatev1.ReportRequest) error {
	ctx, info := connect.NewClientContext(m.ctx)
	info.RequestHeader().Set("Authorization", "Bearer feedback")
	_, err := m.feedback.Report(ctx, request)
	return err
}

// reportProcess delivers feedback of kind about a process of the Work.
func (m *lifecycleModule) reportProcess(workID string, kind agentprivatev1.FeedbackKind, processID string, exitCode int32) error {
	return m.report(&agentprivatev1.ReportRequest{WorkId: workID, Kind: kind, ProcessId: processID, ExitCode: exitCode})
}

// deliver hands input to the Work under inputID.
func (m *lifecycleModule) deliver(workID, inputID, text string) error {
	_, err := m.client.DeliverInput(m.control(), &agentprivatev1.DeliverInputRequest{SubjectId: workOwner, WorkId: workID, InputId: inputID, Input: text})
	return err
}

// resume and restartWork ask for the Work's next start as subject.
func (m *lifecycleModule) resume(subject, workID string) error {
	_, err := m.client.ResumeWork(m.control(), &agentprivatev1.ResumeWorkRequest{SubjectId: subject, WorkId: workID, RequestId: uuid.Must(uuid.NewV7()).String()})
	return err
}

func (m *lifecycleModule) restartWork(subject, workID string) error {
	_, err := m.client.RestartWork(m.control(), &agentprivatev1.RestartWorkRequest{SubjectId: subject, WorkId: workID, RequestId: uuid.Must(uuid.NewV7()).String()})
	return err
}

// locate finds the definition's Work of the case Work Key within the
// fixture's Work Caller.
func (m *lifecycleModule) locate(agentID, subject string) *agentprivatev1.LocateWorkResponse {
	m.t.Helper()
	located, err := m.client.LocateWork(m.control(), &agentprivatev1.LocateWorkRequest{SubjectId: workOwner, AgentId: agentID, WorkKey: &agentpublicv1.WorkKey{Kind: "case", Id: subject}, WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: workProject}})
	require.NoError(m.t, err)
	return located
}

// subjectCreation asks to create the definition's Work of the case Work Key with the
// fixture's capability differences.
func subjectCreation(agentID, subject string) *agentprivatev1.CreateWorkRequest {
	return &agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: workOwner, AgentId: agentID, WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: workProject}, WorkKey: &agentpublicv1.WorkKey{Kind: "case", Id: subject},
		CapabilityDifferences: &agentprivatev1.CapabilityDifferences{Prompt: &workPrompt, StartupAgent: &workStartupAgent}}
}
